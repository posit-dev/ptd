# Adding Configuration Options to Team Operator

This guide explains how to add new configuration options to Posit Team products via the Team Operator.

## Overview

Configuration options in Team Operator follow a specific propagation pattern:

```
site_types.go (SiteSpec)
    → InternalProductSpec (Site-level config)
        → site_controller_{product}.go (Propagation logic)
            → Product CR (using product's expected config path)
```

## Before You Start

You need to know:

1. **Product**: Which product does this config affect? (Connect, Workbench, Package Manager, Chronicle, Keycloak)
2. **Site Field Name**: The Go-style field name for the Site CRD (e.g., `MaxConnections`)
3. **Product Config Name**: The actual config key the product expects - check product documentation as these are often inconsistent with Site naming
4. **Type**: Go type (string, int, bool, *int, struct)
5. **Description**: What does this config control?
6. **Default**: What's the default value?

## Step-by-Step Guide

### Step 1: Add to Site Spec

Edit `team-operator/api/core/v1beta1/site_types.go`

Find the relevant `Internal{Product}Spec` struct and add your field:

```go
// MaxConnections sets the maximum number of connections
// Maps to product config: Scheduler.MaxConnections
// +optional
MaxConnections *int `json:"maxConnections,omitempty"`
```

### Step 2: Add Propagation Logic

Edit `team-operator/internal/controller/core/site_controller_{product}.go`

In the reconcile function, add the mapping from Site field to product config:

```go
// Propagate MaxConnections
// Site: MaxConnections → Product: Scheduler.MaxConnections
if site.Spec.Connect.MaxConnections != nil {
    targetConnect.Spec.Config.Scheduler.MaxConnections = *site.Spec.Connect.MaxConnections
}
```

### Step 3: Add Tests

Edit `team-operator/internal/controller/core/site_controller_{product}_test.go`

```go
It("should propagate MaxConnections to Connect Scheduler config", func() {
    maxConn := 100
    site := &v1beta1.Site{
        Spec: v1beta1.SiteSpec{
            Connect: v1beta1.InternalConnectSpec{
                MaxConnections: &maxConn,
            },
        },
    }

    result := reconcileConnect(site)

    Expect(result.Spec.Config.Scheduler.MaxConnections).To(Equal(100))
})
```

### Step 4: Update Documentation

Update `docs/guides/product-team-site-management.md` to include the new field in the example Site spec.

## Common Patterns

### Optional Integer (pointer)

```go
// MaxConnections sets the maximum number of connections
// +optional
MaxConnections *int `json:"maxConnections,omitempty"`
```

Propagation:
```go
if site.Spec.Product.MaxConnections != nil {
    target.Spec.Config.Path = *site.Spec.Product.MaxConnections
}
```

### Enum (string with validation)

```go
// LogLevel sets the logging verbosity
// +kubebuilder:validation:Enum=debug;info;warn;error
// +optional
LogLevel string `json:"logLevel,omitempty"`
```

### Nested Struct

```go
// GPUSettings configures GPU resources
// +optional
GPUSettings *GPUSettings `json:"gpuSettings,omitempty"`

type GPUSettings struct {
    // NvidiaGPULimit sets the GPU limit
    // +optional
    NvidiaGPULimit int `json:"nvidiaGpuLimit,omitempty"`
}
```

### Boolean

```go
// EnableFeatureX enables the experimental feature X
// +optional
EnableFeatureX bool `json:"enableFeatureX,omitempty"`
```

Note: With `omitempty`, false values are omitted. Only propagate when explicitly true.

## Sensible Defaults Pattern

Many configuration values have sensible defaults that users shouldn't need to specify. The Team Operator uses several patterns to handle these:

### Inline Hardcoded Defaults

Set defaults directly when constructing the target CR in the Site controller:

```go
// site_controller_connect.go
targetConnect := v1beta1.Connect{
    Spec: v1beta1.ConnectSpec{
        Config: v1beta1.ConnectConfig{
            Applications: &v1beta1.ConnectApplicationsConfig{
                BundleRetentionLimit:     2,      // Sensible default
                PythonEnvironmentReaping: true,   // Sensible default
            },
            Http: &v1beta1.ConnectHttpConfig{
                ForceSecure: true,                // Sensible default
                Listen:      ":3939",             // Sensible default
            },
        },
    },
}
```

**When to use**: Values that are always the same across deployments and don't need user customization.

### Computed Defaults

Derive values from other fields in the Site spec:

```go
// site_controller.go
connectUrl := prefixDomain(site.Spec.Connect.DomainPrefix, site.Spec.Domain, domainType)
packageManagerRepoUrl := fmt.Sprintf("https://%s/cran/__linux__/jammy/latest", packageManagerUrl)
```

**When to use**: Values that can be calculated from existing configuration.

