# Team Operator

The Team Operator is a Kubernetes operator that manages the deployment and configuration of Posit Team products within a Kubernetes cluster.

## Overview

The Team Operator automates the deployment and lifecycle management of:
- **Posit Workbench** - Interactive development environment
- **Posit Connect** - Publishing and sharing platform
- **Posit Package Manager** - Package repository management
- **Posit Chronicle** - Telemetry and monitoring
- **Keycloak** - Authentication and identity management

## Architecture

The operator uses a hierarchical configuration model:

```
Site CRD (single source of truth)
    ├── Connect configuration
    ├── Workbench configuration
    ├── Package Manager configuration
    ├── Chronicle configuration
    └── Keycloak configuration
```

The Site controller watches for Site resources and reconciles product-specific Custom Resources for each enabled product.

## Key Concepts

### Site CRD

The `Site` Custom Resource is the primary configuration point. It contains:
- Global settings (domain, secrets, storage)
- Product-specific configuration sections
- Feature flags and experimental options

### Configuration Propagation

Configuration flows from Site CRD to individual product CRDs:

1. User edits Site spec
2. Site controller detects change
3. Site controller updates product CRs
4. Product controllers reconcile deployments

See [Adding Config Options](../guides/adding-config-options.md) for details on extending configuration.

## Quick Start

### View Sites

```bash
kubectl get sites -n posit-team
```

### Edit a Site

```bash
kubectl edit site main -n posit-team
```

### Check Operator Logs

```bash
kubectl logs -n posit-team deploy/team-operator
```

## Session Label Injection

The session label controller watches Workbench session pods and injects numbered labels from a configurable pod field. Labels can be consumed by any downstream tooling that reads pod metadata.

### How it works

1. When a session pod starts, the controller reads the field specified by `sourceField` (default: `spec.containers[0].args`).
2. It extracts the value at `sourceKey` from that field (default: the `--container-user-groups` flag value).
3. Each comma-separated entry is matched against `searchRegex`. Non-matching entries are skipped.
4. Matching entries are sanitized to valid Kubernetes label values and written as `user-group-1`, `user-group-2`, etc.
5. A `posit.co/session-group-labels-injected: "true"` marker is set on the pod so it is never processed twice.

### Enabling

Add a `sessionLabels` block to `workbench:` in `site.yaml`. Its presence enables the feature for that site; omitting it disables it. PTD automatically enables the controller in the Helm chart when any site in the workload has this block configured.

```yaml
# site.yaml
spec:
  workbench:
    sessionLabels:
      sourceField: "spec.containers[0].args"
      sourceKey: "--container-user-groups"
      searchRegex: "_entra_[^ ,]+"
```

All fields are optional — defaults cover the standard Workbench + Entra ID setup. See [Configuration Reference](../CONFIGURATION.md) for the full schema.

### Reprocessing existing pods

By default, already-processed pods are skipped (the marker label prevents re-reconciliation). To force re-labeling of existing session pods — e.g. after changing `searchRegex` or `trimPrefix` — set `reprocess: true`:

```yaml
spec:
  workbench:
    sessionLabels:
      reprocess: true
```

The controller will re-enqueue all existing session pods for the site immediately, clearing stale labels before applying the new set. Set back to `false` (or omit it) once done.

### Result

A session pod will have labels added for each matching entry found:

```
user-group-1: entra_research_team
user-group-2: entra_data_science
posit.co/session-group-labels-injected: "true"
```

### Custom sources

`sourceField` accepts any dot-path into the pod spec, including array index notation:

| Source | `sourceField` | `sourceKey` |
|---|---|---|
| Container args (default) | `spec.containers[0].args` | `--container-user-groups` |
| Pod annotation | `metadata.annotations` | `posit.co/user-groups` |
| Pod label | `metadata.labels` | `posit.co/group` |

## Related Documentation

- [Site Management Guide](../guides/product-team-site-management.md) - For product teams
- [Adding Config Options](../guides/adding-config-options.md) - For contributors
