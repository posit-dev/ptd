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
  - **kubelet cAdvisor** for container-level resource usage metrics
  - Blackbox exporter for health checks
  - Cloud provider metrics for managed storage and database services
- Collects logs from:
  - Kubernetes pods in `posit-team` and `posit-team-system` namespaces
  - Optionally system logs via journald (controlled by `grafana_scrape_system_logs`)
- Runs with clustering enabled for high availability

**Container Metrics (via cAdvisor)**: The following container-level metrics are collected for debugging resource issues:

#### Memory Metrics
- `container_memory_working_set_bytes` - Active memory usage (what the OOM killer evaluates against limits)
- `container_memory_usage_bytes` - Total memory usage including cache
- `container_memory_rss` - Resident Set Size (anonymous memory: heap, stack)
- `container_memory_cache` - Page cache memory (can be reclaimed)
- `container_memory_swap` - Swap space usage
- `container_memory_failcnt` - Number of times memory allocation failed (OOM events)
- `container_spec_memory_limit_bytes` - Configured memory limit
- `container_spec_memory_reservation_limit_bytes` - Configured memory request

#### CPU Metrics
- `container_cpu_usage_seconds_total` - Cumulative CPU time consumed
- `container_cpu_cfs_throttled_seconds_total` - Total time container was throttled due to CPU limits
- `container_cpu_cfs_throttled_periods_total` - Number of throttled periods
- `container_cpu_cfs_periods_total` - Total number of CPU CFS scheduler periods
- `container_spec_cpu_quota` - CPU limit in microseconds per 100ms period (-1 if unlimited)
- `container_spec_cpu_shares` - CPU request weight (relative to other containers)

#### Network Metrics
- `container_network_receive_bytes_total` - Bytes received
- `container_network_transmit_bytes_total` - Bytes transmitted
- `container_network_receive_packets_total` - Packets received
- `container_network_transmit_packets_total` - Packets transmitted
- `container_network_receive_errors_total` - Errors receiving packets
- `container_network_transmit_errors_total` - Errors transmitting packets
- `container_network_receive_packets_dropped_total` - Inbound packets dropped
- `container_network_transmit_packets_dropped_total` - Outbound packets dropped

#### Filesystem Metrics
- `container_fs_usage_bytes` - Current filesystem usage
- `container_fs_limit_bytes` - Filesystem capacity
- `container_fs_reads_bytes_total` - Bytes read from filesystem
- `container_fs_writes_bytes_total` - Bytes written to filesystem
- `container_fs_reads_total` - Number of read operations
- `container_fs_writes_total` - Number of write operations

#### Container Lifecycle Metrics
- `container_start_time_seconds` - Unix timestamp when container started
- `kube_pod_container_status_restarts_total` - Number of container restarts (from kube-state-metrics)
- `kube_pod_container_status_last_terminated_reason` - Reason for last termination (from kube-state-metrics)

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

## Container Troubleshooting with Metrics

This section provides practical Grafana queries for diagnosing common container issues.

### Memory Issues and OOMKilled Pods

When pods are terminated due to OOM (Out of Memory), use these queries to investigate:

#### Identify OOMKilled Pods
```promql
# See which containers were OOMKilled
kube_pod_container_status_last_terminated_reason{reason="OOMKilled"}

# Count OOM events by pod over time
sum by (pod, namespace) (container_memory_failcnt{namespace="posit-team"})
```

#### Memory Usage Analysis
```promql
# Working set memory (what OOM killer evaluates) by container
container_memory_working_set_bytes{namespace="posit-team"}

# Memory usage as percentage of limit
(container_memory_working_set_bytes{namespace="posit-team"}
  / container_spec_memory_limit_bytes{namespace="posit-team"}) * 100

# Memory breakdown: RSS vs cache
container_memory_rss{namespace="posit-team"}
container_memory_cache{namespace="posit-team"}

# Containers approaching memory limit (>90%)
(container_memory_working_set_bytes{namespace="posit-team"}
  / container_spec_memory_limit_bytes{namespace="posit-team"}) > 0.9
```

#### Historical Memory Trends
```promql
# Memory usage over time for a specific pod
container_memory_working_set_bytes{pod="<pod-name>", namespace="posit-team"}

# Memory growth rate (bytes per second)
rate(container_memory_working_set_bytes{namespace="posit-team"}[5m])

# Peak memory usage in last hour
max_over_time(container_memory_working_set_bytes{namespace="posit-team"}[1h])
```

**Key Investigation Points:**
- `container_memory_working_set_bytes` exceeding `container_spec_memory_limit_bytes` triggers OOM
- High `container_memory_rss` indicates application memory pressure (heap, stack)
- High `container_memory_cache` can usually be reclaimed and is not the root cause
- Check if `container_memory_failcnt` is incrementing (indicates memory allocation failures)

### CPU Throttling and Performance

CPU throttling occurs when containers hit their CPU limits, causing performance degradation.

