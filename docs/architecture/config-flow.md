# Configuration flow: YAML → Go

This document explains how configuration flows from YAML files through the Go CLI into the inline-Go Pulumi steps that build infrastructure.

## Overview

PTD configuration is parsed once, by the Go CLI. The Go structs in `lib/types/*.go` are the sole source of truth for ptd.yaml config. The parsed structs drive both orchestration decisions (which steps to run, credentials, backend) and the inline-Go Pulumi programs that create cloud resources.

There is no longer a second parser. (Historically a parallel Python layer re-read the same YAML into dataclasses; that layer and its Go↔Python parity linter were removed when Python was deleted from the repo.)

## Configuration flow diagram

```
┌─────────────────────────────────────────────────────────────────┐
│ YAML Configuration Files                                        │
│ __work__/<workload-name>/ptd.yaml                              │
│ __ctrl__/<control-room-name>/ptd.yaml                          │
└─────────────────┬───────────────────────────────────────────────┘
                  │
                  ▼
          ┌───────────────┐
          │ Go CLI        │
          │ (parse YAML)  │
          └───────┬───────┘
                  │
                  ▼
          ┌───────────────┐
          │ Go Structs    │
          │ - WorkloadSpec│
          │ - ClusterSpec │
          └───────┬───────┘
                  │
        ┌─────────┴──────────┐
        ▼                    ▼
┌───────────────┐   ┌──────────────────┐
│ Orchestration │   │ Inline-Go Pulumi │
│ - Step order  │   │ steps (lib/steps)│
│ - Credentials │   │                  │
│ - Backend URL │   │                  │
└───────────────┘   └────────┬─────────┘
                             ▼
                    ┌─────────────────┐
                    │ Infrastructure  │
                    │ Resources       │
                    └─────────────────┘
```

## Detailed flow

### Step 1: Go CLI parses configuration {#go-cli-parses-configuration}

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

### Step 2: Steps build infrastructure {#steps-build-infrastructure}

**Location:** `lib/steps/`

Each step is an inline-Go Pulumi program compiled into the `ptd` binary. The CLI builds a Pulumi Automation API workspace (project, stack name, backend, secrets provider) and runs the step's program with the parsed config structs in hand. Resources are created directly from the Go config; nothing re-reads the YAML.

## How to add a new configuration option

Follow this checklist to add a new configuration field:

### 1. Update YAML schema {#update-yaml-schema}
Add the field to your YAML file:
```yaml
spec:
  my_new_setting: true
```

### 2. Add Go struct field {#add-go-struct-field}
Update the appropriate struct in `lib/types/`:
```go
// lib/types/workload.go
type WorkloadSpec struct {
    // ... existing fields
    MyNewSetting bool `yaml:"my_new_setting"`
}
```

The YAML struct tag uses snake_case to match the YAML key. By convention, ptd.yaml keys use snake_case.

### 3. Consume the field in a step {#consume-the-field}
Read the new field from the config struct in the relevant step under `lib/steps/` and create or adjust resources accordingly.

### 4. Update tests {#update-tests}
If the config is required, update Go test fixtures:
- Update test YAML files or mock structs used by `lib/types/*_test.go` and `lib/steps/*_test.go`

### 5. Validation (optional) {#validation}
Add validation logic if needed in `lib/types/workload.go` or during step setup.

## Common pitfalls

### 1. YAML tag does not match the key
**Problem:** The `yaml:"..."` struct tag does not match the actual ptd.yaml key, so the field silently stays at its zero value.

**Solution:** Make the struct tag match the YAML key exactly (snake_case).

### 2. Missing default for an optional field
**Problem:** An optional field is absent from a ptd.yaml and the zero value is wrong for the use case.

**Solution:** Handle the zero value explicitly in the step, or normalize defaults when loading config in `lib/types`.

## Testing configuration changes

```bash
just test-lib
```

Tests are in `lib/types/*_test.go` and `lib/steps/*_test.go`.

## Related documentation
- [Step Dependencies](./step-dependencies.md) - How steps depend on each other
- [Pulumi Conventions](./pulumi-conventions.md) - Pulumi-specific patterns
