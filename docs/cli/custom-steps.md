# Custom Steps Guide

## Overview

PTD Custom Steps allow you to extend the standard provisioning pipeline with custom Go-based Pulumi programs. Custom steps are first-class citizens that integrate seamlessly with the standard steps in the PTD deployment workflow.

## Key Features

- **Go-only**: Custom steps must be written in Go for type safety and consistency
- **Isolated dependencies**: Each custom step has its own `go.mod` file
- **Remote state**: Custom steps store state in the same remote backend as standard steps
- **Flexible insertion**: Insert custom steps at any point in the deployment sequence
- **CLI integration**: Custom steps work with all PTD CLI flags (`--only-steps`, `--start-at-step`, etc.)

## Quick Start

### 1. Create the customizations directory

```bash
cd infra/__work__/your-workload
mkdir -p customizations
```

### 2. Create your first custom step

```bash
cd customizations
mkdir my-custom-step
cd my-custom-step
```

Create `main.go`:

```go
package main

import (
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/s3"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		// Create your custom resources here
		bucket, err := s3.NewBucketV2(ctx, "my-custom-bucket", &s3.BucketV2Args{
			Tags: pulumi.StringMap{
				"Custom": pulumi.String("true"),
			},
		})
		if err != nil {
			return err
		}

		ctx.Export("bucketName", bucket.ID())
		return nil
	})
}
```

Initialize the Go module:

```bash
go mod init github.com/<org>/<project>/<workload>/my-custom-step
go get github.com/pulumi/pulumi/sdk/v3@latest
go get github.com/pulumi/pulumi-aws/sdk/v6@latest
go mod tidy
```

### 3. Create the manifest

Create `customizations/manifest.yaml`:

```yaml
version: 1
customSteps:
  - name: my-custom-step
    description: "My first custom step"
    path: my-custom-step/
    insertAfter: persistent
    proxyRequired: false
```

### 4. Deploy

```bash
cd /path/to/ptd
ptd ensure your-workload
```

### Project Structure Limitations

Custom steps must have all Go source files in the root directory. Subdirectories with additional Go packages are not currently supported.

## Manifest Reference

### Top-level fields

```yaml
version: 1           # Required: Manifest schema version (currently only 1 is supported)

customSteps:         # Required: List of custom steps
  - name: ...        # Step configuration (see below)
```

### Custom step fields

```yaml
- name: my-step              # Required: Unique step name (used in CLI commands)
  description: "..."         # Optional: Human-readable description
  path: my-step/             # Required: Path to step directory (relative to customizations/)

  # Insertion point (choose one or both)
  insertAfter: persistent    # Optional: Insert after this standard step
  insertBefore: eks          # Optional: Insert before this standard step

  proxyRequired: false       # Optional: Whether this step needs cluster proxy access (default: false)
  enabled: true              # Optional: Enable/disable this step (default: true)
```

### Insertion points

See [steps.go](lib/steps/steps.go) for the current enumeration of standard steps for both workloads and control rooms.

You can insert custom steps:
- After any step: `insertAfter: persistent`
- Before any step: `insertBefore: eks`
- At the end: omit both `insertAfter` and `insertBefore`

## Dependency Management

### Module structure

Each custom step is a **completely independent Go module** with its own dependencies:

```
customizations/
├── manifest.yaml
├── my-step/
│   ├── main.go          # Pulumi program
│   ├── go.mod           # Independent module
│   └── go.sum           # Dependency lock
└── another-step/
    ├── main.go
    ├── go.mod
    └── go.sum
```

### Initializing a new custom step

```bash
cd customizations/my-step
go mod init github.com/<project>/<org>/<workload>/my-step

# Add Pulumi dependencies
go get github.com/pulumi/pulumi/sdk/v3@latest

# Add provider dependencies as needed
go get github.com/pulumi/pulumi-aws/sdk/v6@latest
go get github.com/pulumi/pulumi-kubernetes/sdk/v4@latest

# Lock dependencies
go mod tidy
```

### Version independence

Different custom steps can use different versions of the same provider:

```go
// customizations/step-a/go.mod
require github.com/pulumi/pulumi-aws/sdk/v6 v6.50.0

// customizations/step-b/go.mod
require github.com/pulumi/pulumi-aws/sdk/v6 v6.65.0  // Newer version!
```

### Using the PTD Library

Custom steps can access useful functions and types from the PTD library, which is published as an open source package (alternatively if you have the CLI project locally, you can use a relative path in your `go.mod` file):

```bash
cd customizations/my-step

# Add the PTD library as a dependency
go get github.com/posit-dev/ptd/lib@latest

go mod tidy
```

Your `go.mod` will include:

```go
require (
    github.com/posit-dev/ptd/lib v0.0.0-20260127184423-6453cc65f826
)
```

To reference your local copy of the CLI project
```
require (
    github.com/posit-dev/ptd/lib v0.0.0-00010101000000-000000000000
)

replace github.com/posit-dev/ptd/lib => {relative path to the /lib directory}
```

#### Available Library Packages

The PTD library provides several useful packages:

