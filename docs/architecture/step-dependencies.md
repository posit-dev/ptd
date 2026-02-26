# Step dependencies and execution pipeline

This document explains the step execution pipeline for PTD deployments, including what each step does, why steps depend on previous steps, and how to safely use `--only-steps` and `--start-at-step`.

## Overview

PTD organizes infrastructure deployment into sequential steps. Each step depends on resources that previous steps create. The Go CLI orchestrates step execution, while most steps use Python Pulumi to create cloud resources.

**Location:** `lib/steps/steps.go`

## Workload steps (full pipeline)

Workloads use this 8-step pipeline:

```
bootstrap → persistent → postgres_config → eks/aks → clusters → helm → sites → persistent_reprise
```

### Step 1: bootstrap (Go) {#bootstrap}
**Implementation:** `lib/steps/bootstrap.go`
**Language:** Go
**Proxy Required:** No

**Creates:**
- S3 state bucket for Pulumi backend
- Key Management Service (KMS) key for state encryption
- Admin Identity and Access Management (IAM) policy for Pulumi operations
- AWS Secrets Manager secret (empty, populated by later steps)

**Why first:** Everything else needs a place to store Pulumi state and credentials to operate. This step creates the foundational infrastructure for the workload account.

**Safe to re-run:** Yes, idempotent.

---

### Step 2: persistent (Python) {#persistent-workload}
**Implementation:**
- AWS: `python-pulumi/src/ptd/pulumi_resources/aws_workload_persistent.py`
- Azure: `python-pulumi/src/ptd/pulumi_resources/azure_workload_persistent.py`

**Language:** Python/Pulumi
**Proxy Required:** No

**Creates:**
- **AWS:** Virtual Private Cloud (VPC), subnets, NAT gateways, route tables, Relational Database Service (RDS) PostgreSQL, S3 buckets (Loki logs, Mimir metrics, general storage), IAM roles and policies, AWS Certificate Manager (ACM) certificates, FSx for OpenZFS or Elastic File System (EFS), bastion host (optional)
- **Azure:** VNet, subnets, Azure Database for PostgreSQL, Storage Accounts, Azure Container Registry (ACR), managed identities, Azure Key Vault certificates, Azure NetApp Files, bastion host (optional)

**Post-stack action:** Updates AWS Secrets Manager (AWS) or Azure Key Vault (Azure) with stack outputs (database endpoint, VPC/VNet ID, etc.) for later steps to use.

**Depends on:**
- `bootstrap`: Needs state bucket and KMS key

**Why second:** Persistent infrastructure (network, database, storage) must exist before we can deploy compute resources or applications.

**Safe to re-run:** Yes, but may require manual state fixes if VPC/RDS changes are detected.

---

### Step 3: postgres_config (Python) {#postgres-config}
**Implementation:** `python-pulumi/src/ptd/pulumi_resources/aws_workload_postgres_config.py`
**Language:** Python/Pulumi
**Proxy Required:** Yes (connects to private RDS)

**Creates:**
- Database users (Grafana, Loki, Keycloak, etc.)
- Database permissions and grants
- PostgreSQL extensions (e.g., `pg_trgm`, `uuid-ossp`)

**Depends on:**
- `persistent`: Needs RDS endpoint and credentials from Secrets Manager

**Why third:** Database configuration must happen before deploying applications that need database access.

**Proxy rationale:** RDS is in a private subnet. The step uses the Systems Manager (SSM) proxy (via bastion host) to connect through the private network.

**Safe to re-run:** Yes, idempotent. Terraform-style state creates users and permissions only once.

---

### Step 4: eks (AWS) or aks (Azure) (cloud-specific) {#eks-aks}
**Implementation:**
- AWS: `python-pulumi/src/ptd/pulumi_resources/aws_workload_eks.py` (Python)
- Azure: `lib/steps/aks.go` (Go)

**Language:** Python (AWS), **Go (Azure)** - This is a key difference from most other steps
**Proxy Required:** No

**Creates:**
- Kubernetes cluster (Elastic Kubernetes Service (EKS) or Azure Kubernetes Service (AKS))
- Node groups or node pools
- OpenID Connect (OIDC) provider for workload identity
- Security groups (AWS) or network security groups (Azure)
- Cluster addons (EBS Container Storage Interface (CSI) driver for AWS, secrets store CSI for both)
- Karpenter resources (if autoscaling enabled, AWS only)

**Depends on:**
- `persistent`: Needs VPC/VNet, subnets, IAM roles (AWS) or managed identities (Azure)

**Why fourth:** The Kubernetes cluster is the foundation for all application workloads.

**Cloud selector:** This step uses the `Selector` pattern in `steps.go`:
```go
Selector("kubernetes", map[types.CloudProvider]Step{
    types.AWS:   &EKSStep{},
    types.Azure: &AKSStep{},
}),
```

**Implementation note:** AKS is implemented in **Go** (`lib/steps/aks.go`) unlike EKS which uses Python. This is because AKS cluster creation logic is better handled in Go for this implementation.

**Safe to re-run:** Yes, but cluster upgrades may cause downtime.

---

