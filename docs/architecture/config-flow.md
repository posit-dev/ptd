# Configuration Flow: YAML → Go → Python

This document explains how configuration flows from YAML files through the Go CLI orchestrator into Python Pulumi infrastructure code.

## Overview

PTD configuration is **parsed twice** during execution:
1. **Go CLI** parses YAML to make orchestration decisions (which steps to run, credentials, etc.)
2. **Python Pulumi** re-reads the same YAML to build infrastructure resources

This dual-parsing architecture means configuration changes must be synchronized across both Go structs and Python dataclasses.

## Configuration Flow Diagram

```
┌─────────────────────────────────────────────────────────────────┐
│ YAML Configuration Files                                        │
│ __work__/<workload-name>/ptd.yaml                              │
│ __ctrl__/<control-room-name>/ptd.yaml                          │
└─────────────────┬───────────────────────────────────────────────┘
                  │
        ┌─────────┴─────────┐
        │                   │
        ▼                   ▼
┌───────────────┐   ┌──────────────────┐
│ Go CLI        │   │ Python Pulumi    │
│ (Parse #1)    │   │ (Parse #2)       │
└───────┬───────┘   └────────┬─────────┘
        │                    │
        ▼                    ▼
┌───────────────┐   ┌──────────────────┐
│ Go Structs    │   │ Python Dataclass │
│ - WorkloadSpec│   │ - WorkloadConfig │
│ - ClusterSpec │   │ - AWSWorkloadCfg │
└───────┬───────┘   └────────┬─────────┘
        │                    │
        ▼                    │
┌───────────────┐            │
│ Orchestration │            │
│ - Step order  │            │
│ - Credentials │            │
│ - PTD_ROOT    │────────────┤
└───────────────┘            │
                             ▼
                    ┌─────────────────┐
                    │ Infrastructure  │
                    │ Resources       │
                    └─────────────────┘
```

## Detailed Flow

### Step 1: Go CLI Parses Configuration

**Location:** `cmd/ensure.go`, `lib/types/workload.go`, `lib/types/types.go`

The Go CLI loads YAML into Go structs when you run `ptd ensure <target>`:

```go
// lib/types/workload.go
type WorkloadSpec struct {
    AccountID               string                    `yaml:"account_id"`
    Region                  string                    `yaml:"region"`
    TailscaleEnabled        bool                      `yaml:"tailscale_enabled"`
    Clusters                map[string]ClusterSpec    `yaml:"clusters"`
    // ... more fields
}
```

The Go code uses these structs to:
- Determine which steps to run (bootstrap, persistent, eks/aks, etc.)
- Set up AWS/Azure credentials
- Configure the Pulumi backend URL
- **Set the `PTD_ROOT` environment variable** (critical bridge to Python)

### Step 2: Go Invokes Python via Pulumi

**Location:** `lib/pulumi/python.go`

When executing Python-based steps (persistent, postgres_config, helm, etc.), Go:

1. **Sets PTD_ROOT** (line 55 of `lib/pulumi/python.go`):
   ```go
   envVars["PTD_ROOT"] = helpers.GetTargetsConfigPath()
   ```

2. **Generates `__main__.py`** dynamically (lines 77-101):
   ```python
   # Generated __main__.py example
   import ptd.pulumi_resources.aws_workload_persistent

   ptd.pulumi_resources.aws_workload_persistent.AWSWorkloadPersistent.autoload()
   ```

3. **Module naming convention:**
   - Module: `{cloud}_{target_type}_{step_name}` (e.g., `aws_workload_persistent`)
   - Class: `{Cloud}{TargetType}{StepName}` (e.g., `AWSWorkloadPersistent`)

### Step 3: Python Re-reads YAML Configuration

**Location:** `python-pulumi/src/ptd/workload.py`, `python-pulumi/src/ptd/aws_workload.py`

The Python infrastructure code reads the same YAML file using `PTD_ROOT`:

```python
# python-pulumi/src/ptd/paths.py
class Paths:
    def __init__(self):
        # Reads PTD_ROOT environment variable set by Go
        ptd_root = pathlib.Path(os.environ.get("PTD_ROOT", ...))
        self.workloads = ptd_root / "__work__"
        self.control_rooms = ptd_root / "__ctrl__"

# python-pulumi/src/ptd/workload.py
class AbstractWorkload(ABC):
    def __init__(self, name: str, paths: ptd.paths.Paths | None = None):
        self.d = (paths or ptd.paths.Paths()).workloads / name
        # Re-reads ptd.yaml from disk
        cfg_dict = yaml.safe_load(self.ptd_yaml.read_text())
```

### Step 4: Python Converts to Dataclasses

**Location:** `python-pulumi/src/ptd/__init__.py`, `python-pulumi/src/ptd/aws_workload.py`, `python-pulumi/src/ptd/azure_workload.py`

YAML configuration is parsed into frozen dataclasses:

```python
@dataclasses.dataclass(frozen=True)
class WorkloadConfig:
    true_name: str
    environment: str
    region: str
    # ... more fields

@dataclasses.dataclass(frozen=True)
class AWSWorkloadConfig(WorkloadConfig):
    account_id: str
    tailscale_enabled: bool
    clusters: dict[str, AWSWorkloadClusterConfig]
    # ... AWS-specific fields

@dataclasses.dataclass(frozen=True)
class AzureWorkloadConfig(WorkloadConfig):
    subscription_id: str
    tenant_id: str
    client_id: str
    network: NetworkConfig  # Azure has nested config for network
    clusters: dict[str, AzureWorkloadClusterConfig]
    # ... Azure-specific fields
```