- **`github.com/posit-dev/ptd/lib/helpers`**: Utility functions including:
  - `LoadPtdYaml(path string)`: Load and parse the ptd.yaml configuration file

- **`github.com/posit-dev/ptd/lib/types`**: Configuration types including:
  - `AWSWorkloadConfig`: AWS workload configuration with resource tags
  - `AzureWorkloadConfig`: Azure workload configuration with resource tags

#### Example: Loading PTD Configuration

```go
package main

import (
	"fmt"

	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/s3"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"

	"github.com/posit-dev/ptd/lib/consts"
	"github.com/posit-dev/ptd/lib/helpers"
	"github.com/posit-dev/ptd/lib/types"
)

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		// Get PTD config path
		ptdCfg := config.New(ctx, "ptd")
		yamlPath := ptdCfg.Require("ptdYamlPath")

		// Load the ptd.yaml configuration
		workloadCfg, err := helpers.LoadPtdYaml(yamlPath)
		if err != nil {
			return fmt.Errorf("loading ptd.yaml: %w", err)
		}

		// Type assert to the appropriate workload config
		workload, ok := workloadCfg.(types.AWSWorkloadConfig)
		if !ok {
			return fmt.Errorf("invalid workload config type")
		}

		// Access resource tags directly from the config
		requiredTags := pulumi.StringMap{}
		for k, v := range workload.ResourceTags {
			requiredTags[k] = pulumi.String(v)
		}
		requiredTags[consts.POSIT_TEAM_MANAGED_BY_TAG] = pulumi.String(ctx.Project())

		// Create resources with the tags
		bucket, err := s3.NewBucketV2(ctx, "my-bucket", &s3.BucketV2Args{
			Tags: requiredTags,
		})
		if err != nil {
			return err
		}

		ctx.Export("bucketName", bucket.ID())
		return nil
	})
}
```

## CLI Usage

Custom steps function as any standard step for all supported `ensure` parameters (e.g. `--only-steps`, `--dry-run`, etc...)

### List all steps (including custom steps)

```bash
ptd ensure your-workload --list-steps
```

Output:
```
Available steps:

Standard Steps:
  1. bootstrap
  2. persistent
  3. postgres_config
  ...

Custom Steps:
  11. my-custom-step [CUSTOM]
      My first custom step
  12. another-custom-step [CUSTOM]
      Another custom resource deployment

Total: 12 steps (10 standard, 2 custom)
```

## Remote State Management

Custom steps store their Pulumi state in the **same remote backend** as standard steps which are backed by Pulumi stacks.

### Benefits

- **Team collaboration**: Multiple developers can work with the same custom steps
- **Consistency**: State is backed up and versioned
- **Security**: State is encrypted using the same KMS/KeyVault keys

### Available Config Values

```go
ptdCfg := config.New(ctx, "ptd")

// Workload name
workloadName := cfg.Get("workloadName")

// AWS config
awsCfg := config.New(ctx, "aws")
region := awsCfg.Require("region")
```

## Best Practices

### 1. Use descriptive names

```yaml
# Good
- name: monitoring-dashboards
  description: "Grafana dashboards for application metrics"

# Bad
- name: step1
  description: "stuff"
```

### 2. Pin dependency versions

```go
// go.mod - pin specific versions
require (
	github.com/pulumi/pulumi-aws/sdk/v6 v6.65.0
	github.com/pulumi/pulumi/sdk/v3 v3.100.0
)
```

### 3. Document special requirements

```go
// main.go
// IMPORTANT: Requires pulumi-aws v6.60.0+ for the new S3 bucket encryption defaults
```

### 4. Test custom steps in isolation

```bash
# Test only your custom step
ptd ensure your-workload --only-steps my-custom-step --dry-run
ptd ensure your-workload --only-steps my-custom-step
```

### 5. Keep custom steps focused

Each custom step should have a single, clear purpose. Don't create monolithic custom steps that do too many things.

### 6. Version control everything

Always commit `go.mod`, `go.sum`, and the manifest to git:

```bash
git add customizations/
git commit -m "feat: add custom monitoring step"
```

## Troubleshooting

### Build failures

If your custom step fails to build:

```bash
# Build manually to see full error output
cd infra/__work__/your-workload/customizations/my-step
go build .
```

### Manifest validation errors

Common issues:

1. **Invalid insertion point**: `insertAfter` or `insertBefore` references a non-existent step
2. **Duplicate step names**: Two custom steps have the same name
3. **Missing files**: `main.go` or `go.mod` not found

### State issues

If you encounter state issues:

```bash
# Refresh the custom step's state
ptd ensure your-workload --only-steps my-custom-step --refresh
```

### Permission issues

Custom steps use the same credentials as standard steps. If you get permission errors:

1. Check the workload's IAM role/managed identity has required permissions
2. Verify the custom step's resources don't conflict with existing policies

## Examples

See the `examples/custom-steps/` directory for complete working examples:

- `simple-s3-bucket/` - Basic S3 bucket creation
- `manifest.yaml` - Annotated example manifest