### Step 5: clusters (Python) {#clusters}
**Implementation:**
- AWS: `python-pulumi/src/ptd/pulumi_resources/aws_workload_clusters.py`
- Azure: `python-pulumi/src/ptd/pulumi_resources/azure_workload_clusters.py`

**Language:** Python/Pulumi
**Proxy Required:** Yes (creates Kubernetes resources)

**Creates:**
- Kubernetes namespaces (`posit-team`, `loki`, `grafana`, `mimir`, etc.)
- Network policies for namespace isolation
- Resource quotas (optional)

**Depends on:**
- `eks/aks`: Needs functioning Kubernetes cluster and kubeconfig

**Why fifth:** Namespaces must exist before Helm charts can deploy into them.

**Proxy rationale:** EKS/AKS API endpoints are private. The proxy (via bastion/Tailscale for AWS, bastion for Azure) provides access to the Kubernetes API.

**Safe to re-run:** Yes, idempotent.

---

### Step 6: helm (Python) {#helm}
**Implementation:**
- AWS: `python-pulumi/src/ptd/pulumi_resources/aws_workload_helm.py`
- Azure: `python-pulumi/src/ptd/pulumi_resources/azure_workload_helm.py`

**Language:** Python/Pulumi
**Proxy Required:** Yes (deploys Helm charts via Kubernetes API)

**Creates:**
- **Team Operator:** Manages Posit Team products (Posit Workbench, Posit Connect, Posit Package Manager)
- **Traefik:** Ingress controller and load balancer
- **cert-manager:** Automatic TLS certificate management
- **Loki:** Log aggregation
- **Grafana:** Observability dashboards
- **Mimir:** Metrics storage
- **kube-state-metrics:** Cluster metrics exporter
- **Grafana Alloy:** Telemetry collector
- **AWS Load Balancer Controller** (AWS only): Integrates Elastic Load Balancing (ELB) with Kubernetes services
- **Secrets Store CSI Driver:** Mounts AWS Secrets Manager (AWS) or Azure Key Vault (Azure) into pods
- **Karpenter** (AWS only): Autoscaling (if enabled)
- **NVIDIA Device Plugin:** GPU support (if enabled)
- **Azure Files CSI Driver** (Azure only): Persistent volume support

**Depends on:**
- `clusters`: Needs namespaces
- `persistent`: Needs certificates, IAM roles/managed identities, S3 buckets (AWS) or Storage Accounts (Azure) for Loki/Mimir

**Why sixth:** Helm charts deploy the platform components that support Posit Team applications.

**Proxy rationale:** Same as `clusters` - needs private Kubernetes API access.

**Safe to re-run:** Yes, but may cause temporary disruption to running services during chart upgrades.

---

### Step 7: sites (Python) {#sites}
**Implementation:**
- AWS: `python-pulumi/src/ptd/pulumi_resources/aws_workload_sites.py`
- Azure: `python-pulumi/src/ptd/pulumi_resources/azure_workload_sites.py`

**Language:** Python/Pulumi
**Proxy Required:** Yes (creates Kubernetes CRDs)

**Creates:**
- `TeamSite` custom resources (Custom Resource Definitions (CRDs) consumed by Team Operator)
- Ingress resources for each site
- DNS records (Route53 for AWS, Azure DNS for Azure)
- Site-specific secrets

**Depends on:**
- `helm`: Needs Team Operator running to reconcile `TeamSite` CRDs
- `persistent`: Needs ACM certificates (AWS) or Key Vault certificates (Azure), IAM roles (AWS) or managed identities (Azure)

**Why seventh:** Sites are the user-facing entry points to Posit Team products. The operator must be running before you create them.

**Proxy rationale:** Creates Kubernetes resources via API.

**Safe to re-run:** Yes, but may cause brief DNS propagation delays.

---

### Step 8: persistent_reprise (Go) {#persistent-reprise}
**Implementation:** `lib/steps/persistent_reprise.go`
**Language:** Go
**Proxy Required:** No

**Purpose:** Re-runs the `persistent` step to update AWS Secrets Manager (AWS) or Azure Key Vault (Azure) with outputs from later steps (e.g., cluster endpoints, load balancer DNS names).

**Why last:** Secrets Manager/Key Vault acts as a cross-step data store. This step makes all outputs from all steps available for future operations or debugging.

**Azure note:** Azure does NOT have control room support (workload-only deployments). This step only applies to Azure workloads, not control rooms.

**Safe to re-run:** Yes, idempotent.

---

## Control room steps

Control rooms use a simpler 4-step pipeline:

```
workspaces → persistent → postgres_config → cluster
```

### Step 1: workspaces (Python) {#workspaces}
**Implementation:** `python-pulumi/src/ptd/pulumi_resources/aws_control_room_workspaces.py`
**Proxy Required:** No

**Creates:**
- AWS IAM roles for each customer workload
- Cross-account trust relationships
- S3 buckets for workspace state

**Why first:** Sets up the multi-tenant workspace infrastructure before deploying the control plane cluster.

---

