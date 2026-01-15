# PTD CLI Developer Reference

## Overview

The PTD (Posit Team Dedicated) CLI is a command-line tool for managing Posit Team Dedicated environments across multiple cloud providers (AWS and Azure). It provides a unified interface for deploying, managing, and interacting with both control room and workload environments.

**Implementation**: Go (using Cobra framework)
**Location**: `/cmd` directory
**Main entry point**: `/cmd/main.go`

## Installation

Build and install the CLI:

```bash
just cli
```

This compiles the CLI and places the binary in `~/.local/bin/ptd` (ensure this is in your PATH).

## Global Configuration

### Configuration Files

The CLI searches for configuration files in the following order:
1. `~/.config/ptd/ptdconfig.yaml`
2. `~/.local/share/ptd/ptdconfig.yaml`
3. `./ptdconfig.yaml` (current directory)
4. `~/ptdconfig.yaml` (home directory)

### Environment Variables

All configuration can be overridden using environment variables with the `PTD_` prefix:
- `PTD_VERBOSE=true` - Enable verbose logging
- `PROJECT_ROOT` - Override project root directory

### Global Flags

All commands support these global flags:

| Flag | Short | Type | Default | Description |
|------|-------|------|---------|-------------|
| `--verbose` | `-v` | bool | false | Enable verbose/debug output |

### Project Root Detection

The CLI determines the project root in this order:
1. `PROJECT_ROOT` environment variable
2. Binary location (2 levels up from `.local/bin/ptd`)
3. Git repository root

---

## Commands

### `ptd version`

Print the version number of the PTD CLI.

**Usage:**
```bash
ptd version
```

**Example:**
```bash
$ ptd version
PTD CLI v1.0.0
```

**Implementation:** `/cmd/version.go:13`

---

### `ptd config`

Manage PTD configuration files and settings.

#### `ptd config show`

Show the current configuration values and which config file is being used.

**Usage:**
```bash
ptd config show
```

**Example Output:**
```
PTD Configuration
================
Config file: /Users/username/.config/ptd/ptdconfig.yaml

Configuration values:
  verbose: false
  top: /Users/username/source/ptd
```

**Implementation:** `/cmd/config.go:21`

#### `ptd config init`

Initialize a new configuration file with default values at `~/.config/ptd/ptdconfig.yaml`.

**Usage:**
```bash
ptd config init
```

**Example:**
```bash
$ ptd config init
Configuration file created: /Users/username/.config/ptd/ptdconfig.yaml
You can now edit this file to customize your ptd settings.
```

**Implementation:** `/cmd/config.go:49`

#### `ptd config path`

Show the paths where PTD looks for configuration files.

**Usage:**
```bash
ptd config path
```

**Example Output:**
```
PTD configuration file search paths:
1. /Users/username/.config/ptd/ptdconfig.yaml
2. /Users/username/.local/share/ptd/ptdconfig.yaml
3. ./ptdconfig.yaml (current directory)
4. /Users/username/ptdconfig.yaml (home directory)

Environment variables with 'PTD_' prefix are also read automatically.
```

**Implementation:** `/cmd/config.go:58`

---

### `ptd assume`

Assume the admin role in a target account and export credentials.

**Usage:**
```bash
ptd assume <target> [flags]
```

**Arguments:**
- `<target>` - Target name (supports auto-completion from available targets)

**Flags:**
| Flag | Short | Type | Default | Description |
|------|-------|------|---------|-------------|
| `--export` | `-e` | bool | true | Export the role credentials |

**Examples:**

Export AWS credentials for a target:
```bash
$ ptd assume ganso01-staging
# Exporting session for arn:aws:sts::123456789012:assumed-role/admin.posit.team/user@example.com
# In order to use this directly, run:
# eval $(ptd assume ganso01-staging)
export AWS_ACCESS_KEY_ID=ASIA...
export AWS_SECRET_ACCESS_KEY=...
export AWS_SESSION_TOKEN=...
```

