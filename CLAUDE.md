# Posit Team Dedicated (PTD)

## Project Structure

The project is organized into several key components:

- **`./cmd`**: Contains the main CLI tool (Go implementation)
- **`./lib`**: Common Go libraries and utilities
- **`./python-pulumi`**: Python package with Pulumi infrastructure-as-code resources
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

### Go→Python Integration

The Go CLI communicates the infrastructure path to Python Pulumi stacks via the `PTD_ROOT` environment variable:
- **Go**: Sets `PTD_ROOT` in `lib/pulumi/python.go` when invoking Python
- **Python**: Reads `PTD_ROOT` in `python-pulumi/src/ptd/paths.py`
- **Tests**: Python tests must set `PTD_ROOT` via `monkeypatch.setenv()`

## Build and Development Commands

### Overall Project Commands (from root Justfile)

- `just deps`: Install dependencies
- `just check`: Check all (includes linting and formatting)
- `just test`: Test all
- `just build`: Build all
- `just format`: Run automatic formatting

#### Check Commands

- `just check-python-pulumi`: Check Python Pulumi code

#### Build Commands

- `just build-cmd`: Build command-line tool

#### Test Commands

- `just test-cmd`: Test command-line tool
- `just test-e2e`: Run end-to-end tests (requires URL argument)
- `just test-lib`: Test library code
- `just test-python-pulumi`: Test Python Pulumi code

#### AWS Development

- `just aws-unset`: Unset all AWS environment variables

## Git Worktrees

**Always use git worktrees instead of plain branches.** This enables concurrent Claude sessions in the same repo.

### Creating a Worktree

This repo is expected to live at `ptd-workspace/ptd/`. The `../../.worktrees/` relative path resolves to `ptd-workspace/.worktrees/` in that layout.

```bash
# New branch
git worktree add ../../.worktrees/ptd-<branch-name> -b <branch-name>

# Existing remote branch
git worktree add ../../.worktrees/ptd-<branch-name> <branch-name>
```

Always prefix worktree directories with `ptd-` to avoid collisions with other repos.

### After Creating a Worktree

1. **Build the binary** — each worktree needs its own ptd binary:
   ```bash
   cd ../../.worktrees/ptd-<branch-name>
   just build-cmd
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
git worktree remove ../../.worktrees/ptd-<branch-name>
```

### Rules

- **NEVER** use `git checkout -b` for new work — always `git worktree add`
- **NEVER** put worktrees inside the repo directory — always use `../../.worktrees/ptd-<name>`
- **ALWAYS** rebuild the binary after creating a worktree (`just build-cmd`)
- Branch names: kebab-case, no slashes, no usernames (slashes break worktree directory paths)

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
- **Config Flow**: How YAML config flows through Go to Python → See `docs/architecture/config-flow.md`
- **Step Dependencies**: Deployment pipeline ordering and why → See `docs/architecture/step-dependencies.md`
- **Pulumi Conventions**: Resource naming, Output handling, autoload pattern → See `docs/architecture/pulumi-conventions.md`

## Danger Zones

### Pulumi Resource Names
- **NEVER** change the first argument (logical name) to a Pulumi resource constructor without understanding state implications
- Changing `aws.s3.Bucket("my-bucket-name", ...)` to `aws.s3.Bucket("different-name", ...)` causes Pulumi to DELETE the old bucket and CREATE a new one
- This applies to ALL resources: VPCs, RDS instances, S3 buckets, IAM roles, EKS clusters, etc.
- If you need to rename a resource, discuss the state migration strategy first

### Config Changes Require Both Languages
- Adding/modifying a config option requires changes in BOTH:
  - **Go**: Struct in `lib/types/workload.go` (with YAML struct tags)
  - **Python**: Dataclass in `python-pulumi/src/ptd/aws_workload.py` or `python-pulumi/src/ptd/__init__.py`
- Field names must match: Go YAML tags (snake_case) = Python dataclass field names
- There is no automated validation between the two — mismatches fail at runtime

### Builder Method Ordering
- `AWSEKSCluster` uses a builder pattern where `with_*()` methods have ordering dependencies
- Example: `with_node_role()` MUST be called before `with_node_group()` (sets `self.default_node_role`)
- Check method dependencies before reordering calls

### Resource Naming Conventions

