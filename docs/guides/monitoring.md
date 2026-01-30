# Monitoring Stack

PTD deploys a Grafana-based observability stack to each workload cluster:

- **Grafana Alloy**: Metrics and log collection (DaemonSet on every node)
- **Mimir**: Prometheus-compatible metrics storage
- **Loki**: Log aggregation
- **Grafana**: Visualization UI at `https://grafana.<workload-domain>`

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                      Workload Cluster                       │
│                                                             │
│  Grafana Alloy (DaemonSet)                                  │
│       │                                                     │
│       ├─── Metrics ──→ Local Mimir ──→ Grafana UI           │
│       │                     │                               │
│       │                     └──────────→ Control Room Mimir │
│       │                                  (for alerting)     │
│       │                                                     │
│       └─── Logs ─────→ Local Loki ───→ Grafana UI           │
│                        (stays in workload)                  │
└─────────────────────────────────────────────────────────────┘
```

**Key Design:**
- **Metrics**: Dual-write to local Mimir (dashboards) and control room Mimir (alerting)
- **Logs**: Stay within workload boundary only

## Components

### Grafana Alloy

Configuration: `python-pulumi/src/ptd/pulumi_resources/grafana_alloy.py`

**Scrapes metrics from:**
- Kubernetes pods in `posit-team`, `posit-team-system`, and `loki` namespaces
- Node exporters, kube-state-metrics, kubelet cAdvisor
- Cloud provider metrics for managed services

**Collects logs from:**
- Kubernetes pods in `posit-team` and `posit-team-system` namespaces
- Optionally system logs via journald (`grafana_scrape_system_logs` setting)

### Mimir

Distributed deployment in `mimir` namespace. Uses object storage (S3/Azure Blob) backend.

**Endpoints:**
- Gateway: `http://mimir-gateway.mimir.svc.cluster.local/prometheus`
- Push API: `http://mimir-gateway.mimir.svc.cluster.local/api/v1/push`

**Architecture:**
```
Write: Alloy → Gateway → Distributor → Ingesters (ring) → S3
Read:  Grafana → Gateway → Query Frontend → Querier → Ingesters/Store Gateway
```

**Ring Health:** Mimir uses a hash ring to distribute data. If ingesters are marked UNHEALTHY but remain in the ring, queries fail. Auto-forget is configured to clean up stale members after 10 minutes.

**Troubleshooting Ring Issues:**
```bash
# View ring status
kubectl port-forward -n mimir svc/mimir-querier 8080:8080
# Visit http://localhost:8080/ingester/ring

# Check pod status
kubectl get pods -n mimir -l app.kubernetes.io/component=ingester
```

### Loki

Distributed deployment in `loki` namespace. Uses object storage backend.

**Endpoint:** `http://loki-gateway.loki.svc.cluster.local`

### Grafana

Single deployment in `grafana` namespace with Mimir and Loki as data sources.

**Access:** `https://grafana.<workload-domain>` (authenticated via Traefik forward auth)

## Container Troubleshooting Queries

### Memory (OOMKilled Investigation)

| Metric | Purpose |
|--------|---------|
| `container_memory_working_set_bytes` | Active memory (OOM killer evaluates this) |
| `container_spec_memory_limit_bytes` | Configured limit |
| `container_memory_failcnt` | OOM event counter |

```promql
# Memory usage as % of limit
(container_memory_working_set_bytes{namespace="posit-team"}
  / container_spec_memory_limit_bytes{namespace="posit-team"}) * 100

# Containers approaching limit (>90%)
(container_memory_working_set_bytes / container_spec_memory_limit_bytes) > 0.9

# OOMKilled containers
kube_pod_container_status_last_terminated_reason{reason="OOMKilled"}
```

### CPU Throttling

| Metric | Purpose |
|--------|---------|
| `container_cpu_usage_seconds_total` | Cumulative CPU time |
| `container_cpu_cfs_throttled_seconds_total` | Time spent throttled |
| `container_spec_cpu_quota` | CPU limit (microseconds per 100ms) |

```promql
# Throttle percentage
rate(container_cpu_cfs_throttled_seconds_total{namespace="posit-team"}[5m])
  / rate(container_cpu_cfs_periods_total{namespace="posit-team"}[5m]) * 100

# CPU usage (cores)
rate(container_cpu_usage_seconds_total{namespace="posit-team"}[5m])
```

> **Tip:** Throttling >25% indicates containers need higher CPU limits.

### Network

```promql
# Throughput
rate(container_network_receive_bytes_total{namespace="posit-team"}[5m])
rate(container_network_transmit_bytes_total{namespace="posit-team"}[5m])

# Errors (non-zero indicates issues)
rate(container_network_receive_errors_total{namespace="posit-team"}[5m])
rate(container_network_transmit_packets_dropped_total{namespace="posit-team"}[5m])
```

### Disk I/O

```promql
# Filesystem usage %
(container_fs_usage_bytes / container_fs_limit_bytes) * 100

# I/O throughput
rate(container_fs_reads_bytes_total{namespace="posit-team"}[5m])
rate(container_fs_writes_bytes_total{namespace="posit-team"}[5m])
```

> **Tip:** Filesystem usage >90% can cause pod evictions.

### Container Restarts

```promql
# Containers with restarts
kube_pod_container_status_restarts_total{namespace="posit-team"} > 0

# Termination reasons
kube_pod_container_status_last_terminated_reason{namespace="posit-team"}

# Recently restarted (< 1 hour uptime)
(time() - container_start_time_seconds{namespace="posit-team"}) < 3600
```

## Mimir Self-Monitoring

### The Chicken-and-Egg Problem

If a workload's Mimir breaks, alerts running on that workload can't query it. PTD solves this by running Mimir alerts on the **control room**, which queries its own Mimir instance that receives metrics via dual-write from all workloads.

### Alerts

Alerts defined in `python-pulumi/src/ptd/grafana_alerts/mimir.yaml` (deployed to control room Grafana):

| Alert | Catches |
|-------|---------|
| `mimir_ingester_pods_not_ready` | Pod crashes/restarts (earliest warning) |
| `mimir_remote_write_failures` | Alloy can't push metrics to Mimir |

Ring health issues are handled by auto-forget configuration (stale members removed after 10 minutes).

### Mimir Diagnostic Queries

```promql
# Ring health
cortex_ring_members{ring="ingester"}
cortex_ring_members{state="Unhealthy",ring="ingester"}

# Ingestion rate
sum(rate(cortex_distributor_received_samples_total[5m]))

# Query latency (p99)
histogram_quantile(0.99, sum(rate(cortex_request_duration_seconds_bucket{route=~".*query.*"}[5m])) by (le))

# Query error rate
sum(rate(cortex_request_duration_seconds_count{status_code=~"5.."}[5m]))
  / sum(rate(cortex_request_duration_seconds_count[5m]))
```

## Related Documentation

- [Grafana Alloy](https://grafana.com/docs/alloy/latest/)
- [Mimir](https://grafana.com/docs/mimir/latest/)
- [Loki](https://grafana.com/docs/loki/latest/)
- [Grafana](https://grafana.com/docs/grafana/latest/)