#### Detect CPU Throttling
```promql
# Percentage of time container was throttled
rate(container_cpu_cfs_throttled_seconds_total{namespace="posit-team"}[5m])
  / rate(container_cpu_cfs_periods_total{namespace="posit-team"}[5m]) * 100

# Containers being throttled more than 10% of the time
(rate(container_cpu_cfs_throttled_periods_total{namespace="posit-team"}[5m])
  / rate(container_cpu_cfs_periods_total{namespace="posit-team"}[5m])) > 0.1
```

#### CPU Usage Analysis
```promql
# CPU usage rate (cores) per container
rate(container_cpu_usage_seconds_total{namespace="posit-team"}[5m])

# CPU usage as percentage of limit (quota/100000 = cores)
rate(container_cpu_usage_seconds_total{namespace="posit-team"}[5m])
  / (container_spec_cpu_quota{namespace="posit-team"} / 100000) * 100

# Total throttled time per container
rate(container_cpu_cfs_throttled_seconds_total{namespace="posit-team"}[5m])
```

#### CPU Requests vs Usage
```promql
# CPU shares (requests) vs actual usage
container_spec_cpu_shares{namespace="posit-team"}
rate(container_cpu_usage_seconds_total{namespace="posit-team"}[5m])
```

**Key Investigation Points:**
- Throttling >25% indicates containers need higher CPU limits
- CPU usage consistently at limit suggests CPU-bound workload
- Compare throttling patterns across similar pods to identify outliers
- Check if `container_spec_cpu_quota` is set too low for the workload

### Network Issues

Diagnose network connectivity, throughput, and error issues.

#### Network Throughput
```promql
# Receive throughput (bytes/second)
rate(container_network_receive_bytes_total{namespace="posit-team"}[5m])

# Transmit throughput (bytes/second)
rate(container_network_transmit_bytes_total{namespace="posit-team"}[5m])

# Total network throughput per pod
sum by (pod) (
  rate(container_network_receive_bytes_total{namespace="posit-team"}[5m]) +
  rate(container_network_transmit_bytes_total{namespace="posit-team"}[5m])
)
```

#### Network Errors and Drops
```promql
# Packet errors
rate(container_network_receive_errors_total{namespace="posit-team"}[5m])
rate(container_network_transmit_errors_total{namespace="posit-team"}[5m])

# Dropped packets (indicates network congestion or buffer overflow)
rate(container_network_receive_packets_dropped_total{namespace="posit-team"}[5m])
rate(container_network_transmit_packets_dropped_total{namespace="posit-team"}[5m])

# Containers with any packet drops
(rate(container_network_receive_packets_dropped_total{namespace="posit-team"}[5m]) +
 rate(container_network_transmit_packets_dropped_total{namespace="posit-team"}[5m])) > 0
```

#### Network Packet Rate
```promql
# Packets per second
rate(container_network_receive_packets_total{namespace="posit-team"}[5m])
rate(container_network_transmit_packets_total{namespace="posit-team"}[5m])
```

**Key Investigation Points:**
- Non-zero error rates indicate network interface or driver issues
- Dropped packets suggest network congestion or insufficient buffer space
- Compare throughput against expected workload to identify bottlenecks
- Sudden changes in packet rates may indicate connectivity problems

### Disk I/O Issues

Diagnose filesystem usage and I/O performance problems.

#### Filesystem Usage
```promql
# Filesystem usage by container
container_fs_usage_bytes{namespace="posit-team"}

# Filesystem usage as percentage of capacity
(container_fs_usage_bytes{namespace="posit-team"}
  / container_fs_limit_bytes{namespace="posit-team"}) * 100

# Containers with >80% disk usage
(container_fs_usage_bytes{namespace="posit-team"}
  / container_fs_limit_bytes{namespace="posit-team"}) > 0.8
```

#### Disk I/O Throughput
```promql
# Read throughput (bytes/second)
rate(container_fs_reads_bytes_total{namespace="posit-team"}[5m])

# Write throughput (bytes/second)
rate(container_fs_writes_bytes_total{namespace="posit-team"}[5m])

# Total I/O throughput
sum by (pod) (
  rate(container_fs_reads_bytes_total{namespace="posit-team"}[5m]) +
  rate(container_fs_writes_bytes_total{namespace="posit-team"}[5m])
)
```

#### Disk I/O Operations
```promql
# Read IOPS (operations per second)
rate(container_fs_reads_total{namespace="posit-team"}[5m])

# Write IOPS
rate(container_fs_writes_total{namespace="posit-team"}[5m])

# Top containers by IOPS
topk(10,
  rate(container_fs_reads_total{namespace="posit-team"}[5m]) +
  rate(container_fs_writes_total{namespace="posit-team"}[5m])
)
```

**Key Investigation Points:**
- Filesystem usage >90% can cause application errors and pod evictions
- High IOPS with low throughput suggests small file operations
- Sudden spikes in write operations may indicate logging or caching issues
- Compare I/O patterns against storage backend limits (EBS, Azure Disk)

### Container Restart and Lifecycle Issues

Track container restarts, crashes, and lifecycle problems.

