# Posit Team Dedicated (PTD)

## Project Structure

The project is organized into several key components:

- **`./cmd`**: Contains the main CLI tool (Go implementation)
- **`./lib`**: Common Go libraries and utilities (including the inline-Go Pulumi infrastructure steps in `lib/steps`)
- **`./examples`**: Example configurations for control rooms and workloads
- **`./e2e`**: End-to-end tests
- **`./docs`**: Documentation (see [docs/README.md](docs/README.md) for structure)
  - **`./docs/cli`**: CLI reference documentation
  - **`./docs/team-operator`**: Team Operator documentation
  - **`./docs/guides`**: How-to guides for common tasks
  - **`./docs/infrastructure`**: Infrastructure documentation
- **`./Justfile`**: Command runner file with various tasks (`just -l` to list commands)

### Team Operator

The Team Operator is a Kubernetes operator that manages the deployment and configuration of Posit Team products within a Kubernetes cluster. It is maintained in a separate public repository: [posit-dev/team-operator](https://github.com/posit-dev/team-operator).

PTD consumes the Team Operator via its public Helm chart at `oci://ghcr.io/posit-dev/charts/team-operator`.

**Testing with adhoc images:** PR builds from posit-dev/team-operator publish adhoc images to GHCR. To test:
```yaml
# In ptd.yaml cluster spec
adhoc_team_operator_image: "ghcr.io/posit-dev/team-operator:adhoc-{branch}-{version}"
```

## CLI Configuration

The PTD CLI uses Viper for configuration management. Configuration can be set via:
- **CLI flags**: Highest precedence (e.g., `--targets-config-dir`)
- **Environment variables**: Second precedence (e.g., `PTD_TARGETS_CONFIG_DIR`)
- **Config file**: Third precedence (`~/.config/ptd/ptdconfig.yaml`)
- **Defaults**: Lowest precedence

### Targets Configuration Directory

PTD expects target configurations in a targets directory. Configure it via:

```yaml
# ~/.config/ptd/ptdconfig.yaml
targets_config_dir: /path/to/your/targets
```

Or via environment variable:
```bash
export PTD_TARGETS_CONFIG_DIR=/path/to/your/targets
```

Or via CLI flag:
```bash
ptd --targets-config-dir /path/to/your/targets ensure workload01
```

The targets configuration directory must contain:
- `__ctrl__/`: Control room configurations
- `__work__/`: Workload configurations

See [examples/](examples/) for example configurations.

## Build and Development Commands

### Overall Project Commands (from root Justfile)

- `just deps`: Install dependencies
- `just check`: Check all (includes linting and formatting)
- `just test`: Test all
- `just build`: Build all
- `just format`: Run automatic formatting

#### Check Commands

- `just check-go`: Vet Go code (lib and cmd)

#### Build Commands

- `just cli`: Build command-line tool

#### Test Commands

- `just test-cmd`: Test command-line tool
- `just test-e2e`: Run end-to-end tests (requires URL argument)
- `just test-lib`: Test library code

#### AWS Development

- `just aws-unset`: Unset all AWS environment variables

## Using the PTD CLI

### Proxy

The proxy subsystem manages SOCKS proxy sessions to target bastion hosts. All proxy state is stored in a shared registry file (`~/.local/share/ptd/proxies.json`), enabling multiple concurrent proxies.

**Starting a proxy:**

```bash
# Interactive use — binds to port 1080
ptd proxy <target>

# Daemon mode — binds to the deterministic workload port (10000–19999) and
# stays running in the background; this is the same port used by ensure/workon
ptd proxy <target> --daemon

# Explicit port
ptd proxy <target> --port 9090
```

**Print the deterministic port for a workload:**

```bash
ptd proxy port <target>
```

**Stopping proxies:**

```bash
# Stop one workload's proxy
ptd proxy <target> --stop

# Stop all running proxies
ptd proxy --stop
```

**Registry management:**

```bash
# List all proxy sessions recorded in the registry
ptd proxy --list

# Remove stale entries (dead PIDs / closed ports)
ptd proxy --prune
```

**Automatic proxy in ensure/workon:**

`ptd ensure` and `ptd workon` start a proxy automatically on the deterministic workload port and reuse an existing one if it is already running. For scripted or agent use, prefer `ptd workon <target> -- <cmd>` rather than managing proxies manually.

## Git Worktrees

**Always use git worktrees instead of plain branches.** This enables concurrent Claude sessions in the same repo.

### Creating a Worktree

This repo is expected to live at `ptd-workspace/ptd/`. The `../.worktrees/` relative path resolves to `ptd-workspace/.worktrees/` in that layout.

```bash
# New branch
git worktree add ../.worktrees/ptd-<branch-name> -b <branch-name>

# Existing remote branch
git worktree add ../.worktrees/ptd-<branch-name> <branch-name>
```

Always prefix worktree directories with `ptd-` to avoid collisions with other repos.

### After Creating a Worktree

1. **Build the binary** — each worktree needs its own ptd binary:
   ```bash
   cd ../.worktrees/ptd-<branch-name>
   just cli
   ```
2. **direnv** — if direnv is available, copy `envrc.recommended` to `.envrc` in the worktree, then run `direnv allow`. The file uses `source_up` to inherit workspace vars and overrides `PTD` to point to the worktree.
3. **For agents without direnv** — set env vars explicitly before running `ptd` commands:
   ```bash
   export PTD="$(pwd)"
   export PATH="${PTD}/.local/bin:${PATH}"
   ```

### Cleaning Up

```bash
# From the main checkout
git worktree remove ../.worktrees/ptd-<branch-name>
```

### Rules

- **NEVER** use `git checkout -b` for new work — always `git worktree add`
- **NEVER** put worktrees inside the repo directory — always use `../.worktrees/ptd-<name>`
- **ALWAYS** rebuild the binary after creating a worktree (`just cli`)
- Branch names: kebab-case, no slashes, no usernames (slashes break worktree directory paths)

## Monitoring and Alerts

### Alert Namespace Scope

Pod alerts (PodError, CrashLoopBackoff, DeploymentReplicaMismatch, etc.) are scoped to a minimal namespace allowlist to prevent false alerts from customer-deployed workloads:

**Monitored Namespaces**:
- **Application**: `posit-team`, `posit-team-system` (direct customer impact)
- **Observability**: `alloy`, `mimir`, `loki`, `grafana` (failures cause monitoring blindness)

**PromQL Filter**: `{namespace=~"posit-team|posit-team-system|alloy|mimir|loki|grafana"}`

**Why Infrastructure Namespaces Are Excluded**: Infrastructure namespaces (Calico, Traefik, kube-system) are excluded because their failures manifest as application failures, avoiding redundant alerts. For example:
- CNI failure → Network breaks → Application pods fail → Alert fires for application namespace
- Ingress failure → HTTP checks fail → `Healthchecks` alert fires

**Alert Configuration**: Alert definitions are in `lib/steps/assets/grafana_alerts/*.yaml` (embedded into the binary via `go:embed`). All pod-related alerts in `pods.yaml` include the namespace filter in their PromQL queries.

## Contributing

When contributing to the project:

1. Ensure that Snyk tests pass before merging a PR
2. Follow the development workflows described in the repository files
3. Use the provided Justfiles for common tasks
4. Always run `just format` before committing changes to ensure code style consistency

# Additional Instructions
- LLM coding instructions shared with copilot: [.github/copilot/copilot-instructions.md](.github/copilot/copilot-instructions.md)
- Follow the template in [.github/pull_request_template.md](.github/pull_request_template.md) to format PR descriptions correctly

## Architecture Overview

Brief pointer section:
- **Config Flow**: How YAML config is parsed by the Go CLI into `lib/types` structs → See `docs/architecture/config-flow.md`
- **Step Dependencies**: Deployment pipeline ordering and why → See `docs/architecture/step-dependencies.md`
- **Pulumi Conventions**: Resource naming and Output handling → See `docs/architecture/pulumi-conventions.md`

## Danger Zones

### Pulumi Resource Names
- **NEVER** change the first argument (logical name) to a Pulumi resource constructor without understanding state implications
- Changing `aws.s3.Bucket("my-bucket-name", ...)` to `aws.s3.Bucket("different-name", ...)` causes Pulumi to DELETE the old bucket and CREATE a new one
- This applies to ALL resources: VPCs, RDS instances, S3 buckets, IAM roles, EKS clusters, etc.
- If you need to rename a resource, discuss the state migration strategy first

### Config Is Defined Only in Go
- The Go structs in `lib/types/*.go` (e.g. `lib/types/workload.go`) are the **sole source of truth** for ptd.yaml config. Add or modify a config option there, with the appropriate YAML struct tag (snake_case).
- There is no longer a parallel Python dataclass to keep in sync. The Python config layer and the Go↔Python parity linter (`scripts/validate-config-sync.py`) were removed when Python was deleted from the repo.

### Builder Method Ordering
- The Go `EKSCluster` builder (`lib/aws/eks_cluster.go`) uses `With*()` methods with ordering dependencies
- Example: `WithNodeRole()` MUST be called before `WithNodeGroup()` (sets the default node role)
- Check method dependencies before reordering calls

### Resource Naming Conventions

**AWS:**
- IAM roles: `f"{purpose}.{compound_name}.posit.team"`
- S3 buckets: `f"{compound_name}-{purpose}"`
- EKS cluster resource name (the `aws.eks.Cluster` first arg / `name`): workload = `{compound_name}-{release}`; control room = bare `{compound_name}`. **Do NOT use `default_{compound_name}-control-plane` for the cluster resource**: that string is the kubeconfig *context* name, not the cluster's name; using it as the resource name would replace the live control plane.
- The naming helpers live in the Go cluster builder (`lib/aws/eks_cluster.go`), which produces these resource names verbatim for state adoption.

**Azure:**
- Resource Groups: `rsg-ptd-{sanitized_name}`
- Key Vault: `kv-ptd-{name[:17]}` (max 24 chars)
- Storage Accounts: `stptd{name_no_hyphens[:19]}` (NO hyphens, max 24 chars)
- VNets: `vnet-ptd-{compound_name}`
- The Azure naming helpers live in `lib/azure` (the Go workload/persistent implementations)
- Azure tags must convert `.` to `/` in tag keys

Do NOT introduce new naming patterns — follow existing conventions

## Key Patterns

### Inline-Go Pulumi Steps
- All `ptd ensure` steps are inline Go Pulumi programs defined in `lib/steps`. There is no per-step program file on disk and no autoload indirection; the program is compiled into the `ptd` binary.
- `ptd workon <target> <step>` opens a Go-runtime state workspace (no program) for manual `pulumi` state operations (`stack export/import`, `state unprotect/delete`). `pulumi preview/up` is not available from `workon`; use `ptd ensure` for those.

### AWS vs Azure Infrastructure Patterns

**AWS (EKS):**
- Uses the Go `EKSCluster` builder (`lib/aws/eks_cluster.go`) with `With*()` methods
- Builder methods have ordering dependencies (e.g., `WithNodeRole()` must come before `WithNodeGroup()`)
- EKS step is Go-based (`lib/steps/eks.go`, `lib/steps/eks_aws.go`, `lib/aws/eks_cluster.go`); the AWS control-room `cluster` step shares the same builder (`lib/steps/cluster.go`, `lib/aws/eks_cluster_cr.go`)

**Azure (AKS):**
- AKS step is Go-based (`lib/steps/aks.go`), like the EKS step
- Azure persistent resources are defined directly in the Go persistent step (no builder pattern)

### Pulumi Output[T]
- Resource properties return `pulumi.Output[T]` values, not plain values
- Use `.ApplyT(func(...) ...)` to transform an output
- Combine multiple outputs with `pulumi.All(a, b).ApplyT(func(args []interface{}) ... )`

### Step Execution
Steps run sequentially via `ptd ensure`:
1. `bootstrap` (Go) → 2. `persistent` (Go) → 3. `postgres_config` (Go) → 4. `eks`/`aks` → 5. `clusters` → 6. `helm` → 7. `sites` → 8. `persistent_reprise` (Go)

Each step produces outputs consumed by later steps. See `docs/architecture/step-dependencies.md`.

## Pulumi Step Development

### Testing
- Run library tests with `just test-lib` and CLI tests with `just test-cmd`
- Step logic lives in `lib/steps`; pure helpers (naming, config parsing, metric-filter extraction) have unit tests alongside them

### Adding a New Pulumi Step
1. Add the step's inline-Go Pulumi program in `lib/steps/` (a function that builds resources on a `*pulumi.Context`)
2. Wire it through the appropriate cloud-specific deploy function
3. Register the step in `WorkloadSteps` or `ControlRoomSteps` in `lib/steps/steps.go`

### Large Files (>1000 lines)
These files are large and require careful context management:

**Go (lib/steps):**
- `helm_aws.go` (~1550 lines) — AWS helm deployments (LBC, Traefik, Loki, Mimir, Grafana, Alloy, Karpenter, etc.)
- `clusters_aws.go` (~1300 lines) — AWS cluster bootstrapping (IAM, Team Operator, HelmController, Karpenter, etc.)
- `helm_azure.go` (~1100 lines) — Azure helm deployments (ExternalDNS, Loki, Mimir, Grafana, Alloy, managed identities)
- `helm_helpers.go` (~880 lines) — Shared Alloy River config generator (`buildAlloyConfig`)

**Go (lib/aws):**
- `eks_cluster.go` (~1340 lines) — `EKSCluster` builder: control plane, IAM/OIDC, node groups, CSI drivers, storage classes (shared by the `eks` and `cluster` steps)
- `eks_cluster_cr.go` (~1180 lines) — control-room `EKSCluster` builder extensions (control-room node group, LBC, Grafana/Mimir/dashboards, Traefik forward-auth)
- `vpc.go` (~1148 lines) — `aws.NewVPC` builder (subnets, NAT, NACLs, flow logs, endpoints, existing-VPC adoption)