### PassDefault Helper Functions

Use helper functions for values where zero-value means "use default":

```go
// api/product/util.go
func PassDefaultReplicas(replicas *int, def int) int {
    if replicas == nil {
        return def
    }
    return *replicas
}

// Usage in controller
targetWorkbench.Spec.Replicas = product.PassDefaultReplicas(site.Spec.Workbench.Replicas, 1)
```

**When to use**: Numeric fields where users might want to override the default.

### Pointer Types for Optional with Defaults

Use `*int` instead of `int` when you need to distinguish "not set" from "explicitly set to zero":

```go
// In types
Replicas *int `json:"replicas,omitempty"`

// In controller - nil means "use default", pointer to 0 means "scale to zero"
if site.Spec.Product.Replicas == nil {
    target.Spec.Replicas = 1  // default
} else {
    target.Spec.Replicas = *site.Spec.Product.Replicas  // could be 0
}
```

**When to use**: Integer fields where 0 is a valid explicit value (like replica counts).

## Shared Attributes Pattern

The Site controller acts as an orchestrator, computing shared values once and passing them to product reconcilers. This ensures consistency across products.

### Function Parameter Passing

Shared values are computed in `reconcileResources()` and passed to product reconcilers:

```go
// site_controller.go - reconcileResources()

// Compute shared values once
dbUrl, _ := internal.DetermineMainDatabaseUrl(ctx, r, req, ...)
packageManagerRepoUrl := fmt.Sprintf("https://%s/cran/__linux__/jammy/latest", packageManagerUrl)

// Pass to multiple product reconcilers
r.reconcileConnect(ctx, req, site, dbUrl.Host, sslMode, ..., packageManagerRepoUrl, connectUrl)
r.reconcileWorkbench(ctx, req, site, dbUrl.Host, sslMode, ..., packageManagerRepoUrl, workbenchUrl)
```

### Common Shared Values

| Value | Computed From | Used By |
|-------|---------------|---------|
| `dbUrl.Host` | Secrets lookup | Connect, Workbench, Package Manager, Keycloak |
| `sslMode` | Database URL query params | All products with DB |
| `packageManagerRepoUrl` | Package Manager domain | Connect, Workbench |
| `additionalVolumes` | Site shared directory config | Connect, Workbench |

### Adding a New Shared Value

1. **Compute in Site controller** (`reconcileResources()`):
   ```go
   mySharedValue := computeSharedValue(site)
   ```

2. **Add to product reconciler signatures**:
   ```go
   func (r *SiteReconciler) reconcileConnect(
       ctx context.Context,
       req controllerruntime.Request,
       site *v1beta1.Site,
       // ... existing params ...
       mySharedValue string,  // Add here
   ) error {
   ```

3. **Pass when calling reconcilers**:
   ```go
   r.reconcileConnect(ctx, req, site, ..., mySharedValue)
   r.reconcileWorkbench(ctx, req, site, ..., mySharedValue)
   ```

4. **Use in product CR construction**:
   ```go
   targetConnect := v1beta1.Connect{
       Spec: v1beta1.ConnectSpec{
           MyField: mySharedValue,
       },
   }
   ```

### Why Not Use Status Fields?

The Site controller could write computed values to `site.Status` for products to read, but the current pattern uses function parameters because:

- **Simplicity**: No additional reconcile cycles needed
- **Atomicity**: All products get the same value in a single reconcile
- **Testability**: Easy to test product reconcilers in isolation

## File Reference

| Product | Site Types | Controller |
|---------|------------|------------|
| Connect | `site_types.go` (InternalConnectSpec) | `site_controller_connect.go` |
| Workbench | `site_types.go` (InternalWorkbenchSpec) | `site_controller_workbench.go` |
| Package Manager | `site_types.go` (InternalPackageManagerSpec) | `site_controller_packagemanager.go` |
| Chronicle | `site_types.go` (InternalChronicleSpec) | `site_controller_chronicle.go` |
| Keycloak | `site_types.go` (InternalKeycloakSpec) | `site_controller_keycloak.go` |

## Finding Product Config Names

Product config paths often differ from Site field names. To find the correct path:

1. Check the product's admin guide or configuration reference
2. Look at existing examples in `site_controller_{product}.go`
3. Check the product's Helm chart `values.yaml`
4. Ask the product team if uncertain

## Validation Checklist

- [ ] Field has correct JSON tag (camelCase)
- [ ] Product config path matches product's expected configuration
- [ ] Field has kubebuilder validation if needed
- [ ] Default value is handled correctly
- [ ] Propagation respects zero values vs explicit values
- [ ] Test covers positive and negative cases
- [ ] Documentation updated with example

## Using Claude Code

If you're using Claude Code, invoke the `/team-operator-config` skill which will guide you through this process interactively.