Evaluate credentials directly in your shell:
```bash
eval $(ptd assume ganso01-staging)
```

For Azure targets:
```bash
$ ptd assume azure-target
# Azure session: user@example.com
# Azure credentials are not exported, the `az` cli state is set instead.
```

**Implementation:** `/cmd/assume.go:19`

---

### `ptd ensure`

Ensure a target is converged by running infrastructure deployment steps. This command orchestrates the deployment using Pulumi to bring the target to its desired state.

**Usage:**
```bash
ptd ensure <target> [flags]
```

**Arguments:**
- `<target>` - Target name (supports auto-completion from available targets)

**Flags:**

| Flag | Short | Type | Default | Description |
|------|-------|------|---------|-------------|
| `--dry-run` | `-n` | bool | false | Dry run the command without making changes |
| `--preview` | `-p` | bool | true | Preview the stack changes before applying |
| `--cancel` | `-c` | bool | false | Clear locks from the stack |
| `--refresh` | `-r` | bool | false | Refresh the stack state before applying |
| `--auto-apply` | `-a` | bool | false | Skip manual approval and automatically apply changes |
| `--destroy` | | bool | false | Destroy the Pulumi stack |
| `--start-at-step` | | string | "" | Start at a specific step (supports tab completion) |
| `--only-steps` | | []string | nil | Only run specific steps (supports tab completion) |
| `--exclude-resources` | | []string | nil | Exclude specific resources from the ensure process |
| `--target-resources` | | []string | nil | Target specific resources for the ensure process |

**Step Names:**

Available steps vary by target type (workload vs control room). Steps are defined in `/lib/steps/`.

Common workload steps (in order):
1. `bootstrap` - Initial infrastructure setup
   - Creates Pulumi state storage (S3 bucket or Azure blob storage)
   - Creates encryption keys (KMS for AWS, Key Vault for Azure)
   - Initializes secrets for workload and sites
   - Requires: Control room target configuration
2. `persistent` - Persistent resources (storage, databases)
   - Creates RDS/Azure Database instances
   - Creates file systems (EFS/Azure Files)
   - Creates S3/blob storage buckets for chronicle and package manager
   - Outputs: Database URLs, file system DNS names, mimir password
3. `postgres_config` - PostgreSQL database configuration
   - Configures PostgreSQL databases and users
   - Requires: Database endpoints from persistent step
   - Requires: Proxy connection (if Tailscale not enabled)
4. `images` - Copy container images
   - Copies Posit product images from control room registry to workload registry
   - Requires: Source (control room) registry credentials
   - Requires: Destination (workload) registry credentials
5. `registry` - Container registry setup (ecr_cache for AWS, acr_cache for Azure)
   - Creates pull-through cache rules for Docker Hub
   - Requires: Docker Hub OAT from control room secret store
6. `kubernetes` - Kubernetes cluster setup (eks for AWS, aks for Azure)
   - Creates EKS or AKS Kubernetes cluster
   - Configures cluster networking and security
   - Requires: Proxy connection (if Tailscale not enabled)
7. `clusters` - Cluster configuration
   - Configures Kubernetes cluster resources and add-ons
   - Requires: Kubernetes cluster from previous step
   - Requires: Proxy connection
8. `helm` - Helm chart deployment
   - Deploys Posit Team products via Helm charts
   - Requires: Kubernetes cluster access
   - Requires: Proxy connection (if Tailscale not enabled)
9. `sites` - Site configuration
   - Configures individual Posit Team sites
   - Requires: Kubernetes cluster access
   - Requires: Proxy connection
10. `persistent_reprise` - Final persistent resource updates
    - Re-runs persistent step to update secrets with final state
    - Updates workload secrets and control room mimir passwords

Common control room steps (in order):
1. `bootstrap` - Initial infrastructure setup
   - Creates Pulumi state storage (S3 bucket or Azure blob storage)
   - Creates encryption keys (KMS for AWS, Key Vault for Azure)
   - Creates admin policy resources (if enabled)
   - Initializes vault secrets for control room
