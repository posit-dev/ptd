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

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for contribution guidelines.

## License

See [LICENSE](LICENSE) for license information.
