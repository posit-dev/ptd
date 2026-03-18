# PTD (Posit Team Dedicated)

Posit Team Dedicated is a toolkit for deploying and managing Posit Team products (Workbench, Connect, Package Manager) on cloud infrastructure using Infrastructure-as-Code.

## Overview

PTD provides:
- **CLI tool** (`ptd`) for managing deployments
- **Pulumi IaC** for provisioning AWS and Azure infrastructure
- **Example configurations** for quick setup

## Installation

Download the latest release from the [releases page](https://github.com/posit-dev/ptd/releases), or see [CONTRIBUTING.md](CONTRIBUTING.md) to build from source.

## Usage

```bash
# Deploy a workload
ptd ensure my-workload

# Open a proxy to a cluster
ptd proxy my-workload

# Check available commands
ptd --help
```

## Documentation

- [Getting Started Guide](docs/GETTING_STARTED.md) - Detailed setup instructions
- [Configuration Reference](docs/CONFIGURATION.md) - Configuration options
- [CLI Reference](docs/cli/PTD_CLI_REFERENCE.md) - Complete CLI documentation
- [Examples](examples/) - Example configurations

## Project Structure

```
ptd/
├── cmd/           # Go CLI implementation
├── lib/           # Shared Go libraries
├── python-pulumi/ # Pulumi IaC resources (Python)
├── examples/      # Example configurations
├── e2e/           # End-to-end tests
└── docs/          # Documentation
```

## Related Projects

- [Team Operator](https://github.com/posit-dev/team-operator) - Kubernetes operator for Posit Team products

## Automated Version Updates

PTD automatically receives version updates from the [Team Operator](https://github.com/posit-dev/team-operator) repository when new releases are published.

### How It Works

1. Team Operator's release workflow triggers `.github/workflows/update-team-operator-version.yml`
2. The workflow verifies the Helm chart exists at `oci://ghcr.io/posit-dev/charts/team-operator`
3. Updates `DEFAULT_CHART_VERSION` in `python-pulumi/src/ptd/pulumi_resources/team_operator.py`
4. Creates a PR for review

### Manual Trigger

You can also trigger the workflow manually:

```bash
gh workflow run update-team-operator-version.yml --field version=v1.16.2
```

Or via the Actions UI with the `workflow_dispatch` trigger.

### Authentication

The incoming dispatch is authenticated via a PAT stored in the Team Operator repository (`PTD_REPO_TOKEN` secret). See the [Team Operator README](https://github.com/posit-dev/team-operator#release-automation) for token management details.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for contribution guidelines.

## License

See [LICENSE](LICENSE) for license information.