### Step 2: persistent (Python) {#persistent-control-room}
**Implementation:** `python-pulumi/src/ptd/pulumi_resources/aws_control_room_persistent.py`
**Proxy Required:** No

Same as workload persistent: VPC, RDS, S3, IAM roles, etc.

---

### Step 3: postgres_config (Python) {#postgres-config-control-room}
**Implementation:** `python-pulumi/src/ptd/pulumi_resources/aws_control_room_postgres_config.py`
**Proxy Required:** Yes

Same as workload postgres_config: database users, permissions, extensions.

---

### Step 4: cluster (Python) {#cluster}
**Implementation:** `python-pulumi/src/ptd/pulumi_resources/aws_control_room_cluster.py`
**Proxy Required:** Yes

**Creates:**
- EKS/AKS cluster
- Namespaces
- Helm charts (Grafana, Mimir, etc.)
- All-in-one step combining workload steps 4-7

**Why combined:** Control rooms are simpler deployments without the multi-site complexity of workloads.

---

## Pulumi stack naming convention

Each step creates a Pulumi stack with a consistent naming pattern:

**Format:**
```
organization/<project>/<stack>
```

**Where:**
- `organization`: Always `"organization"` (hardcoded in `lib/pulumi/python.go:40`)
- `project`: `ptd-<cloud>-<target-type>-<step-name>`
  - Example: `ptd-aws-workload-persistent`
- `stack`: `<target-name>`
  - Example: `myworkload-staging`

**Full stack name example:**
```
organization/ptd-aws-workload-persistent/myworkload-staging
```

**Code location:** `lib/pulumi/python.go:38-40`

---

## Selective step execution

### --only-steps

Run specific steps by name, skipping others:

```bash
ptd ensure myworkload-staging --only-steps postgres_config,helm
```

**Use cases:**
- Re-run a single failed step
- Update configuration for one component without re-running the entire pipeline

**Safety:**
- Safe if the step doesn't depend on changes in skipped steps
- Dangerous if upstream resources changed (e.g., running `helm` without running `persistent` first after changing VPC config)

**Validation:** Go validates that step names are valid before execution.

---

### --start-at-step

Run all steps starting from a specific step:

```bash
ptd ensure myworkload-staging --start-at-step helm
```

**Use cases:**
- Resume a partially-failed deployment
- Re-run everything downstream of a change

**Safety:**
- Safer than `--only-steps` because it doesn't skip downstream steps
- Still requires that upstream steps are already complete and up-to-date

**Implementation:** `lib/steps/steps.go:88-107`

---

### --list-steps

List all available steps for a target:

```bash
ptd ensure myworkload-staging --list-steps
```

**Output example:**
```
bootstrap
persistent
postgres_config
eks
clusters
helm
sites
persistent_reprise
```

---

## When steps are safe to re-run

| Step | Safe to Re-run? | Notes |
|------|-----------------|-------|
| `bootstrap` | ✅ Yes | Idempotent, no destructive changes |
| `persistent` | ⚠️ Mostly | VPC/RDS changes may require manual intervention |
| `postgres_config` | ✅ Yes | Idempotent, only creates missing users/grants |
| `eks`/`aks` | ⚠️ Mostly | Cluster version upgrades may cause downtime |
| `clusters` | ✅ Yes | Namespace creation is idempotent |
| `helm` | ⚠️ Mostly | Chart upgrades may restart pods |
| `sites` | ✅ Yes | CRD updates are reconciled by operator |
| `persistent_reprise` | ✅ Yes | Idempotent secret updates |

---

## Understanding proxy requirements

Some steps require a proxy to access private resources:

| Step | Proxy? | Why? |
|------|--------|------|
| `bootstrap` | No | Creates public S3 and IAM resources |
| `persistent` | No | Creates cloud resources via AWS/Azure APIs |
| `postgres_config` | **Yes** | Connects to RDS in private subnet |
| `eks`/`aks` | No | Creates cluster via cloud APIs |
| `clusters` | **Yes** | Accesses private Kubernetes API |
| `helm` | **Yes** | Accesses private Kubernetes API |
| `sites` | **Yes** | Accesses private Kubernetes API |
| `persistent_reprise` | No | Updates Secrets Manager via AWS API |

**Proxy mechanisms:**
- **SSM Session Manager:** Via bastion host in the VPC
- **Tailscale:** If `tailscale_enabled: true` in config

**Code check:** `lib/steps/steps.go:127-134` (`ProxyRequiredSteps()`)

---

## Debugging step execution

### View step status
```bash
ptd ensure myworkload-staging --dry-run
```

Shows what would change without applying.

### Run a single step for debugging
```bash
export AWS_PROFILE=ptd-staging
ptd ensure myworkload-staging --only-steps postgres_config
```

### Inspect Pulumi state
```bash
export AWS_PROFILE=ptd-staging
ptd workon myworkload-staging persistent -- pulumi stack export
```

See [CLAUDE.md](../../CLAUDE.md) for more debugging commands.

---

## Related documentation
- [Config Flow](./config-flow.md) - How configuration flows from YAML to Go to Python
- [Pulumi Conventions](./pulumi-conventions.md) - Pulumi-specific patterns
