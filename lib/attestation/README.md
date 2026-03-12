# Attestation Package

The attestation package provides functionality to collect comprehensive metadata about PTD workload deployments for auditing, compliance, and operational visibility.

## Overview

This package collects deployment information from multiple sources:

- **Site Configurations**: Product images, replicas, domains, and authentication settings from `site.yaml` files
- **Pulumi Stack State**: Resource counts, types, versions, and timestamps from Pulumi state files in S3
- **Custom Steps**: Custom deployment steps from `customizations/manifest.yaml`
- **Cluster Configuration**: Kubernetes cluster settings from `ptd.yaml`

## Usage

```go
package main

import (
    "context"
    "encoding/json"
    "fmt"

    "github.com/posit-dev/ptd/lib/attestation"
    "github.com/posit-dev/ptd/lib/types"
)

func main() {
    ctx := context.Background()

    // target is a types.Target (AWS or Azure)
    // workloadPath is the path to the workload directory (e.g., "infra/__work__/workload-name")

    data, err := attestation.Collect(ctx, target, workloadPath)
    if err != nil {
        panic(err)
    }

    // Serialize to JSON
    jsonData, err := json.MarshalIndent(data, "", "  ")
    if err != nil {
        panic(err)
    }

    fmt.Println(string(jsonData))
}
```

## Data Structure

The `AttestationData` struct contains:

- `TargetName`: Name of the deployment target
- `CloudProvider`: Cloud provider (aws/azure)
- `Region`: Cloud region
- `AccountID`: Cloud account/subscription ID
- `GeneratedAt`: Timestamp when attestation was collected
- `Sites`: Array of site configurations with product details
- `Stacks`: Array of Pulumi stack summaries with resource information
- `CustomSteps`: Array of custom deployment steps
- `ClusterConfig`: Cluster configuration from ptd.yaml

## Current Limitations

- **AWS Only**: Currently supports AWS workloads. Azure support planned for future releases.
- **S3 State Backend**: Assumes Pulumi state is stored in S3 (the standard PTD configuration).
- **Read-Only**: This package only reads deployment state; it does not modify any infrastructure.

## Thread Safety

The `Collect` function processes Pulumi state files in parallel using goroutines for improved performance when scanning large deployments with many stacks.