2. `workspaces` - Workspace configuration
   - Creates workspaces infrastructure for control room
   - Configures workspace resources via Pulumi
3. `persistent` - Persistent resources (storage, databases)
   - Creates RDS/Azure Database instances
   - Creates file systems and storage resources
   - Outputs: Database URLs and connection information
4. `postgres_config` - PostgreSQL database configuration
   - Configures PostgreSQL databases and users for control room
   - Requires: Database endpoints from persistent step
   - Requires: Proxy connection (if Tailscale not enabled)
5. `cluster` - Cluster setup
   - Creates and configures control room Kubernetes cluster
   - Deploys cluster infrastructure and Helm charts
   - Requires: Proxy connection

**Examples:**

Full deployment with preview:
```bash
ptd ensure ganso01-staging
```

Auto-apply without manual confirmation:
```bash
ptd ensure ganso01-staging --auto-apply
```

Run only specific steps:
```bash
ptd ensure ganso01-staging --only-steps cluster,helm
```

Start at a specific step:
```bash
ptd ensure ganso01-staging --start-at-step helm
```

Destroy a stack (runs steps in reverse order):
```bash
ptd ensure ganso01-staging --destroy
```

Target specific resources:
```bash
ptd ensure ganso01-staging --target-resources my-resource
```

Exclude resources:
```bash
ptd ensure ganso01-staging --exclude-resources problematic-resource
```

Dry run to see what would change:
```bash
ptd ensure ganso01-staging --dry-run
```

**Implementation:** `/cmd/ensure.go:50`

**Notes:**
- For workload targets, automatically loads the associated control room target
- Automatically starts proxy session if required by steps and Tailscale is not enabled
- When `--destroy` is specified, steps run in reverse order

---

### `ptd workon`

Start an interactive shell with credentials and environment configured for a target. Optionally, work within a specific Pulumi stack directory.

**Usage:**
```bash
ptd workon <cluster> [step] [flags]
```

**Arguments:**
- `<cluster>` - Target name (supports auto-completion)
- `[step]` - Optional: specific Pulumi step/stack to work on

**Examples:**

Open shell with target credentials:
```bash
ptd workon ganso01-staging
```

Work on a specific step (opens shell in Pulumi stack directory):
```bash
ptd workon ganso01-staging helm
```

**What it does:**
1. Loads target configuration
2. Assumes appropriate credentials
3. Creates/loads Pulumi stack if step is specified
4. Opens an interactive shell with:
   - Cloud provider credentials set
   - Working directory set to Pulumi stack (if step specified)
   - Full environment inherited

**Implementation:** `/cmd/workon.go:18`

**Example session:**
```bash
$ ptd workon ganso01-staging helm
Starting interactive shell in /path/to/stack with session identity arn:aws:sts::123456789012:assumed-role/admin.posit.team/user@example.com
To exit the shell, type 'exit' or press Ctrl+D
(shell opens with environment configured)
```

---

### `ptd proxy`

Start a SOCKS5 proxy session to the bastion host in a given target. The proxy runs on `localhost:1080` and enables secure access to private resources.

**Usage:**
```bash
ptd proxy <target> [flags]
```

**Arguments:**
- `<target>` - Target name (supports auto-completion)

**Flags:**

| Flag | Short | Type | Default | Description |
|------|-------|------|---------|-------------|
| `--daemon` | `-d` | bool | false | Run the proxy in the background |
| `--stop` | `-s` | bool | false | Stop any running proxy session |

**Examples:**

Start proxy in foreground (blocks until Ctrl+C):
```bash
ptd proxy ganso01-staging
```

Start proxy in background:
```bash
ptd proxy ganso01-staging --daemon
```

Stop running proxy:
```bash
ptd proxy ganso01-staging --stop
```

**Implementation:** `/cmd/proxy.go:26`

