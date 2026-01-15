# PTD Documentation

Welcome to the Posit Team Dedicated (PTD) documentation.

## Quick Links

### For Developers

- [PTD CLI Reference](cli/PTD_CLI_REFERENCE.md) - Complete CLI command documentation
- [Adding Config Options](guides/adding-config-options.md) - How to add new configuration options to Team Operator

### For Operators

- [Site Management Guide](guides/product-team-site-management.md) - Managing Posit Team sites
- [Team Operator Overview](team-operator/README.md) - Understanding the Kubernetes operator

### Infrastructure

- [Kubernetes Guide](infrastructure/kubernetes.md) - Kubernetes-specific documentation

## Documentation Structure

```
docs/
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
