# PTD (Posit Team Dedicated)

Posit Team Dedicated is a toolkit for deploying and managing Posit Team products (Workbench, Connect, Package Manager) on cloud infrastructure using Infrastructure-as-Code.

## Overview

PTD provides:
- **CLI tool** (`ptd`) for managing deployments
- **Pulumi IaC** for provisioning AWS and Azure infrastructure
- **Example configurations** for quick setup

## Quick Start

### Prerequisites

- [Go](https://golang.org/dl/) 1.21+
- [Python](https://www.python.org/downloads/) 3.12+
- [uv](https://github.com/astral-sh/uv) (Python package manager)
- [Pulumi](https://www.pulumi.com/docs/get-started/install/)
- [just](https://github.com/casey/just) (command runner)
- AWS CLI or Azure CLI (depending on your cloud provider)

### Installation

```bash
# Clone the repository
git clone https://github.com/posit-dev/ptd.git
cd ptd

# Install dependencies
just deps

# Build the CLI
just build-cmd

# The CLI is now available at .local/bin/ptd
```

### Configuration

1. Copy the example account configuration:
   ```bash
   cp accounts.env.example accounts.env
   ```

2. Edit `accounts.env` with your AWS account IDs (optional - PTD auto-detects via STS)

3. Set up your targets directory with control room and workload configurations.
   See [examples/](examples/) for starter configurations.

### Usage

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

## Development

```bash
# Run tests
just test

# Run specific test suites
just test-cmd        # CLI tests
just test-lib        # Library tests
just test-python-pulumi  # Python tests

# Format code
just format

# Build CLI
just build-cmd
```

## Related Projects

- [Team Operator](https://github.com/posit-dev/team-operator) - Kubernetes operator for Posit Team products

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for contribution guidelines.

## License

See [LICENSE](LICENSE) for license information.