#### Container Restart Patterns
```promql
# Containers with recent restarts
kube_pod_container_status_restarts_total{namespace="posit-team"} > 0

# Restart rate (restarts per minute)
rate(kube_pod_container_status_restarts_total{namespace="posit-team"}[5m]) * 60

# Top restarting containers
topk(10, kube_pod_container_status_restarts_total{namespace="posit-team"})
```

#### Termination Reasons
```promql
# See why containers terminated
kube_pod_container_status_last_terminated_reason{namespace="posit-team"}

# Count terminations by reason
count by (reason) (kube_pod_container_status_last_terminated_reason{namespace="posit-team"})

# OOMKilled containers specifically
kube_pod_container_status_last_terminated_reason{reason="OOMKilled", namespace="posit-team"}
```

#### Container Age and Uptime
```promql
# Container uptime (seconds)
time() - container_start_time_seconds{namespace="posit-team"}

# Containers younger than 1 hour (recently restarted)
(time() - container_start_time_seconds{namespace="posit-team"}) < 3600

# Average container age by pod
avg by (pod) (time() - container_start_time_seconds{namespace="posit-team"})
```

**Key Investigation Points:**
- Restart rate >0 indicates instability (crashes, OOM, failed health checks)
- Check `kube_pod_container_status_last_terminated_reason` to understand why
- Frequent restarts with "Error" reason suggest application bugs
- OOMKilled restarts indicate insufficient memory limits
- Short uptime combined with high restart count suggests crash loops

## Configured Alerts

PTD deploys a set of Grafana alerts to the control room for centralized monitoring of all workload clusters. Alert definitions are stored in `python-pulumi/src/ptd/grafana_alerts/`.

All alerts are configured to send notifications to OpsGenie when triggered.

### Application Alerts

| Alert | Threshold | Duration | Description |
|-------|-----------|----------|-------------|
| **Loki WAL Disk Full Failures** | > 0 failures | 5m | Loki ingester has experienced WAL disk full failures, indicating storage issues with the Loki WAL directory |

### CloudWatch Alerts (AWS)

| Alert | Threshold | Duration | Description |
|-------|-----------|----------|-------------|
| **FSx Capacity** | > 80% used | 5m | FSx storage instance has less than 20% capacity remaining |
| **EC2 Network Out High** | > 300 MiB/s | 5m | EC2 instance has sustained high network outbound traffic |
| **EC2 Network Packets Out High** | > 400,000 packets/s | 5m | EC2 instance has unusually high packet transmission rate |

### Health Check Alerts

| Alert | Threshold | Duration | Description |
|-------|-----------|----------|-------------|
| **Healthchecks** | HTTP status != 200 | 5m | Health check for a PTD site component returned non-200 response |

### Mimir Alerts

| Alert | Threshold | Duration | Description |
|-------|-----------|----------|-------------|
| **Workload Metrics Silent** | No metrics received | 10m | No metrics received from workload cluster; may indicate Alloy not running, network issues, or cluster down |

### Node Alerts

| Alert | Threshold | Duration | Description |
|-------|-----------|----------|-------------|
| **Node Not Ready** | Ready condition = false | 15m | Kubernetes node has been in unready state |
| **Node Memory Pressure** | MemoryPressure = true | 15m | Node is experiencing memory pressure |
| **Node Disk Pressure** | DiskPressure = true | 15m | Node is experiencing disk pressure |

### Pod Alerts

| Alert | Threshold | Duration | Description |
|-------|-----------|----------|-------------|
| **CrashLoopBackOff** | Any container in CrashLoopBackOff | 5m | Container is repeatedly crashing and restarting |
| **Pod Error** | Container terminated (reason != Completed) | 5m | Pod container terminated with an error (excludes user session pods) |
| **Pod Not Healthy** | Phase = Pending/Unknown/Failed | 15m | Pod has been in non-running state (excludes user session pods) |
| **Pod Restarts** | > 5 restarts in 15m | 15m | Pod has restarted excessively |
| **Deployment Replicas Mismatch** | Desired != Available | 15m | Deployment does not have the expected number of available replicas |
| **StatefulSet Replicas Mismatch** | Ready != Desired | 15m | StatefulSet does not have the expected number of ready replicas |

### Adding or Modifying Alerts

To add or modify alerts, edit the YAML files in `python-pulumi/src/ptd/grafana_alerts/`. Each file contains alerts grouped by category:

- `applications.yaml` - Application-specific alerts (Loki, etc.)
- `cloudwatch.yaml` - AWS CloudWatch metric alerts
- `healthchecks.yaml` - HTTP health check alerts
- `mimir.yaml` - Metrics pipeline alerts
- `nodes.yaml` - Kubernetes node alerts
- `pods.yaml` - Kubernetes pod and workload alerts

To delete an alert, follow the instructions in the file header comments regarding the `deleteRules` syntax.

## Related Documentation

- [Grafana Alloy Documentation](https://grafana.com/docs/alloy/latest/)
- [Mimir Documentation](https://grafana.com/docs/mimir/latest/)
- [Loki Documentation](https://grafana.com/docs/loki/latest/)
- [Grafana Documentation](https://grafana.com/docs/grafana/latest/)
