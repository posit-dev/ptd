# PTD Documentation

Welcome to the Posit Team Dedicated (PTD) documentation.

## Quick Links

### For Developers

- [PTD CLI Reference](cli/PTD_CLI_REFERENCE.md) - Complete CLI command documentation
- [Adding Config Options](guides/adding-config-options.md) - How to add new configuration options to Team Operator

### Architecture

- [Config Flow](architecture/config-flow.md) - How configuration flows from YAML through Go to Python
- [Step Dependencies](architecture/step-dependencies.md) - The step execution pipeline and dependencies
- [Pulumi Conventions](architecture/pulumi-conventions.md) - Pulumi-specific patterns and resource naming

### For Operators

- [Team Operator Overview](team-operator/README.md) - Understanding the Kubernetes operator
- [Monitoring](guides/monitoring.md) - The Grafana monitoring stack

### Infrastructure

- [Kubernetes Guide](infrastructure/kubernetes.md) - Kubernetes-specific documentation

### Misc
- [Known Issues](KNOWN_ISSUES.md) - Known issues and rough edges

## Documentation Structure

```
docs/
├── architecture/           # Architecture documentation
├── cli/                    # PTD CLI documentation
├── team-operator/          # Team Operator documentation
├── guides/                 # How-to guides
└── infrastructure/         # Infrastructure documentation
```

## Contributing

When adding new features:
1. Update relevant documentation in this structure
2. Use the `/update-docs` skill to identify which docs need updating
3. Keep examples up-to-date and runnable