**Notes:**
- Proxy runs on `localhost:1080`
- Proxy session state is stored in `~/.local/share/ptd/proxy.json`
- Works with both AWS and Azure targets
- Automatically handles credential management
- Not needed if Tailscale is enabled for the target

**Use cases:**
- Access private Kubernetes clusters
- Connect to internal services
- Required for `ensure` command when Tailscale is not enabled

---

### `ptd k9s`

Run k9s (Kubernetes CLI UI) on a target cluster with proper authentication and proxy configuration.

**Usage:**
```bash
ptd k9s <cluster> [flags]
```

**Arguments:**
- `<cluster>` - Target name (supports auto-completion)

**Flags:**

| Flag | Short | Type | Default | Description |
|------|-------|------|---------|-------------|
| `--namespace` | `-n` | string | "posit-team" | Namespace to focus on |
| `--args` | | []string | [] | Additional arguments to pass to k9s |

**Examples:**

Open k9s in default namespace:
```bash
ptd k9s ganso01-staging
```

Open k9s in specific namespace:
```bash
ptd k9s ganso01-staging -n kube-system
```

Pass additional k9s arguments:
```bash
ptd k9s ganso01-staging --args="--readonly"
```

**What it does:**
1. Loads target configuration
2. Starts proxy session (if needed and Tailscale not enabled)
3. Assumes credentials
4. Creates temporary kubeconfig with:
   - Proper cluster configuration
   - SOCKS5 proxy settings (if needed)
   - Authentication configured
5. Launches k9s with configured environment

**Implementation:** `/cmd/k9s.go:30`

**Notes:**
- Automatically handles cluster name resolution for both control room and workload targets
- For AWS EKS clusters, uses `aws eks update-kubeconfig`
- Kubeconfig is temporary and stored at `/tmp/kubeconfig-{target-hash}`
- Checks Tailscale connection status if enabled

**Cluster naming patterns:**
- Control room: `main01-{environment}` (e.g., `main01-staging`)
- Workload: `{target_name}-{release}` (e.g., `ganso01-main`)

---

### `ptd hash`

Return a stable hash value for a target name. Useful for generating unique identifiers based on target names.

**Usage:**
```bash
ptd hash <target>
```

**Arguments:**
- `<target>` - Target name (supports auto-completion)

**Example:**
```bash
$ ptd hash ganso01-staging
a1b2c3d4
```

**Implementation:** `/cmd/hash.go:14`

**Use cases:**
- Generate unique resource names
- Create consistent identifiers across deployments
- Useful in scripts and automation

---

### `ptd admin`

Run administrative commands for managing PTD infrastructure.

#### `ptd admin generate-role`

Generate the admin principal role CloudFormation template for AWS accounts.

**Usage:**
```bash
ptd admin generate-role [flags]
```

**Flags:**

| Flag | Short | Type | Default | Description |
|------|-------|------|---------|-------------|
| `--production` | `-p` | bool | true | Generate production role (vs staging) |

**Examples:**

Generate production role template:
```bash
ptd admin generate-role > admin-role-prod.yaml
```

Generate staging role template:
```bash
ptd admin generate-role --production=false > admin-role-staging.yaml
```

**What it generates:**
- CloudFormation template with:
  - Managed policy: `PositTeamDedicatedAdminPolicy`
  - IAM role: `admin.posit.team`
  - Trust policy for authorized principals
  - Permissions boundary
  - Self-protection policies

**Implementation:** `/cmd/admin.go:56`

**Usage:**
Deploy the generated template to AWS accounts to set up admin access:
```bash
ptd admin generate-role > template.yaml
aws cloudformation create-stack \
  --stack-name ptd-admin-role \
  --template-body file://template.yaml \
  --capabilities CAPABILITY_NAMED_IAM \
  --parameters ParameterKey=TrustedPrincipals,ParameterValue="arn:aws:iam::123456789012:user/admin"
```

