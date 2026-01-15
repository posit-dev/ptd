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
- `just latest-images`: Show latest ECR images

## Contributing

When contributing to the project:

1. Ensure that Snyk tests pass before merging a PR
2. Follow the development workflows described in the repository files
3. Use the provided Justfiles for common tasks
4. Always run `just format` before committing changes to ensure code style consistency

# Additional Instructions
- LLM coding instructions shared with copilot: [.github/copilot/copilot-instructions.md](.github/copilot/copilot-instructions.md)
- Follow the template in [.github/pull_request_template.md](.github/pull_request_template.md) to format PR descriptions correctly
