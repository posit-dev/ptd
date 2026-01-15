# PTD Examples

This directory contains example configurations for PTD deployments.

## Examples

| Directory | Description |
|-----------|-------------|
| [control-room/](control-room/) | Control Room configuration - central management hub |
| [workload/](workload/) | Workload configuration with a Site |
| [custom-steps/](custom-steps/) | Custom Pulumi steps for extending PTD |

## Quick Start

### 1. Set up a Control Room

```bash
# Copy the example
cp -r examples/control-room infra/__ctrl__/my-control-room

# Edit the configuration
$EDITOR infra/__ctrl__/my-control-room/ptd.yaml

# Deploy
ptd ensure my-control-room
```

### 2. Set up a Workload

```bash
# Copy the example
cp -r examples/workload infra/__work__/my-workload

# Edit the configurations
$EDITOR infra/__work__/my-workload/ptd.yaml
$EDITOR infra/__work__/my-workload/site_main/site.yaml

# Deploy
ptd ensure my-workload
```

### 3. Add Custom Steps (Optional)

```bash
# Copy the custom steps example
cp -r examples/custom-steps/simple-s3-bucket infra/__work__/my-workload/customizations/

# Create/edit the manifest
$EDITOR infra/__work__/my-workload/customizations/manifest.yaml

# Deploy with custom steps
ptd ensure my-workload
```

## Architecture Overview

```
PTD Deployment
├── Control Room (1 per organization)
│   └── Manages DNS, authentication, coordination
│
└── Workloads (1+ per organization)
    ├── AWS Infrastructure (VPC, EKS, RDS, FSx)
    └── Sites (1+ per workload)
        ├── Workbench - Interactive development
        ├── Connect - Publishing platform
        └── Package Manager - Package repository
```

## Documentation

- [CLI Reference](../docs/cli/PTD_CLI_REFERENCE.md)
- [Getting Started](../docs/GETTING_STARTED.md)
- [Configuration Guide](../docs/CONFIGURATION.md)
