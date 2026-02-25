# Pulumi Conventions and Patterns

This document covers Pulumi-specific conventions that are critical for making correct changes to PTD infrastructure code without accidentally destroying resources.

## Table of Contents
- [Resource Naming (CRITICAL)](#resource-naming-critical)
- [The Autoload Pattern](#the-autoload-pattern)
- [Constructor Patterns](#constructor-patterns)
- [Output[T] Handling](#outputt-handling)
- [Key Classes and Their Roles](#key-classes-and-their-roles)

---

## Resource Naming (CRITICAL)

### Logical Names vs Physical Names

Pulumi resources have **two kinds of names**:

1. **Logical name** (first argument to resource constructor)
   - This is the key in Pulumi's state file
   - **Changing it causes a DELETE + CREATE operation**
   - Used by Pulumi to track resources across updates
   - Not visible in cloud console

2. **Physical name** (the `name` field in resource args)
   - This is the actual name in AWS/Azure/Kubernetes
   - Appears in cloud console, CLI output, etc.
   - Can sometimes be changed without destroying the resource (depends on cloud provider)

### Example

```python
# DON'T DO THIS (changes logical name)
aws.s3.Bucket(
    f"{compound_name}-loki",  # ❌ If compound_name changes, bucket is DESTROYED
    bucket=f"{compound_name}-loki-logs",
    ...
)

# DO THIS (stable logical name)
aws.s3.Bucket(
    f"loki-logs-bucket",  # ✅ Stable logical name
    bucket=f"{compound_name}-loki-logs",  # Physical name can change
    ...
)
```

**Why this matters:** Destroying and recreating resources like RDS databases, S3 buckets with data, or VPCs with running workloads is catastrophic.

---

### Resource Naming Patterns

PTD uses consistent naming patterns. Here are common patterns found in the codebase:

#### AWS Naming Patterns

##### IAM Roles
**Pattern:** `f"{purpose}.{compound_name}.posit.team"`

```python
# Example from aws_workload.py
def team_operator_role_name(self) -> str:
    return f"team-operator.{self.compound_name}.posit.team"

# Creates: "team-operator.myworkload-staging.posit.team"
```

**Usage locations:**
- `python-pulumi/src/ptd/aws_workload.py`
- IAM role names for EKS IRSA (IAM Roles for Service Accounts)

---

##### S3 Buckets
**Pattern:** `f"{compound_name}-{purpose}"`

```python
# Example from aws_workload_persistent.py
loki_bucket = aws.s3.Bucket(
    f"{self.workload.compound_name}-loki",
    bucket=f"{self.workload.compound_name}-loki",
    ...
)

# Creates: "myworkload-staging-loki"
```

**Usage locations:**
- `python-pulumi/src/ptd/pulumi_resources/aws_workload_persistent.py`
- Loki logs, Mimir metrics, general storage buckets

---

##### EKS Clusters
**Pattern:** `f"default_{compound_name}-control-plane"`

```python
# Example from aws_workload_eks.py
cluster_name = f"default_{self.workload.compound_name}-control-plane"

# Creates: "default_myworkload-staging-control-plane"
```

**Usage locations:**
- `python-pulumi/src/ptd/pulumi_resources/aws_workload_eks.py`

---

##### Helm Releases
**Pattern:** `f"{compound_name}-{release}-{component}"`

```python
# Example from aws_workload_helm.py
k8s.apiextensions.CustomResource(
    f"{self.workload.compound_name}-{release}-aws-fsx-openzfs-csi-helm-release",
    metadata=k8s.meta.v1.ObjectMetaArgs(
        name="aws-fsx-openzfs-csi",  # Physical name (what appears in K8s)
        ...
    ),
    ...
)

# Logical name: "myworkload-staging-r1-aws-fsx-openzfs-csi-helm-release"
# Physical name: "aws-fsx-openzfs-csi"
```

**Usage locations:**
- `python-pulumi/src/ptd/pulumi_resources/aws_workload_helm.py`

---

#### Azure Naming Patterns

Azure has strict naming constraints. All naming methods are on the `AzureWorkload` class in `python-pulumi/src/ptd/azure_workload.py`.

##### Resource Groups
**Pattern:** `f"rsg-ptd-{sanitized_name}"`

```python
# Example from azure_workload.py
@property
def resource_group_name(self) -> str:
    name = self.compound_name.lower()
    name = re.sub(r"[^a-z0-9-]", "-", name)
    return f"rsg-ptd-{name}"

# Creates: "rsg-ptd-myworkload-staging"
```

---

##### Key Vault
**Pattern:** `f"kv-ptd-{compound_name[:17]}"` (max 24 chars total)

```python
# Example from azure_workload.py
@functools.cached_property
def key_vault_name(self) -> str:
    name = self.compound_name.lower()
    name = re.sub(r"[^a-z0-9-]", "-", name)
    name = name[:17]  # Truncate to fit max length
    return f"kv-ptd-{name}"

# Creates: "kv-ptd-myworkload-st" (truncated if necessary)
```

**Critical:** Key Vault names have a 24-character limit. The compound name is truncated to 17 chars to leave room for the `kv-ptd-` prefix.

---

##### Storage Accounts
**Pattern:** `f"stptd{compound_name_no_hyphens[:19]}"` (max 24 chars, NO hyphens)

```python
# Example from azure_workload.py
@property
def storage_account_name(self) -> str:
    name = self.compound_name.lower()
    name = re.sub(r"[^a-z0-9-]", "-", name)
    name = name.replace("-", "")  # Remove ALL hyphens
    name = name[:19]
    return f"stptd{name}"

# Creates: "stptdmyworkloadstaging" (no hyphens!)
```

**Critical:** Storage account names:
- Cannot contain hyphens (Azure requirement)
- Max 24 characters
- Must be lowercase alphanumeric only

---

##### VNets
**Pattern:** `f"vnet-ptd-{compound_name}"`

```python
# Example from azure_workload.py
@property
def vnet_name(self) -> str:
    return f"vnet-ptd-{self.compound_name}"

# Creates: "vnet-ptd-myworkload-staging"
```

---

##### AKS Clusters
**Pattern:** `f"{compound_name}-{release}"`

```python
# Example from azure_workload.py
def cluster_name(self, release: str) -> str:
    return f"{self.compound_name}-{release}"

# Creates: "myworkload-staging-r1"
```

**Usage locations:**
- `python-pulumi/src/ptd/azure_workload.py`
- All Azure resource naming methods

---

### How to Mark Critical Names in Code

Add comments to warn future editors:

```python
# CRITICAL: This logical name is in Pulumi state. Changing it will DESTROY the RDS instance.
rds_instance = aws.rds.Instance(
    "postgresql-primary",  # ← Comment above this line
    identifier=f"{self.workload.compound_name}-postgres",
    ...
)
```

---

## The Autoload Pattern

PTD uses a convention where Python Pulumi modules are dynamically loaded by Go-generated `__main__.py` files.

### How It Works

1. **Go generates `__main__.py`** (see `lib/pulumi/python.go:127-131`):
   ```python
   import ptd.pulumi_resources.aws_workload_persistent

   ptd.pulumi_resources.aws_workload_persistent.AWSWorkloadPersistent.autoload()
   ```

2. **Python module provides an `autoload()` classmethod:**
   ```python
   class AWSWorkloadPersistent(pulumi.ComponentResource):
       @classmethod
       def autoload(cls) -> "AWSWorkloadPersistent":
           # Reads stack name from Pulumi context
           stack_name = pulumi.get_stack()
           # Creates workload object from YAML
           workload = ptd.aws_workload.AWSWorkload(stack_name)
           # Instantiates the component
           return cls(workload=workload)
   ```

3. **Component constructor creates all resources:**
   ```python
   def __init__(self, workload: ptd.aws_workload.AWSWorkload):
       super().__init__(f"ptd:{self.__class__.__name__}", workload.compound_name)
       # Create resources here
   ```

### Naming Convention

| Element | Format | Example |
|---------|--------|---------|
| **Module name** | `{cloud}_{target_type}_{step_name}` | `aws_workload_persistent` |
| **Class name** | `{Cloud}{TargetType}{StepName}` | `AWSWorkloadPersistent` |

**Special cases** (see `lib/pulumi/python.go:88-94`):
- `"aws"` → `"AWS"` (not `"Aws"`)
- `"postgres_config"` → `"PostgresConfig"`
- `"eks"` → `"EKS"`

**Generated file location:** Pulumi workspace directory (temporary, not source-controlled)

---

## Constructor Patterns

PTD uses three main patterns for Pulumi component constructors:

### Pattern 1: All-in-Constructor
**Example:** `CertManager`

All resources created in `__init__`, call `register_outputs({})` at the end.

```python
class CertManager(pulumi.ComponentResource):
    def __init__(self, workload, provider, **kwargs):
        super().__init__(f"ptd:{self.__class__.__name__}", workload.compound_name, **kwargs)

        # Create all resources
        self.namespace = k8s.core.v1.Namespace(...)
        self.helm_release = k8s.helm.v3.Release(...)

        # Register outputs
        self.register_outputs({})
```

**When to use:**
- Simple components with no conditional logic
- All resources created unconditionally

**Azure note:** Azure workload components consistently use this pattern with `_define_*()` helper methods (no builder pattern).

---

### Pattern 2: Builder/Chaining
**Example:** `AWSEKSCluster`

`__init__` sets up state, then `with_*()` methods build resources incrementally. Returns `self` for chaining.

```python
class AWSEKSCluster(pulumi.ComponentResource):
    def __init__(self, name, subnet_ids, version, tags, **kwargs):
        super().__init__(f"ptd:{self.__class__.__name__}", name, **kwargs)

        self.name = name
        self.tags = tags
        # Initialize collections
        self.node_groups = {}
        self.fargate_profiles = {}

        # Create cluster (but not node groups yet)
        self.eks = aws.eks.Cluster(...)

    def with_node_role(self) -> "AWSEKSCluster":
        self.default_node_role = aws.iam.Role(...)
        return self

    def with_node_group(self, name, ...) -> "AWSEKSCluster":
        self.node_groups[name] = aws.eks.NodeGroup(...)
        return self

# Usage
cluster = AWSEKSCluster(...).with_node_role().with_node_group("default")
```

**When to use:**
- Complex resources with many optional components
- Want to expose a fluent API for configuration

**Important:** Builder methods in `AWSEKSCluster` have ordering dependencies. For example, `with_node_role()` must be called before `with_node_group()` because it sets `self.default_node_role`.

**Azure note:** Azure does NOT use the builder pattern. AKS cluster creation is handled in Go (`lib/steps/aks.go`), not Python. Azure Python components use the all-in-constructor pattern with `_define_*()` methods that have no ordering dependencies.

---

### Pattern 3: Autoload + Constructor
**Example:** `AWSWorkloadHelm`, `AzureWorkloadHelm`

`autoload()` classmethod loads config, then `__init__` creates resources.

```python
class AWSWorkloadHelm(pulumi.ComponentResource):
    @classmethod
    def autoload(cls) -> "AWSWorkloadHelm":
        return cls(workload=ptd.aws_workload.AWSWorkload(pulumi.get_stack()))

    def __init__(self, workload: ptd.aws_workload.AWSWorkload):
        super().__init__(f"ptd:{self.__class__.__name__}", workload.compound_name)

        self.workload = workload
        # Create resources
        self._define_traefik(...)
        self._define_loki(...)
```

**When to use:**
- Components invoked via Go-generated `__main__.py`
- Need to load configuration from YAML before creating resources

---

## Output[T] Handling

Pulumi resources return `Output[T]` (similar to promises/futures) instead of plain values. You **cannot use outputs directly** in f-strings or conditionals.

### Problem

```python
# ❌ WRONG - outputs can't be used directly
bucket_name = bucket.id  # This is Output[str], not str
print(f"Created bucket: {bucket_name}")  # Won't work as expected
```

### Solution 1: `.apply()`

Use `.apply()` to transform outputs:

```python
# ✅ Correct
bucket_name = bucket.id.apply(lambda name: f"s3://{name}")
```

### Solution 2: `Output.all()` for Multiple Outputs

Combine multiple outputs before transforming:

```python
# ✅ Correct
url = pulumi.Output.all(bucket.id, key.id).apply(
    lambda args: f"s3://{args[0]}/{args[1]}"
)
```

### Solution 3: Use Outputs in Resource Args

Pulumi automatically unwraps outputs when passed to resource constructors:

```python
# ✅ Correct - Pulumi handles Output[str] automatically
policy = aws.iam.Policy(
    "my-policy",
    policy=bucket.arn.apply(lambda arn: json.dumps({
        "Statement": [{
            "Resource": arn,
            ...
        }]
    }))
)
```

### Testing with Mocks

In tests using `pulumi.runtime.set_mocks()`, outputs resolve synchronously:

```python
@pulumi.runtime.test
def test_something():
    pulumi.runtime.set_mocks(...)

    bucket = aws.s3.Bucket("test-bucket")
    # In tests, .id resolves immediately
    assert bucket.id == "test-bucket"
```

---

## Key Classes and Their Roles

### AbstractWorkload
**Location:** `python-pulumi/src/ptd/workload.py`

**Role:** Base class for all workload types (AWS, Azure). Loads configuration from YAML.

**Key methods:**
- `__init__(name, paths)`: Loads `ptd.yaml` from disk
- `load_unique_config()`: Abstract method for cloud-specific config
- `compound_name`: Property returning `"{true_name}-{environment}"`

**Example:**
```python
class AbstractWorkload(ABC):
    def __init__(self, name: str, paths: ptd.paths.Paths | None = None):
        self.d = (paths or ptd.paths.Paths()).workloads / name
        cfg_dict = yaml.safe_load(self.ptd_yaml.read_text())
        self._load_common_config()
        self.load_unique_config()
```

---

### AWSWorkload
**Location:** `python-pulumi/src/ptd/aws_workload.py`

**Role:** AWS-specific workload config loading, role name generation, naming conventions.

**Key methods:**
- `load_unique_config()`: Parses AWS-specific YAML into `AWSWorkloadConfig`
- `team_operator_role_name()`: Returns IAM role name for Team Operator
- `aws_assume_role()`: Returns temporary credentials for workload account
- `managed_clusters_by_release()`: Returns cluster info for all releases

**Example:**
```python
class AWSWorkload(AbstractWorkload):
    cfg: AWSWorkloadConfig

    def load_unique_config(self) -> None:
        # Parse AWS-specific config from YAML
        self.cfg = AWSWorkloadConfig(**self.spec)

    @property
    def team_operator_role_name(self) -> str:
        return f"team-operator.{self.compound_name}.posit.team"
```

---

### AzureWorkload
**Location:** `python-pulumi/src/ptd/azure_workload.py`

**Role:** Azure-specific workload config loading, naming with strict character limits.

**Key methods:**
- `load_unique_config()`: Parses Azure-specific YAML into `AzureWorkloadConfig`
- `resource_group_name`: Returns sanitized resource group name (`f"rsg-ptd-{name}"`)
- `key_vault_name`: Returns Key Vault name with 24-char limit (`f"kv-ptd-{name[:17]}"`)
- `storage_account_name`: Returns storage account name with NO hyphens (`f"stptd{name_no_hyphens[:19]}"`)
- `cluster_name(release)`: Returns AKS cluster name (`f"{compound_name}-{release}"`)
- `vnet_name`: Returns VNet name (`f"vnet-ptd-{compound_name}"`)

**Example:**
```python
class AzureWorkload(AbstractWorkload):
    cfg: AzureWorkloadConfig

    def load_unique_config(self) -> None:
        # Parse Azure-specific config from YAML
        self.cfg = AzureWorkloadConfig(**self.spec)

    @property
    def storage_account_name(self) -> str:
        name = self.compound_name.lower()
        name = re.sub(r"[^a-z0-9-]", "-", name)
        name = name.replace("-", "")  # Remove ALL hyphens
        name = name[:19]
        return f"stptd{name}"
```

**Critical:** AKS cluster creation is in Go (`lib/steps/aks.go`), not Python, unlike EKS.

---

### WorkloadConfig / AWSWorkloadConfig / AzureWorkloadConfig
**Location:** `python-pulumi/src/ptd/__init__.py`, `python-pulumi/src/ptd/aws_workload.py`, `python-pulumi/src/ptd/azure_workload.py`

**Role:** Frozen dataclasses holding parsed configuration.

**Key fields:**
```python
@dataclasses.dataclass(frozen=True)
class WorkloadConfig:
    true_name: str
    environment: str
    region: str
    control_room_account_id: str
    control_room_cluster_name: str
    network_trust: NetworkTrust

@dataclasses.dataclass(frozen=True)
class AWSWorkloadConfig(WorkloadConfig):
    account_id: str
    tailscale_enabled: bool
    clusters: dict[str, AWSWorkloadClusterConfig]
    # ... many more AWS-specific fields

@dataclasses.dataclass(frozen=True)
class AzureWorkloadConfig(WorkloadConfig):
    subscription_id: str
    tenant_id: str
    client_id: str
    network: NetworkConfig  # Nested dataclass for Azure
    clusters: dict[str, AzureWorkloadClusterConfig]
    # ... Azure-specific fields
```

**Usage:**
```python
# AWS
workload = AWSWorkload("myworkload-staging")
print(workload.cfg.account_id)  # "123456789012"
print(workload.cfg.region)       # "us-east-1"

# Azure
workload = AzureWorkload("myworkload-staging")
print(workload.cfg.subscription_id)  # "abc-123-def"
print(workload.cfg.network.vnet_cidr)  # "10.0.0.0/16"
```

---

### pulumi.ComponentResource
**Location:** Pulumi SDK

**Role:** Base class for all PTD infrastructure modules.

**Pattern:**
```python
class MyComponent(pulumi.ComponentResource):
    def __init__(self, name, **kwargs):
        super().__init__(
            f"ptd:{self.__class__.__name__}",  # Type identifier
            name,                                # Logical name
            **kwargs
        )
        # Create child resources
        self.register_outputs({})  # Optional: export outputs
```

**Why use it:**
- Groups related resources together in Pulumi state
- Provides encapsulation for complex infrastructure
- Allows custom resource providers

---

## Common Mistakes to Avoid

### 1. Changing Logical Names Without Planning
**Mistake:**
```python
# Old code
aws.s3.Bucket(f"{workload.compound_name}-loki", ...)

# New code (accidentally changes logical name)
aws.s3.Bucket(f"loki-bucket-{workload.compound_name}", ...)
```

**Impact:** Bucket is destroyed and recreated, losing all logs.

**Fix:** Use `pulumi state rename` or create a resource with the new name, migrate data, then delete the old one.

---

### 2. Using Outputs in F-Strings
**Mistake:**
```python
bucket_name = bucket.id  # Output[str]
key = f"s3://{bucket_name}/data"  # ❌ Won't work
```

**Fix:**
```python
key = bucket.id.apply(lambda name: f"s3://{name}/data")
```

---

### 3. Missing `autoload()` Classmethod
**Mistake:**
```python
class AWSWorkloadHelm(pulumi.ComponentResource):
    def __init__(self, workload):
        # No autoload() method
        ...
```

**Impact:** Go-generated `__main__.py` calls `autoload()` and fails.

**Fix:**
```python
@classmethod
def autoload(cls) -> "AWSWorkloadHelm":
    return cls(workload=ptd.aws_workload.AWSWorkload(pulumi.get_stack()))
```

---

### 4. Forgetting `register_outputs({})`
**Mistake:**
```python
class MyComponent(pulumi.ComponentResource):
    def __init__(self, name):
        super().__init__(f"ptd:{self.__class__.__name__}", name)
        # Create resources but forget to register outputs
```

**Impact:** Component works but doesn't export any outputs for `pulumi stack output`.

**Fix:** Call `self.register_outputs({...})` at the end of `__init__`.

---

### 5. Azure Storage Account Names with Hyphens (Azure-specific)
**Mistake:**
```python
# ❌ WRONG - Azure storage accounts cannot have hyphens
storage_account_name = f"stptd-{compound_name}"
```

**Impact:** Azure API rejects the resource creation with a validation error.

**Fix:**
```python
# ✅ Correct - remove ALL hyphens
name = compound_name.replace("-", "")
storage_account_name = f"stptd{name[:19]}"
```

**Why this happens:** AI models trained on AWS patterns often add hyphens, but Azure storage accounts only allow lowercase alphanumeric characters (no hyphens, no underscores).

---

### 6. Azure Resource Names Exceeding Character Limits (Azure-specific)
**Mistake:**
```python
# ❌ WRONG - Key Vault names can't exceed 24 chars
key_vault_name = f"kv-ptd-{very_long_compound_name}"  # Could be 30+ chars
```

**Impact:** Azure API rejects the resource with a length validation error.

**Fix:**
```python
# ✅ Correct - truncate to fit within limits
name = compound_name[:17]  # Leave room for "kv-ptd-" prefix
key_vault_name = f"kv-ptd-{name}"
```

**Azure character limits:**
- Key Vault: 24 chars max
- Storage Account: 24 chars max
- Most other resources: 64-80 chars (more lenient)

---

### 7. Incorrect Azure Tag Key Format (Azure-specific)
**Mistake:**
```python
# ❌ WRONG - Azure doesn't allow dots in tag keys
tags = {"posit.team/environment": "staging"}
```

**Impact:** Azure silently converts or rejects the tags, leading to inconsistent tagging.

**Fix:**
```python
# ✅ Correct - use azure_tag_key_format() helper
tags = {
    ptd.azure_tag_key_format("posit.team/environment"): "staging"
}
# Converts dots to forward slashes: "posit/team/environment"
```

---

## Testing Pulumi Code

### Unit Tests with Mocks

```python
import pulumi
import pytest

class MyMocks(pulumi.runtime.Mocks):
    def new_resource(self, args: pulumi.runtime.MockResourceArgs):
        return [args.name, args.inputs]

    def call(self, args: pulumi.runtime.MockCallArgs):
        return {}

@pulumi.runtime.test
def test_component():
    pulumi.runtime.set_mocks(MyMocks())

    # Outputs resolve synchronously in tests
    bucket = aws.s3.Bucket("test-bucket")
    assert bucket.id == "test-bucket"
```

### Integration Tests

Run `pulumi preview` to see planned changes without applying:

```bash
export AWS_PROFILE=ptd-staging
ptd ensure myworkload-staging --only-steps persistent --dry-run
```

---

## Related Documentation
- [Config Flow](./config-flow.md) - How configuration flows from YAML to Go to Python
- [Step Dependencies](./step-dependencies.md) - How steps depend on each other
