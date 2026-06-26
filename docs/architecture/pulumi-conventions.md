# Pulumi conventions and patterns

This document covers Pulumi-specific conventions that are critical for making correct changes to PTD infrastructure code without accidentally destroying resources.

All PTD infrastructure is defined in inline-Go Pulumi programs under `lib/steps` (with shared builders in `lib/aws` and `lib/azure`), compiled into the `ptd` binary.

## Table of contents
- [Resource naming (CRITICAL)](#resource-naming-critical)
- [Constructor patterns](#constructor-patterns)
- [Output[T] handling](#outputt-handling)

---

## Resource naming (CRITICAL)

### Logical names vs physical names

Pulumi resources have two kinds of names:

1. Logical name (first argument to resource constructor)
   - This is the key in Pulumi's state file
   - Changing it causes a DELETE + CREATE operation
   - Pulumi uses this to track resources across updates
   - Not visible in cloud console

2. Physical name (the `Name`/`Bucket`/etc. field in resource args)
   - This is the actual name in AWS/Azure/Kubernetes
   - Appears in cloud console, CLI output, etc.
   - Can sometimes be changed without destroying the resource (depends on cloud provider)

### Example

```go
// DON'T DO THIS (logical name depends on compoundName)
aws.NewBucket(ctx,
    fmt.Sprintf("%s-loki", compoundName), // if compoundName changes, the bucket is DESTROYED
    &aws.BucketArgs{Bucket: pulumi.String(fmt.Sprintf("%s-loki-logs", compoundName))},
)

// DO THIS (stable logical name)
aws.NewBucket(ctx,
    "loki-logs-bucket", // stable logical name
    &aws.BucketArgs{Bucket: pulumi.String(fmt.Sprintf("%s-loki-logs", compoundName))}, // physical name can change
)
```

**Why this matters:** Destroying and recreating resources like RDS databases, S3 buckets with data, or VPCs with running workloads is catastrophic.

---

### Resource naming patterns

PTD uses consistent naming patterns. The naming helpers are implemented in Go and produce these physical names verbatim for state adoption.

#### AWS naming patterns

##### IAM roles
**Pattern:** `{purpose}.{compound_name}.posit.team`

Example: `team-operator.myworkload-staging.posit.team`

**Usage locations:**
- `lib/aws` and `lib/steps` (e.g. IAM role names for EKS IAM Roles for Service Accounts (IRSA))

---

##### S3 buckets
**Pattern:** `{compound_name}-{purpose}`

Example: `myworkload-staging-loki`

**Usage locations:**
- `lib/steps/persistent_aws.go` (the persistent step)
- Loki logs, Mimir metrics, general storage buckets

---

##### EKS clusters
The EKS `aws.eks.Cluster` resource name (first arg / `name`) differs by target type:
- Workload: `{compound_name}-{release}`
- Control room: bare `{compound_name}`

**Do NOT** use `default_{compound_name}-control-plane` as the cluster resource name. That string is the kubeconfig *context* name, not the cluster's name; using it as the resource name would replace the live control plane.

**Usage locations:**
- `lib/aws/eks_cluster.go`, `lib/aws/eks_cluster_cr.go`, `lib/steps/eks_aws.go`, `lib/steps/cluster_aws.go`

---

##### Helm releases
**Pattern (logical name):** `{compound_name}-{release}-{component}`

```go
// Example from lib/steps/helm_aws.go
apiextensions.NewCustomResource(ctx,
    compoundName+"-"+release+"-aws-fsx-openzfs-csi-helm-release", // logical name
    &apiextensions.CustomResourceArgs{
        Metadata: metav1.ObjectMetaArgs{
            Name: pulumi.String("aws-fsx-openzfs-csi"), // physical name (what appears in K8s)
        },
        // ...
    })

// Logical name: "myworkload-staging-r1-aws-fsx-openzfs-csi-helm-release"
// Physical name: "aws-fsx-openzfs-csi"
```

**Usage locations:**
- `lib/steps/helm_aws.go`

---

#### Azure naming patterns

Azure has strict naming constraints. The Azure naming helpers live in `lib/azure` and the Azure steps in `lib/steps`.

##### Resource groups
**Pattern:** `rsg-ptd-{sanitized_name}` (lowercase, non-`[a-z0-9-]` replaced with `-`)

Example: `rsg-ptd-myworkload-staging`

---

##### Key Vault
**Pattern:** `kv-ptd-{compound_name[:17]}` (max 24 chars total)

Example: `kv-ptd-myworkload-st` (truncated if necessary)

**Critical:** Key Vault names have a 24-character limit. The compound name is truncated to 17 chars to leave room for the `kv-ptd-` prefix.

---

##### Storage accounts
**Pattern:** `stptd{compound_name_no_hyphens[:19]}` (max 24 chars, NO hyphens)

Example: `stptdmyworkloadstaging` (no hyphens)

**Critical:** Storage account names:
- Cannot contain hyphens (Azure requirement)
- Max 24 characters
- Must be lowercase alphanumeric only

---

##### VNets
**Pattern:** `vnet-ptd-{compound_name}`

Example: `vnet-ptd-myworkload-staging`

---

##### AKS clusters
**Pattern:** `{compound_name}-{release}`

Example: `myworkload-staging-r1`

**Usage locations:**
- `lib/azure`, `lib/steps/aks.go`

---

### How to mark critical names in code

Add comments to warn future editors:

```go
// CRITICAL: This logical name is in Pulumi state. Changing it will DESTROY the RDS instance.
rds.NewInstance(ctx,
    "postgresql-primary", // <- comment above this line
    &rds.InstanceArgs{Identifier: pulumi.String(fmt.Sprintf("%s-postgres", compoundName))},
)
```

---

## Constructor patterns

PTD uses two main patterns for building Pulumi resources in Go:

### Pattern 1: Inline deploy function

Most steps build all of their resources in a single deploy function that takes a `*pulumi.Context` and the parsed config. Used for simple to moderate steps with no incremental builder needs.

**Azure note:** Azure workload/persistent resources use this pattern; there is no builder.

### Pattern 2: Builder/chaining

**Example:** `EKSCluster` (`lib/aws/eks_cluster.go`)

A constructor sets up state, then `With*()` methods build resources incrementally and return the builder for chaining. The `ptd:AWSEKSCluster` type token still appears in alias URNs so existing Pulumi state is adopted, not replaced.

```go
// lib/aws/eks_cluster.go
c, _ := aws.NewEKSCluster(ctx, cfg)
c.WithNodeRole(roleName).        // must run before WithNodeGroup (sets the default node role)
    WithNodeGroup(nodeGroupParams).
    WithOidcProvider()
```

**Important:** Builder methods on `EKSCluster` have ordering dependencies. For example, `WithNodeRole()` must be called before `WithNodeGroup()` because it sets the default node role consumed by the node group.

**Azure note:** Azure does NOT use the builder pattern. AKS cluster creation is handled in `lib/steps/aks.go` using the inline deploy-function pattern with no ordering dependencies.

---

## Output[T] handling

Pulumi resources return `pulumi.Output[T]` (similar to promises/futures) instead of plain values. You **cannot use outputs directly** in string formatting or conditionals.

### Problem

```go
// WRONG - outputs can't be used directly
bucketName := bucket.ID() // this is pulumi.IDOutput, not a string
fmt.Printf("Created bucket: %s\n", bucketName) // won't print the resolved value
```

### Solution 1: ApplyT

Use `ApplyT` to transform a single output:

```go
url := bucket.ID().ApplyT(func(id pulumi.ID) string {
    return fmt.Sprintf("s3://%s", id)
}).(pulumi.StringOutput)
```

### Solution 2: pulumi.All for multiple outputs

Combine multiple outputs before transforming:

```go
url := pulumi.All(bucket.ID(), key.ID()).ApplyT(func(args []interface{}) string {
    return fmt.Sprintf("s3://%s/%s", args[0], args[1])
}).(pulumi.StringOutput)
```

### Solution 3: Pass outputs straight into resource args

Pulumi automatically unwraps outputs when passed to resource constructors, so an output can be threaded into another resource's args without resolving it manually.

---

## Common mistakes to avoid

### 1. Changing logical names without planning
Changing the first argument to a resource constructor (the logical name) renames the state key, which Pulumi treats as DELETE + CREATE. For stateful resources (RDS, S3 with data, VPCs) this is catastrophic. Use `pulumi state rename`, or create the new resource, migrate data, then delete the old one.

### 2. Using outputs in string formatting
`pulumi.Output[T]` values are not resolved synchronously. Use `ApplyT` / `pulumi.All(...).ApplyT(...)` to derive strings from them; do not `fmt.Sprintf` an output directly.

### 3. Azure storage account names with hyphens (Azure-specific)
Azure storage account names only allow lowercase alphanumeric characters (no hyphens, no underscores). Remove all hyphens and truncate to fit (`stptd{name_no_hyphens[:19]}`).

### 4. Azure resource names exceeding character limits (Azure-specific)
- Key Vault: 24 chars max (`kv-ptd-{name[:17]}`)
- Storage Account: 24 chars max
- Most other resources: 64-80 chars (more lenient)

### 5. Incorrect Azure tag key format (Azure-specific)
Azure does not allow dots in tag keys. Convert `.` to `/` in tag keys (e.g. `posit.team/environment` becomes `posit/team/environment`).

---

## Testing Pulumi code

Run library tests with `just test-lib`. Pure helpers (naming, config parsing, metric-filter extraction) have unit tests alongside them in `lib/steps` and `lib/aws`/`lib/azure`.

To see planned changes without applying:

```bash
ptd ensure myworkload-staging --only-steps persistent --dry-run
```

---

## Related documentation
- [Config Flow](./config-flow.md) - How configuration flows from YAML into Go
- [Step Dependencies](./step-dependencies.md) - How steps depend on each other
