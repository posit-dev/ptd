# Kubernetes Configuration Guide

This guide explains the key Kubernetes features and configurations in PTD (Posit Team Dedicated).

## Table of Contents

- [Workbench Session Scheduling](#workbench-session-scheduling)
- [Karpenter Configurations](#karpenter-configurations)
- [Additional Features](#additional-features)

---

## Workbench Session Scheduling

### Overview

Workbench session scheduling allows you to control where user Workbench sessions run within your Kubernetes cluster. This is especially important for isolating expensive resources like GPUs or high-memory nodes.

### Session Taints: Isolating Session Workloads

The `session_taints` setting is a configuration option that dedicates specific node pools exclusively to user sessions, preventing other workloads (like system pods or infrastructure services) from consuming those resources and allowing Karpenter to scale to 0 when not in use.

**Configuration in `ptd.yaml`:**
```yaml
clusters:
  '20250415':
    spec:
      karpenter_config:
        node_pools:
          - name: default-karpenter-node-pool
            session_taints: true  # Enable session isolation
            expireAfter: 720h
            requirements:
              - key: "kubernetes.io/arch"
                operator: In
                values: ["amd64"]
```

#### What Happens Downstream

When `session_taints: true` is configured, the following happens automatically:

**1. Taints Are Added to Nodes**

The Karpenter NodePool gets a taint applied to all its nodes:
```yaml
taints:
  - key: workload-type
    value: session
    effect: NoSchedule
```

> **Kubernetes Concept**: A *taint* is like a "Do Not Enter" sign on a node. Pods cannot be scheduled on tainted nodes unless they have a matching *toleration*.

**2. Tolerations Are Injected into Session Pods**

All Workbench session pods automatically receive a toleration:
```yaml
tolerations:
  - key: workload-type
    operator: Equal
    value: session
    effect: NoSchedule
```

> **Kubernetes Concept**: A *toleration* is like a special password that allows a pod to ignore a taint and be scheduled on that node.

**3. Workbench Server vs. Sessions**

This is a critical distinction:
- **Workbench Application** (the main application pods): Do NOT get session tolerations, so they run on regular nodes
- **User Sessions** (RStudio sessions, Jupyter notebooks): DO get session tolerations, so they can run on tainted nodes

This separation ensures that the lightweight Workbench app doesn't consume expensive session node resources.

#### The Complete Flow

```
User Configuration (ptd.yaml) session_taints: true
    ↓
Python Pulumi Processing
    ├─→ Adds taint to Karpenter NodePool
    └─→ Injects tolerations into Site CRD
    ↓
Karpenter Provisions Tainted Nodes
    ↓
Team Operator Deploys Site
    ├─→ Workbench server: NO session tolerations (runs on regular nodes)
    └─→ Session pods: YES session tolerations (can run on tainted nodes)
    ↓
Sessions Scheduled on Dedicated Nodes
```

### Resource Profiles and Placement Constraints

Resource profiles define the compute resources (CPU, memory, GPU) available for Workbench user sessions and can target specific node pools using placement constraints.

#### AWS/Karpenter Example

**Configuration in `site.yaml` (AWS with Karpenter):**
```yaml
experimentalFeatures:
  resourceProfiles:
    "c1.m8":
      name: "small c1.m8"
      cpus: "1"
      mem-mb: "8000"
      placement-constraints: "karpenter.sh/nodepool:default-karpenter-node-pool"

    "gpu-g5":
      name: "GPU g5e"
      cpus: "8"
      mem-mb: "32000"
      nvidia-gpus: "1"
      placement-constraints: "karpenter.sh/nodepool:gpu-g5e-karpenter-node-pool"
```

#### Azure/AKS Example

**Configuration in `site.yaml` (Azure with AKS):**
```yaml
experimentalFeatures:
  resourceProfiles:
    "medium":
      name: "Medium"
      cpus: "2"
      mem-mb: "4000"
      placement-constraints: "kubernetes.azure.com/agentpool:default"

    "gpu-nc6":
      name: "GPU NC6s_v3"
      cpus: "8"
      mem-mb: "32000"
      nvidia-gpus: "1"
      placement-constraints: "kubernetes.azure.com/agentpool:gpupool"
```

**Key Differences:**
- **AWS/Karpenter**: Uses `karpenter.sh/nodepool` label (added by Karpenter)
- **Azure/AKS**: Uses `kubernetes.azure.com/agentpool` label (automatically added by AKS)
- Both formats use `key:value` or `key=value` syntax

**How Sessions are Scheduled:**

1. **User starts a session** and selects a resource profile (e.g., "gpu-g5")
2. **Session pod is created** with:
   - Resource requests (8 CPUs, 32GB memory, 1 GPU)
   - Node affinity targeting `gpu-g5e-karpenter-node-pool`
   - Session tolerations (if pool has `session_taints: true`)
3. **Kubernetes scheduler** finds nodes matching:
   - The node pool label (`karpenter.sh/nodepool=gpu-g5e-karpenter-node-pool`)
   - Available resources (8 CPUs, 32GB RAM, 1 GPU)
   - Tolerable taints (if any)
4. **Karpenter provisions** new nodes if no suitable nodes exist
5. **Session runs** on the appropriate node

### Consolidation Protection

Active sessions are automatically protected from Karpenter's cost-optimization evictions using the `karpenter.sh/do-not-disrupt: "true"` annotation. This prevents sessions from being terminated when Karpenter tries to consolidate underutilized nodes.

---

## Karpenter

[Karpenter](https://karpenter.sh/) is an open-source Kubernetes cluster autoscaler that provisions right-sized compute resources in response to workload demands. PTD uses Karpenter to automatically scale EKS clusters based on pod scheduling needs.

### Node Pools

A Karpenter NodePool defines a class of nodes with specific characteristics (instance types, capacity limits, taints, etc.). You can have multiple node pools for different workload types.

**Basic NodePool Configuration:**
```yaml
karpenter_config:
  node_pools:
    - name: default-karpenter-node-pool
      expireAfter: 720h  # Nodes expire after 30 days
      weight: 100        # Higher weight = higher scheduling priority

      requirements:
        - key: "kubernetes.io/arch"
          operator: In
          values: ["amd64"]
        - key: "karpenter.k8s.aws/instance-category"
          operator: In
          values: ["t", "m", "r"]  # General-purpose instances

      limits:
        cpu: "16"       # Max 16 CPUs across all nodes in this pool
        memory: "64Gi"  # Max 64GB memory across all nodes

      session_taints: true  # See "Workbench Session Scheduling" section
```

**NodePool Components:**

- **Requirements**: Define what types of EC2 instances Karpenter can provision (architecture, instance families, zones, etc.)
- **Limits**: Cap the total resources Karpenter can provision for this pool
- **Weight**: Priority for scheduling (default pool = 100, GPU pool = 10 typically)
- **expireAfter**: How long nodes live before automatic replacement (reduces drift, applies security updates)

**Example Multi-Pool Setup:**
```yaml
karpenter_config:
  node_pools:
    # General purpose for regular sessions
    - name: default-karpenter-node-pool
      weight: 100
      session_taints: true
      requirements:
        - key: "karpenter.k8s.aws/instance-category"
          operator: In
          values: ["m", "r"]
      limits:
        cpu: "256"
        memory: "512Gi"

    # GPU nodes for ML/data science sessions
    - name: gpu-g5e-karpenter-node-pool
      weight: 10  # Lower priority
      session_taints: true
      requirements:
        - key: "karpenter.k8s.aws/instance-family"
          operator: In
          values: ["g5"]
        - key: "node.kubernetes.io/instance-type"
          operator: In
          values: ["g5.xlarge", "g5.2xlarge"]
      limits:
        nvidia.com/gpu: "8"
```

### Overprovisioning

Overprovisioning keeps spare capacity in your cluster for faster session startup times by maintaining "placeholder" pods that can be evicted when real workloads arrive.

#### How Overprovisioning Works

1. **Placeholder pods** are deployed with low priority (`-100`)
2. **Karpenter sees resource requests** and provisions nodes to satisfy them
3. **Real session pod** needs resources
4. **Kubernetes evicts** placeholder pods (because they have low priority)
5. **Real session schedules** immediately without waiting for node provisioning

#### Configuration

Overprovisioning is configured **per NodePool**:

```yaml
karpenter_config:
  node_pools:
    - name: default-karpenter-node-pool
      overprovisioning_replicas: 2  # Keep 2 placeholder pods
      overprovisioning_cpu_request: "2"
      overprovisioning_memory_request: "8Gi"
      session_taints: true
```

This configuration maintains 2 placeholder pods, each requesting 2 CPUs and 8GB of memory. Karpenter will provision nodes to accommodate these pods, ensuring spare capacity.

**For GPU Pools:**
```yaml
- name: gpu-g5e-karpenter-node-pool
  overprovisioning_replicas: 1
  overprovisioning_cpu_request: "4"
  overprovisioning_memory_request: "16Gi"
  overprovisioning_nvidia_gpu_request: "1"  # Reserve 1 GPU
```

#### Implementation Details

- Each NodePool gets its own overprovisioning deployment (not a shared pool)
- Placeholder pods have node affinity targeting their specific NodePool
- Placeholder pods include tolerations matching the NodePool's taints (critical for `session_taints: true`)
- Pod anti-affinity spreads placeholders across multiple nodes

#### When to Use Overprovisioning

**Good use cases:**
- High-traffic environments with frequent session starts
- GPU pools where provisioning time is expensive (GPUs take longer to start)
- Workloads where startup latency matters

**When to skip it:**
- Low-usage environments (wastes money)
- Batch/async workloads that can tolerate startup delays
- Very large instance types where even one placeholder is costly

### Consolidation and Disruption

Karpenter actively optimizes costs by consolidating workloads onto fewer nodes when possible.

**Default Settings:**
- **Policy**: `WhenEmptyOrUnderutilized`
- **Consolidation Delay**: 5 minutes after a node becomes empty
- **Disruption Budget**: 10% of nodes can be disrupted at once

**Session Protection:**
Active Workbench sessions receive the `karpenter.sh/do-not-disrupt: "true"` annotation, which prevents Karpenter from evicting them during consolidation. This ensures users don't experience unexpected interruptions.

---

## Azure AKS Configuration

Azure Kubernetes Service (AKS) uses a different architecture than AWS with Karpenter. Instead of dynamic node pool creation, AKS uses pre-defined agent pools with autoscaling capabilities.

### User Node Pools

Azure PTD deployments configure `user_node_pools` separately from the system node pool. User node pools are dedicated to running workloads (sessions, applications) while the system pool runs cluster infrastructure.

**Configuration in `ptd.yaml`:**
```yaml
clusters:
  '20250627':
    user_node_pools:
      - name: default
        vm_size: Standard_D8s_v6  # 8 cores, 32 GB RAM
        min_count: 2
        max_count: 6
        enable_auto_scaling: true
        root_disk_size: 128

      - name: gpupool
        vm_size: Standard_NC6s_v3  # GPU instance
        min_count: 0
        max_count: 4
        enable_auto_scaling: true
        node_labels:  # Optional custom labels
          gpu-vendor: nvidia
          workload-type: ml
```

### Automatic AKS Labels

AKS automatically adds several labels to nodes that can be used for placement constraints:

**Key Automatic Labels:**
- `kubernetes.azure.com/agentpool` - Agent pool name (e.g., "default", "gpupool")
- `node.kubernetes.io/instance-type` - VM size (e.g., "Standard_NC6s_v3")
- `kubernetes.azure.com/accelerator` - Accelerator type (e.g., "nvidia")
- `topology.kubernetes.io/region` - Azure region
- `topology.kubernetes.io/zone` - Availability zone

### Key Differences from AWS/Karpenter

| Feature | AWS with Karpenter | Azure with AKS |
|---------|-------------------|----------------|
| **Node Pools** | Dynamic, created on-demand | Pre-defined, scaled within limits |
| **Scale to Zero** | Yes, with session_taints | No, system pool always runs |
| **Taints for Isolation** | Required (session_taints) | Not needed (separate agent pools) |
| **Label for Placement** | `karpenter.sh/nodepool` | `kubernetes.azure.com/agentpool` |
| **Overprovisioning** | Per-pool configuration | Not applicable |
| **Node Expiration** | Configurable per pool | Managed by AKS |

### Azure Session Scheduling

For Azure, you don't need session taints because:
1. **System node pool** is separate and runs cluster infrastructure
2. **User node pools** are dedicated to workloads
3. **Autoscaling** handles capacity within configured min/max bounds
4. **Placement constraints** direct sessions to specific agent pools

**Example Azure Site Configuration:**
```yaml
workbench:
  experimentalFeatures:
    resourceProfiles:
      "default":
        name: "Default"
        cpus: "2"
        mem-mb: "8000"
        placement-constraints: "kubernetes.azure.com/agentpool:default"

      "gpu-workload":
        name: "GPU Workload"
        cpus: "8"
        mem-mb: "32000"
        nvidia-gpus: "1"
        placement-constraints: "kubernetes.azure.com/agentpool:gpupool"
```

When a user starts a GPU session, Workbench interprets the placement constraint and adds a node selector for `kubernetes.azure.com/agentpool=gpupool`, ensuring the session pod runs on the GPU agent pool. If the pool is at minimum capacity, AKS autoscaler provisions additional nodes.

---

## Additional Features

### Node Expiration

Nodes are automatically replaced after a configured lifetime (default: 720h/30 days) using the `expireAfter` setting. This ensures:
- Security updates are applied
- Configuration drift is minimized
- Nodes don't run indefinitely with outdated AMIs

**Configuration:**
```yaml
karpenter_config:
  node_pools:
    - name: default-karpenter-node-pool
      expireAfter: 720h  # 30 days
```

### IAM and Security

PTD automatically configures:
- **KarpenterNodeRole**: IAM role for EC2 instances (with EKS node permissions)
- **KarpenterControllerRole**: IRSA role for the Karpenter controller
- **EC2NodeClass**: AMI selection, block devices, security groups
- **Instance Metadata**: IMDSv2 required for enhanced security

### Controller Placement

The Karpenter controller itself is scheduled on **EKS managed nodes** (not Karpenter-managed nodes) to avoid circular dependency issues. If Karpenter manages its own node, it could evict itself and cause cluster instability.

### GPU Node Considerations

GPU nodes require special handling:

1. **NVIDIA Device Plugin**: DaemonSet must tolerate GPU node taints
2. **Session Scheduling**: Sessions must request GPU resources AND have appropriate tolerations
3. **Higher Weight**: GPU pools typically have lower weight (10 vs 100) to prefer cheaper nodes
4. **Overprovisioning**: GPU overprovisioning is expensive but reduces wait times significantly

**Example GPU Pool:**
```yaml
- name: gpu-g5e-karpenter-node-pool
  weight: 10
  session_taints: true
  overprovisioning_replicas: 1
  overprovisioning_nvidia_gpu_request: "1"
  requirements:
    - key: "karpenter.k8s.aws/instance-family"
      operator: In
      values: ["g5"]
  limits:
    nvidia.com/gpu: "8"
```

---

## Key Concepts for Non-Kubernetes Engineers

### Taints and Tolerations

Think of taints and tolerations like a bouncer at a nightclub:
- **Taint**: "You need a VIP pass to get in here"
- **Toleration**: "I have a VIP pass, let me through"
- **Regular pod**: "I don't have a pass, I'll go elsewhere"

### Node Affinity

Node affinity is like specifying "I want to be seated in the patio section" at a restaurant. It's a preference or requirement for where a pod should run.

### Priority and Preemption

Priority determines who gets resources when there's contention:
- **High priority** (100): Real user sessions
- **Low priority** (-100): Overprovisioning placeholder pods
- When resources are tight, low-priority pods are evicted to make room

## References

- **Karpenter Documentation**: [karpenter.sh](https://karpenter.sh/)