#### `ptd admin export-accounts`

Export AWS account IDs for PTD accounts as shell export commands.

**Usage:**
```bash
ptd admin export-accounts
```

**Example Output:**
```bash
export PTD_AWS_ACCOUNT_LAB_PRODUCTION=123456789012
export PTD_AWS_ACCOUNT_LAB_STAGING=234567890123
export PTD_AWS_ACCOUNT_PRODUCTION=345678901234
export PTD_AWS_ACCOUNT_STAGING=456789012345
export PTD_AWS_ACCOUNT_TEAM=567890123456
```

**Usage:**
```bash
eval $(ptd admin export-accounts)
```

**Implementation:** `/cmd/admin.go:36`

---

## Target Auto-Completion

Many commands support auto-completion for `<target>` arguments. This is powered by the `ValidTargetArgs` function which reads available targets from `ptd.yaml` files.

**Implementation:** `/cmd/internal/legacy/ptd_config.go`

To enable shell completion:
```bash
# Bash
ptd completion bash > /etc/bash_completion.d/ptd

# Zsh
ptd completion zsh > "${fpath[1]}/_ptd"

# Fish
ptd completion fish > ~/.config/fish/completions/ptd.fish
```

---

## Architecture and Code Organization

### Command Structure

All commands follow the Cobra pattern:
- Each command defined in its own file under `/cmd/`
- Commands register themselves in `init()` functions
- Main entry point at `/cmd/main.go`

### Key Libraries

Located in `/lib/`:
- `aws/` - AWS-specific implementations (credentials, ECR, EKS, IAM, proxy, S3, SSM)
- `azure/` - Azure-specific implementations (credentials, ACR, AKS, Key Vault, proxy, storage)
- `steps/` - Deployment step definitions (bootstrap, cluster, helm, images, persistent, workspaces, sites)
- `types/` - Core type definitions (Target, Credentials, etc.)
- `proxy/` - Proxy session management
- `pulumi/` - Pulumi integration (inline, Python)
- `helpers/` - Utility functions (file operations, networking, process management)
- `secrets/` - Secret management
- `containers/` - Container operations
- `humans/` - User/principal management

### Target Types

Targets are loaded from `ptd.yaml` files and implement the `types.Target` interface:
- AWS targets: `aws.Target` (implements for AWS EKS)
- Azure targets: `azure.Target` (implements for Azure AKS)

Target features:
- Cloud provider abstraction
- Credential management
- Region configuration
- Proxy requirements
- Tailscale support
- Control room vs workload distinction

### Credentials

Credentials are managed through the `types.Credentials` interface:
- `Identity()` - Returns identity string
- `EnvVars()` - Returns environment variables map

Implementations:
- AWS: Assumes IAM roles, returns temporary credentials
- Azure: Uses Azure CLI authentication

### Proxy Sessions

Proxy sessions enable secure access to private resources:
- SOCKS5 proxy on `localhost:1080`
- Managed lifecycle (Start/Stop/Wait)
- State persistence in `~/.local/share/ptd/proxy.json`
- Automatic integration with ensure, k9s commands

AWS: Uses SSM Session Manager (`aws ssm start-session --target <bastion-instance>`)
Azure: Uses Azure Bastion proxy connection (`az network bastion tunnel`)

---

## Development

### Building

```bash
just build-cmd
```

### Testing

```bash
just test-cmd
```

### Adding New Commands

1. Create new file in `/cmd/` (e.g., `newcommand.go`)
2. Define command using Cobra:
```go
var newCmd = &cobra.Command{
    Use:   "new <arg>",
    Short: "Short description",
    Long:  `Long description`,
    Run: func(cmd *cobra.Command, args []string) {
        // Implementation
    },
}

func init() {
    rootCmd.AddCommand(newCmd)
    // Add flags if needed
}
```
3. Add any required flags in `init()`
4. Implement command logic
5. Add tests in `newcommand_test.go`

### Logging

