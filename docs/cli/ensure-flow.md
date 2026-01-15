# Ensure Command Flow

This document describes the sequence of steps executed when running `ptd ensure` for both control room and workload targets.

## Overview

The `ensure` command converges a target's infrastructure to match its configuration. It orchestrates multiple steps that provision cloud resources, configure Kubernetes clusters, and deploy applications. Each step builds upon the outputs of previous steps, creating a complete Posit Team Dedicated environment.

## Command Execution Flow

### 1. Initialization

When you run `ptd ensure <target>`:

1. Loads the target configuration from `ptd.yaml`
2. Determines if the target is a control room or workload
3. For workloads, also loads the associated control room configuration
4. Loads all steps (standard + any custom steps defined for the workload)
5. Filters steps based on flags (`--only-steps`, `--start-at-step`)
6. Sets options on each step (dry-run, preview, auto-apply, etc.)

### 2. Proxy Session Management

If any step requires a proxy connection (to access private resources like databases or Kubernetes clusters):

- **AWS**: Starts an SSM Session Manager tunnel through a bastion host
- **Azure**: Starts an Azure Bastion tunnel
- **Skipped if**: Tailscale is enabled on the target
- The proxy runs on `localhost:1080` and is available to all subsequent steps

### 3. Step Execution

Steps are executed sequentially in the order defined. Each step typically:

1. Retrieves cloud credentials for the target
2. Creates or selects a Pulumi stack
3. Runs Pulumi operations: preview, refresh, up, or destroy
4. Updates secrets or outputs as needed

## Control Room Steps

Control rooms provide centralized management and monitoring for workloads.

### 1. workspaces

**Purpose**: Provision workspace infrastructure

**Implementation**: Python Pulumi (`python-pulumi/src/ptd/pulumi_resources/aws_control_room_workspaces.py`)

Creates the infrastructure needed for administrative workspaces in the control room.

### 2. persistent

**Purpose**: Create persistent infrastructure resources

**Implementation**: Python Pulumi (`python-pulumi/src/ptd/pulumi_resources/aws_control_room_persistent.py`)

Provisions long-lived infrastructure components that persist across deployments. Outputs from this step are used by subsequent steps and stored in secrets for workload access.

### 3. postgres_config

**Purpose**: Configure PostgreSQL database settings

**Proxy Required**: Yes (connects to database)

**Implementation**: Python Pulumi (`python-pulumi/src/ptd/pulumi_resources/aws_control_room_postgres_config.py`)

Connects to the RDS PostgreSQL instance to configure:
- Database users and permissions
- Database schemas
- Extensions and settings

Uses proxy connection (or Tailscale) to access the private database endpoint.

### 4. cluster

**Purpose**: Create the control room Kubernetes cluster

**Proxy Required**: Yes

**Implementation**: Python Pulumi (`python-pulumi/src/ptd/pulumi_resources/aws_control_room_cluster.py`)


Provisions the Kubernetes cluster that hosts control room applications and monitoring tools.

## Workload Steps

Workloads host the Posit Team products (Connect, Workbench, Package Manager) for end users.

### 1. bootstrap

**Purpose**: Set up cloud infrastructure prerequisites

**Implementation**: Go (`lib/steps/bootstrap.go`)

**AWS Actions**:
- Create S3 bucket for Pulumi state storage
- Create KMS key for state encryption
- Create admin IAM policy (permissions boundary)
- Initialize secrets in AWS Secrets Manager:
  - Workload secret
  - Site secrets for each configured site
  - Site session secrets
  - SSH vault for Package Manager

**Azure Actions**:
- Create resource group for infrastructure
- Create Key Vault for secrets
- Create encryption key for Pulumi state
- Create storage account and blob container
- Initialize site secrets in Key Vault (one secret per field)

### 2. persistent

**Purpose**: Create persistent infrastructure resources

**Implementation**: Python Pulumi (`python-pulumi/src/ptd/pulumi_resources/aws_workload_persistent.py`)