**Azure-specific note:** Azure config includes a `NetworkConfig` nested dataclass with stricter naming constraints (see `python-pulumi/src/ptd/azure_workload.py:24-50`) for subnet CIDRs and VNet configuration.

**Critical pattern:** YAML keys with hyphens are converted to underscores (line 408-409 of `aws_workload.py`):

```python
for key in list(cluster_spec.keys()):
    cluster_spec[key.replace("-", "_")] = cluster_spec.pop(key)
```

This means:
- YAML: `tailscale-enabled`
- Go struct tag: `yaml:"tailscale_enabled"`
- Python dataclass field: `tailscale_enabled`

## The Bridge: PTD_ROOT

`PTD_ROOT` is the critical environment variable that connects Go and Python:

| Component | Role | Location |
|-----------|------|----------|
| **Go sets it** | Points to the targets directory | `lib/pulumi/python.go:55` |
| **Python reads it** | Locates `__work__/` and `__ctrl__/` directories | `python-pulumi/src/ptd/paths.py` |
| **Tests must set it** | Required for Python tests to load config | Via `monkeypatch.setenv()` |

Without `PTD_ROOT`, Python cannot find the YAML configuration files.

## How to Add a New Configuration Option

Follow this checklist to add a new configuration field:

### 1. Update YAML Schema
Add the field to your YAML file:
```yaml
spec:
  my_new_setting: true
```

### 2. Add Go Struct Field
Update the appropriate struct in `lib/types/`:
```go
// lib/types/workload.go
type WorkloadSpec struct {
    // ... existing fields
    MyNewSetting bool `yaml:"my_new_setting"`
}
```

**Important:** The YAML struct tag must match the Python field name (with underscores).

### 3. Add Python Dataclass Field
Update the corresponding dataclass in `python-pulumi/src/ptd/`:
```python
@dataclasses.dataclass(frozen=True)
class AWSWorkloadConfig(WorkloadConfig):
    # ... existing fields
    my_new_setting: bool = False  # Provide a default if optional
```

**For Azure workloads:** Azure has its own config files in `python-pulumi/src/ptd/azure_workload.py` with cloud-specific dataclasses (`AzureWorkloadConfig`, `NetworkConfig`). Azure config changes follow the same pattern but are in separate files from AWS.

### 4. Handle Hyphen-to-Underscore Conversion
The conversion happens automatically in the config loader (e.g., `aws_workload.py`):
```python
for key in list(cluster_spec.keys()):
    cluster_spec[key.replace("-", "_")] = cluster_spec.pop(key)
```

No additional changes needed unless you have nested configuration.

### 5. Update Tests
If the config is required, update test fixtures:
- Go tests: Update test YAML files or mock structs
- Python tests: Set `PTD_ROOT` via `monkeypatch.setenv()` and update test config files

### 6. Validation (Optional)
Add validation logic if needed:
- Go: In `lib/types/workload.go` or during step setup
- Python: In dataclass `__post_init__` or during resource creation

## Common Pitfalls

### 1. Mismatched Field Names
**Problem:** Go struct tag doesn't match Python dataclass field name.

```go
// BAD - Go uses different name than Python
type WorkloadSpec struct {
    TailscaleOn bool `yaml:"tailscale_enabled"`  // ❌ Field name doesn't match
}
```

```python
# Python expects field name to match
tailscale_enabled: bool  # Must match Go struct field name, not YAML tag
```

**Solution:** Use consistent field names. The YAML tag can differ, but the struct field name should match the Python field.

### 2. Missing PTD_ROOT in Tests
**Problem:** Python tests fail with "File not found" errors.

**Solution:** Always set `PTD_ROOT` in test fixtures:
```python
def test_something(monkeypatch):
    monkeypatch.setenv("PTD_ROOT", "/path/to/test/fixtures")
    # ... rest of test
```

### 3. Forgetting Hyphen-to-Underscore Conversion
**Problem:** Python tries to use hyphenated keys that don't exist.

**Solution:** The conversion is automatic in config loaders, but make sure custom parsers include it:
```python
for key in list(config.keys()):
    config[key.replace("-", "_")] = config.pop(key)
```

### 4. No Validation Between Go and Python
**Problem:** Go and Python structs drift apart over time.

**Solution:** There is no automated validation. Code review must catch mismatches. Consider:
- Documenting all config fields in one place (this document)
- Creating integration tests that validate config parsing in both languages
- Using linters or custom tooling to check struct/dataclass alignment

## Testing Configuration Changes

### Go Side
```bash
just test-lib
```

Tests are in `lib/types/*_test.go`.

### Python Side
```bash
just test-python-pulumi
```

Tests are in `python-pulumi/tests/`. Remember to set `PTD_ROOT`:
```python
import pytest

def test_config_loading(monkeypatch, tmp_path):
    # Create test YAML
    workload_dir = tmp_path / "__work__" / "test-staging"
    workload_dir.mkdir(parents=True)
    (workload_dir / "ptd.yaml").write_text("""
    spec:
      account_id: "123456789"
      region: us-east-1
    """)

    # Set PTD_ROOT
    monkeypatch.setenv("PTD_ROOT", str(tmp_path))

    # Load config
    workload = AWSWorkload("test-staging")
    assert workload.cfg.account_id == "123456789"
```

## Related Documentation
- [Step Dependencies](./step-dependencies.md) - How steps depend on each other
- [Pulumi Conventions](./pulumi-conventions.md) - Pulumi-specific patterns