**AWS:**
- IAM roles: `f"{purpose}.{compound_name}.posit.team"`
- S3 buckets: `f"{compound_name}-{purpose}"`
- EKS clusters: `f"default_{compound_name}-control-plane"`
- All naming methods are on `AWSWorkload` class in `python-pulumi/src/ptd/aws_workload.py`

**Azure:**
- Resource Groups: `f"rsg-ptd-{sanitized_name}"`
- Key Vault: `f"kv-ptd-{name[:17]}"` (max 24 chars)
- Storage Accounts: `f"stptd{name_no_hyphens[:19]}"` (NO hyphens, max 24 chars)
- VNets: `f"vnet-ptd-{compound_name}"`
- All naming methods are on `AzureWorkload` class in `python-pulumi/src/ptd/azure_workload.py`
- Azure tags must use `azure_tag_key_format()` which converts `.` to `/`

Do NOT introduce new naming patterns — follow existing conventions

## Key Patterns

### The autoload Pattern
Go generates `__main__.py` dynamically (see `lib/pulumi/python.go:WriteMainPy`):
```python
import ptd.pulumi_resources.<module>
ptd.pulumi_resources.<module>.<Class>.autoload()
```
- Module: `{cloud}_{target_type}_{step_name}` (e.g., `aws_workload_persistent`)
- Class: `{Cloud}{TargetType}{StepName}` (e.g., `AWSWorkloadPersistent`)
- `__main__.py` is NOT in source control — it's generated at runtime

### AWS vs Azure Infrastructure Patterns

**AWS (EKS):**
- Uses builder pattern with `with_*()` methods
- Builder methods have ordering dependencies (e.g., `with_node_role()` must come before `with_node_group()`)
- EKS step is Python-based (`AWSEKSCluster` class)

**Azure (AKS):**
- AKS step is Go-based (`lib/steps/aks.go`) unlike the Python-based EKS step
- Azure persistent resources use simple `_define_*()` methods (no builder pattern)
- No ordering dependencies between `_define_*()` methods

### Pulumi Output[T]
- Resource properties return `Output[T]`, not plain values
- Use `.apply(lambda x: ...)` to transform; cannot use in f-strings directly
- Combine with `pulumi.Output.all(a, b).apply(lambda args: ...)`

### Step Execution
Steps run sequentially via `ptd ensure`:
1. `bootstrap` (Go) → 2. `persistent` (Python) → 3. `postgres_config` (Python) → 4. `eks`/`aks` → 5. `clusters` → 6. `helm` → 7. `sites` → 8. `persistent_reprise` (Go)

Each step produces outputs consumed by later steps. See `docs/architecture/step-dependencies.md`.

## Python Pulumi Development

### Testing
- Use `pulumi.runtime.set_mocks()` for Pulumi resource tests
- For Go→Python integration details, see the "Go→Python Integration" section above
- Tests must set `PTD_ROOT` via `monkeypatch.setenv("PTD_ROOT", ...)`
- See `python-pulumi/tests/` for examples
- Run: `just test-python-pulumi`

### Adding a New Pulumi Resource Module
1. Create `python-pulumi/src/ptd/pulumi_resources/<cloud>_<target_type>_<step_name>.py`
2. Define a class inheriting from `pulumi.ComponentResource`
3. Implement `@classmethod autoload(cls)` that reads stack name and constructs workload
4. Add corresponding step in `lib/steps/`
5. Register step in `WorkloadSteps` or `ControlRoomSteps` in `lib/steps/steps.go`

### Large Files (>1000 lines)
These files are large and require careful context management:

**AWS:**
- `pulumi_resources/aws_eks_cluster.py` (~2580 lines) — EKS cluster provisioning with builder pattern
- `pulumi_resources/aws_workload_persistent.py` (~1454 lines) — VPC, RDS, S3, IAM
- `pulumi_resources/aws_workload_helm.py` (~1390 lines) — Helm chart deployments (AWS)
- `__init__.py` (~1275 lines) — Base types, constants, utility functions
- `aws_workload.py` (~815 lines) — AWS workload config and naming conventions

**Azure:**
- `pulumi_resources/azure_workload_persistent.py` (~817 lines) — VNet, Postgres, Storage, ACR
- `pulumi_resources/azure_workload_helm.py` (~675 lines) — Helm chart deployments (Azure)
- `azure_workload.py` (~398 lines) — Azure workload config and naming with strict char limits