Creates the foundational infrastructure for the workload:

**AWS Resources**:
- **VPC**: Virtual network with public and private subnets across availability zones
- **RDS**: PostgreSQL database for Posit Team products
- **S3 Buckets**:
  - Package Manager repository storage
  - Chronicle audit log storage
  - Loki log storage
  - Mimir metrics storage
- **IAM Roles and Policies**:
  - Load Balancer Controller role
  - ExternalDNS role
  - Traefik Forward Auth role
  - EBS CSI Driver role
  - Loki and Mimir access policies
  - Team Operator policy
- **ACM Certificates**: TLS certificates for site domains with DNS validation
- **File Systems**:
  - FSx for OpenZFS (for shared storage)
  - EFS (optional, for shared storage)
  - Security groups for NFS access
- **Bastion Host**: For secure access to private resources
- **Secrets**: Mimir password (random)

**Outputs**: Database connection details, bucket names, file system IDs, DNS names

**Post-Stack Actions** (AWS only):
- Updates the workload secret in Secrets Manager with infrastructure outputs
- Updates the control room's Mimir authentication secret with the workload's Mimir password

**Azure Resources**: Similar resources using Azure equivalents (VNet, Azure Database for PostgreSQL, Storage Accounts, Managed Identities, etc.)

### 3. postgres_config

**Purpose**: Configure PostgreSQL database

**Proxy Required**: Yes (connects to database)

**Implementation**: Python Pulumi (`python-pulumi/src/ptd/pulumi_resources/aws_workload_postgres_config.py`)

Configures the RDS database created in the persistent step:
- Creates databases for each Posit Team product
- Sets up database users with appropriate permissions
- Configures required PostgreSQL extensions
- Sets database parameters

### 4. eks / aks (Cloud Provider Selector)

**Purpose**: Create the Kubernetes cluster

**Proxy Required**: Yes (EKS), No (AKS)

This step is selected based on the cloud provider:

#### AWS - EKS Step

**Implementation**: Python Pulumi (`python-pulumi/src/ptd/pulumi_resources/aws_workload_eks.py`)

Creates Amazon EKS clusters:
- EKS control plane with specified Kubernetes version
- Enables cluster logging (API, audit, authenticator, controller manager, scheduler)
- Creates node groups with EC2 launch templates
- Configures security groups for cluster and node communication
- Sets up OIDC identity provider for IAM roles for service accounts (IRSA)
- Optionally installs Tigera operator for network policies

#### Azure - AKS Step

**Implementation**: Go Pulumi (`lib/steps/aks.go`)

Creates Azure Kubernetes Service clusters:
- AKS control plane with private cluster configuration
- **System node pool**: Runs Kubernetes system components (2-5 nodes, with taint)
- **User node pools**: Configurable pools for workload pods
  - Auto-scaling configuration
  - Custom taints and labels
  - Configurable disk sizes and VM sizes
- Enables Azure AD integration and RBAC
- Configures Azure Key Vault Secrets Provider addon
- Enables workload identity and OIDC issuer
- Sets up network policies (Calico) and overlay networking
- Configures auto-upgrade for node OS images and patches

### 5. clusters

**Purpose**: Configure Kubernetes cluster settings

**Proxy Required**: Yes

**Implementation**: Python Pulumi (`python-pulumi/src/ptd/pulumi_resources/aws_workload_clusters.py`)

Configures the Kubernetes cluster created in the previous step:
- Deploys cluster-wide Kubernetes resources
- Configures namespaces
- Sets up cluster-level network policies
- Prepares the cluster for application deployment

### 6. helm

**Purpose**: Deploy Helm charts to Kubernetes

**Proxy Required**: Yes

**Implementation**: Python Pulumi (`python-pulumi/src/ptd/pulumi_resources/aws_workload_helm.py`)