Uses Go's `log/slog` package with `charmbracelet/log` for terminal output:
- `slog.Info()` - General information
- `slog.Debug()` - Debug information (requires `--verbose`)
- `slog.Warn()` - Warnings
- `slog.Error()` - Errors

Control log level:
```bash
ptd --verbose <command>  # Enable debug logging
```

---

## Common Workflows

### Deploy a new workload

```bash
# 1. Ensure control room is up
ptd ensure main01-staging --auto-apply

# 2. Deploy workload
ptd ensure ganso01-staging --auto-apply

# 3. Access the cluster
ptd k9s ganso01-staging
```

### Debug a deployment

```bash
# 1. Open interactive shell
ptd workon ganso01-staging helm

# 2. Manually run Pulumi commands
pulumi preview
pulumi up

# 3. Check specific resources
pulumi stack output
pulumi logs
```

### Update infrastructure

```bash
# Preview changes
ptd ensure ganso01-staging

# Apply after review
ptd ensure ganso01-staging --auto-apply
```

### Access private resources

```bash
# Start proxy in background
ptd proxy ganso01-staging --daemon

# Configure application to use SOCKS5 proxy on localhost:1080
export HTTPS_PROXY=socks5://localhost:1080

# When done, stop proxy
ptd proxy ganso01-staging --stop
```

---

## Troubleshooting

### Command not found

Ensure `~/.local/bin` is in your PATH:
```bash
export PATH="$HOME/.local/bin:$PATH"
```

### Credential errors

Verify you can assume the role:
```bash
ptd assume <target> -v
```

Check your AWS/Azure CLI is configured:
```bash
aws sts get-caller-identity
az account show
```

### Proxy connection fails

1. Check bastion instance is running
2. Verify security groups allow SSM/Bastion traffic
3. Try manual proxy connection
4. Enable verbose logging: `ptd proxy <target> -v`

### K9s can't connect

1. Verify cluster exists: `aws eks list-clusters --region <region>`
2. Check kubeconfig: `cat /tmp/kubeconfig-<hash>`
3. Test kubectl: `kubectl --kubeconfig /tmp/kubeconfig-<hash> get nodes`
4. Enable verbose logging: `ptd k9s <target> -v`

### Pulumi errors

1. Check stack exists: `pulumi stack ls`
2. Verify credentials: `ptd assume <target>`
3. Try clearing locks: `ptd ensure <target> --cancel`
4. Work interactively: `ptd workon <target> <step>`

---

## Configuration Reference

### ptdconfig.yaml

Example configuration file:

```yaml
verbose: false
# Add custom configuration values as needed
```

### Target Configuration (ptd.yaml)

Target configurations are defined in `ptd.yaml` files throughout the `/infra` directory. These are loaded by the CLI's internal legacy configuration system.

Example structure:
```yaml
targets:
  ganso01-staging:
    cloud_provider: aws
    region: us-east-1
    control_room: false
    tailscale_enabled: false
    # Additional target-specific configuration
```

---

## Related Documentation

- [Main README](../../README.md) - Project overview
- [Getting Started](../GETTING_STARTED.md) - Setup prerequisites
- [Configuration Guide](../CONFIGURATION.md) - Configuration reference
- [Justfile](../../Justfile) - Build and development tasks

---

## API Reference

### types.Target Interface

```go
type Target interface {
    Name() string
    Region() string
    CloudProvider() CloudProvider
    ControlRoom() bool
    Credentials(ctx context.Context) (Credentials, error)
    HashName() string
    TailscaleEnabled() bool
    PulumiBackendUrl() string
    PulumiSecretsProviderKey() string
}
```

### types.Credentials Interface

```go
type Credentials interface {
    Identity() string
    EnvVars() map[string]string
}
```

### steps.Step Interface

```go
type Step interface {
    Name() string
    Set(target Target, controlRoom Target, opts StepOptions)
    Run(ctx context.Context) error
}
```

---

*Last updated: 2025*
