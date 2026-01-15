# Monitoring Stack

This guide describes the Grafana-based monitoring stack deployed by the PTD CLI for workload observability.

## Overview

PTD deploys a complete observability stack to each workload cluster consisting of:

- **Grafana Alloy**: Metrics and log collection agent (deployed as a DaemonSet)
- **Mimir**: Prometheus-compatible metrics storage and querying
- **Loki**: Log aggregation and querying
- **Grafana**: Visualization and dashboard UI

## Architecture

### Data Flow

```
┌─────────────────────────────────────────────────────────────┐
│                      Workload Cluster                        │
│                                                              │
│  ┌──────────────┐                                           │
│  │ Grafana      │                                           │
│  │ Alloy        │ (DaemonSet - runs on every node)         │
│  │              │                                           │
│  └──────┬───────┘                                           │
│         │                                                    │
│         ├─── Metrics ───┬─────────────────────────────┐    │
│         │               │                              │    │
│         │               ▼                              │    │
│         │      ┌─────────────────┐                     │    │
│         │      │ Local Mimir     │                     │    │
│         │      │ (workload-only) │                     │    │
│         │      └────────┬────────┘                     │    │
│         │               │                              │    │
│         │               ▼                              │    │
│         │      ┌─────────────────┐                     │    │
│         │      │ Grafana UI      │                     │    │
│         │      │                 │                     │    │
│         │      └─────────────────┘                     │    │
│         │                                              │    │
│         └─── Logs ──────────────────────┐             │    │
│                                          │             │    │
│                                          ▼             │    │
│                                 ┌─────────────────┐   │    │
│                                 │ Local Loki      │   │    │
│                                 │ (workload-only) │   │    │
│                                 └────────┬────────┘   │    │
│                                          │             │    │
│                                          ▼             │    │
│                                 ┌─────────────────┐   │    │
│                                 │ Grafana UI      │   │    │
│                                 │                 │   │    │
│                                 └─────────────────┘   │    │
│                                                        │    │
└────────────────────────────────────────────────────────┼───┘
                                                         │
                             Metrics Only (for alerting)│
                                                         │
                                                         ▼
                                              ┌──────────────────┐
                                              │ Control Room     │
                                              │ Mimir            │
                                              │                  │
                                              └──────────────────┘
```

### Key Design Principles

**Metrics**: Dual-write pattern
- Sent to **local Mimir** for workload-specific dashboards and queries
- Sent to **control room Mimir** for centralized alerting and cross-workload monitoring

**Logs**: Workload boundary isolation
- Sent **only to local Loki** within the workload
- Logs never leave the workload boundary
- Each workload has complete control over its own log data

## Components

### Grafana Alloy

Grafana Alloy is the telemetry collection agent that runs on every node in the cluster.

**Deployment**: DaemonSet in the `alloy` namespace

**Configuration** (see `python-pulumi/src/ptd/pulumi_resources/grafana_alloy.py`):
- Scrapes metrics from:
  - Kubernetes pods in `posit-team`, `posit-team-system`, and `loki` namespaces
  - Node exporters (CPU, memory, disk, network)
  - kube-state-metrics for cluster state
  - Blackbox exporter for health checks
  - Cloud provider metrics for managed storage and database services
- Collects logs from:
  - Kubernetes pods in `posit-team` and `posit-team-system` namespaces
  - Optionally system logs via journald (controlled by `grafana_scrape_system_logs`)
- Runs with clustering enabled for high availability

**Helm Chart**: `grafana/alloy`

**Key Configuration** (from `aws_workload_helm.py:1127-1258`):
```yaml
alloy:
  clustering:
    enabled: true
  mounts:
    extra:
      - name: mimir-auth
        mountPath: /etc/mimir/
        readOnly: true
    varlog: true  # If grafana_scrape_system_logs enabled
  securityContext:
    privileged: true  # If grafana_scrape_system_logs enabled
tolerations:
  - key: workload-type
    operator: Equal
    value: session
    effect: NoSchedule
```

**Authentication**: Alloy uses basic authentication when writing metrics to the control room Mimir. Credentials are stored in a Kubernetes Secret (`mimir-auth`) and mounted into the Alloy pods.

### Mimir

Mimir is a horizontally scalable, long-term storage for Prometheus metrics.

**Deployment**: Distributed deployment in the `mimir` namespace

**Storage Backend**: Object storage (S3 or Azure Blob Storage, configured per workload)

**Helm Chart**: `grafana/mimir-distributed`

**Key Configuration** (from `aws_workload_helm.py:473-604`):
```yaml
mimir:
  structuredConfig:
    blocks_storage:
      backend: <s3 or azure>
      storage_prefix: blocks
    limits:
      max_global_series_per_user: 800000
      max_label_names_per_series: 45

ingester:
  replicas: <configurable>
  persistentVolume:
    size: 20Gi

compactor:
  replicas: <configurable>
  persistentVolume:
    size: 20Gi

store_gateway:
  replicas: <configurable>
  persistentVolume:
    size: 20Gi
```

**Endpoints**:
- Gateway: `http://mimir-gateway.mimir.svc.cluster.local/prometheus`
- Push API: `http://mimir-gateway.mimir.svc.cluster.local/api/v1/push`

### Loki

Loki is a log aggregation system designed to store and query logs efficiently.

**Deployment**: Distributed deployment in the `loki` namespace

**Storage Backend**: Object storage (S3 or Azure Blob Storage, configured per workload)

**Helm Chart**: `grafana/loki`

**Key Configuration** (from `aws_workload_helm.py:270-393`):
```yaml
loki:
  auth_enabled: false
  storage:
    type: <s3 or azure>
    bucketNames:
      chunks: <workload-prefix>-<bucket-name>
      ruler: <workload-prefix>-<bucket-name>
      admin: <workload-prefix>-<bucket-name>
  limits_config:
    max_cache_freshness_per_query: 10m
    query_timeout: 300s
    reject_old_samples: true
    reject_old_samples_max_age: 168h  # 7 days
    split_queries_by_interval: 15m
    volume_enabled: true
  storage_config:
    hedging:
      at: 250ms
      max_per_second: 20
      up_to: 3

backend:
  replicas: <configurable>
read:
  replicas: <configurable>
write:
  replicas: <configurable>
```

**Endpoints**:
- Gateway: `http://loki-gateway.loki.svc.cluster.local`
- Push API: `http://loki-gateway.loki.svc.cluster.local/loki/api/v1/push`

### Grafana

Grafana provides the visualization layer for metrics and logs.

**Deployment**: Single deployment in the `grafana` namespace

**Helm Chart**: `grafana/grafana`

**Data Sources** (from `aws_workload_helm.py:444-466`):
```yaml
datasources:
  - name: Loki
    type: loki
    access: proxy
    url: http://loki-gateway.loki.svc.cluster.local
    isDefault: true
  - name: Mimir
    type: prometheus
    access: proxy
    url: http://mimir-gateway.mimir.svc.cluster.local/prometheus
    isDefault: false
```

**Authentication**: Configured with proxy authentication via Traefik forward auth. Users are automatically signed up with Editor role.

**Access**: Available at `https://grafana.<workload-domain>`


## Accessing Monitoring Data

### Grafana UI

Access Grafana at `https://grafana.<workload-domain>` for metrics visualization and log exploration.


## Related Documentation

- [Grafana Alloy Documentation](https://grafana.com/docs/alloy/latest/)
- [Mimir Documentation](https://grafana.com/docs/mimir/latest/)
- [Loki Documentation](https://grafana.com/docs/loki/latest/)
- [Grafana Documentation](https://grafana.com/docs/grafana/latest/)