Deploys essential Kubernetes applications and controllers:
- **Team Operator**: Posit's operator for managing Team product deployments
- **Monitoring Stack**: Grafana Alloy, Loki, Mimir, or other observability tools
- **Ingress Controllers**: Traefik or other ingress solutions
- **Certificate Management**: Cert-manager for TLS certificate automation
- **DNS Management**: ExternalDNS for automatic DNS record creation
- **Storage Providers**: CSI drivers and storage class configuration
- **Authentication**: Keycloak or other identity providers

Helm releases are deployed with values configured from the target's `ptd.yaml` and infrastructure outputs.

### 7. sites

**Purpose**: Create site-specific resources
**Proxy Required**: Yes
**Implementation**: Python Pulumi (`python-pulumi/src/ptd/pulumi_resources/aws_workload_sites.py`)

Deploys resources for each Posit Team site:
- Creates `TeamSite` custom resources (managed by Team Operator)
- Configures site-specific ingress rules
- Sets up DNS records for site domains
- Configures site secrets and configuration

A "site" represents a complete deployment of Posit Team products (Connect, Workbench, Package Manager) with a specific configuration and domain.

### 8. persistent_reprise

**Purpose**: Update secrets with resources created by later steps

**Implementation**: Go (`lib/steps/persistent_reprise.go`)

Re-runs the persistent step to:
- Refresh infrastructure outputs
- Update secrets that depend on resources created in helm or sites steps
- Ensure all secrets contain complete information for the running environment

This is necessary because some secret values are only known after Kubernetes resources are deployed.

## Step Options and Flags

The ensure command supports several flags that modify step execution:

- **`--dry-run`**: Show what would be changed without making changes
- **`--preview`**: Show a preview of changes before applying (default: true)
- **`--auto-apply`**: Skip manual approval and automatically apply changes
- **`--refresh`**: Refresh Pulumi state before preview/up
- **`--cancel`**: Clear locks from Pulumi stacks
- **`--destroy`**: Destroy resources (steps run in reverse order)
- **`--start-at-step <name>`**: Start execution at a specific step
- **`--only-steps <names>`**: Run only specific steps
- **`--list-steps`**: List all available steps and exit
- **`--exclude-resources <urns>`**: Exclude specific Pulumi resources
- **`--target-resources <urns>`**: Target specific Pulumi resources only

## Custom Steps

Workloads can define custom steps in their configuration directory. Custom steps are:
- Loaded from the workload's target directory
- Inserted into the step sequence based on their configuration
- Executed alongside standard steps
- Useful for workload-specific customizations

Use `ptd ensure <target> --list-steps` to see all steps (standard and custom) for a target.

See [Custom Steps Guide](/docs/cli/custom-steps.md)

## Implementation Details

### Step Interface

All steps implement the `Step` interface (`lib/steps/steps.go`):

```go
type Step interface {
    Run(ctx context.Context) error
    Set(t types.Target, controlRoomTarget types.Target, options StepOptions)
    Name() string
    ProxyRequired() bool
}
```

### Pulumi Stack Naming

Pulumi stacks follow this naming convention:
- **Project**: `ptd-<cloud>-<target-type>-<step-name>`
- **Stack**: `organization/<project>/<target-name>`

Example: `ptd-aws-workload-persistent` project with stack `organization/ptd-aws-workload-persistent/workload01`

### Python Pulumi Integration

Go code dynamically generates a `__main__.py` file for each Python Pulumi stack:

```python
import ptd.pulumi_resources.<module>

ptd.pulumi_resources.<module>.<Class>.autoload()
```

The `PTD_ROOT` environment variable is passed to Python, allowing it to locate target configurations.

## Error Handling

If a step fails:
- Execution stops immediately
- The error is logged with the step name
- Subsequent steps are not executed
- Fix the error and re-run `ensure`, optionally using `--start-at-step` to resume

## See Also

- [CLI Reference](../cli/README.md) - Complete CLI command documentation
- [Custom Steps Guide](../cli/custom-steps.md) - Creating custom steps for workloads
