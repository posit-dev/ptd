# Getting Started with PTD

This guide walks you through setting up PTD to deploy Posit Team products on AWS or Azure.

## Prerequisites

### Required Tools

| Tool | Version | Description |
|------|---------|-------------|
| [Go](https://golang.org/dl/) | 1.21+ | For building the CLI |
| [Python](https://www.python.org/downloads/) | 3.12+ | For Pulumi IaC |
| [uv](https://github.com/astral-sh/uv) | Latest | Python package manager |
| [Pulumi](https://www.pulumi.com/docs/get-started/install/) | 3.x | Infrastructure as Code |
| [just](https://github.com/casey/just) | Latest | Command runner |
| [goreleaser](https://goreleaser.com/install/) | Latest | For building releases |

### Cloud Provider Tools

**For AWS:**
- [AWS CLI](https://aws.amazon.com/cli/) v2
- [AWS Session Manager Plugin](https://docs.aws.amazon.com/systems-manager/latest/userguide/session-manager-working-with-install-plugin.html)
- AWS credentials configured (via `aws configure` or environment variables)

**For Azure:**
- [Azure CLI](https://docs.microsoft.com/en-us/cli/azure/install-azure-cli)
- Azure credentials configured (via `az login`)

## Installation

### 1. Clone the Repository

```bash
git clone https://github.com/posit-dev/ptd.git
cd ptd
```

### 2. Install Dependencies

```bash
just deps
```

This installs:
- Python dependencies via uv
- Go dependencies
- Required CLI tools symlinked to `.local/bin/`

### 3. Build the CLI

```bash
just build-cmd
```

The CLI binary is created at `.local/bin/ptd`.

### 4. Configure AWS Accounts (Optional)

Copy and edit the account configuration:

```bash
cp accounts.env.example accounts.env
# Edit accounts.env with your AWS account IDs
```

This step is optional - PTD can auto-detect your AWS account via STS.

## Setting Up Your First Deployment

### 1. Create a Targets Directory

PTD organizes deployments into "targets" - either control rooms or workloads.

```bash
mkdir -p my-infrastructure/__ctrl__/my-control-room
mkdir -p my-infrastructure/__work__/my-workload
```

### 2. Copy Example Configurations

```bash
# Copy control room example
cp examples/control-room/ptd.yaml my-infrastructure/__ctrl__/my-control-room/

# Copy workload example
cp -r examples/workload/* my-infrastructure/__work__/my-workload/
```

### 3. Edit Configurations

Edit the configuration files with your actual values:

**Control Room (`my-infrastructure/__ctrl__/my-control-room/ptd.yaml`):**
- AWS account ID
- Domain names
- Trusted principals (users who can manage)

**Workload (`my-infrastructure/__work__/my-workload/ptd.yaml`):**
- AWS account ID
- Control room reference
- Cluster configuration
- Site domains

**Site (`my-infrastructure/__work__/my-workload/site_main/site.yaml`):**
- Authentication (OIDC/SAML) configuration
- Product versions
- Replicas and scaling

### 4. Configure PTD

Tell PTD where your configurations are:

```bash
# Option 1: Environment variable
export PTD_TARGETS_CONFIG_DIR=/path/to/my-infrastructure

# Option 2: Config file (~/.config/ptd/ptdconfig.yaml)
echo "targets_config_dir: /path/to/my-infrastructure" > ~/.config/ptd/ptdconfig.yaml

# Option 3: CLI flag (per-command)
ptd --targets-config-dir /path/to/my-infrastructure <command>
```

### 5. Deploy

```bash
# Deploy the control room first
ptd ensure my-control-room

# Then deploy the workload
ptd ensure my-workload
```

## Architecture Overview

PTD deployments consist of:

```
Organization
├── Control Room (1 per org)
│   ├── EKS cluster
│   ├── DNS management
│   └── Shared services
│
└── Workloads (1+ per org)
    ├── AWS Infrastructure
    │   ├── VPC
    │   ├── EKS cluster(s)
    │   ├── RDS PostgreSQL
    │   └── FSx for OpenZFS
    │
    └── Sites (1+ per workload)
        ├── Workbench
        ├── Connect
        └── Package Manager
```

## Common Operations

### Access a Cluster

```bash
# Start a proxy to the cluster
ptd proxy my-workload

# In another terminal, use kubectl
export KUBECONFIG=./kubeconfig
kubectl get pods -A
```

### View Deployment Status

```bash
ptd workon my-workload
```

### Update a Deployment

Edit the configuration files, then re-run:

```bash
ptd ensure my-workload
```

## Next Steps

- [Configuration Reference](CONFIGURATION.md) - Detailed configuration options
- [CLI Reference](cli/PTD_CLI_REFERENCE.md) - All CLI commands
- [Examples](../examples/) - More example configurations
