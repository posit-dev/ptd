# Custom Targets Configuration Directory

## Overview

By default, PTD expects target configurations (workloads and control rooms) in the `infra/` directory relative to the project root. This guide explains how to use a custom location for your infrastructure configurations.

## Why Use a Custom Directory?

You might want to use a custom targets configuration directory for:

- **Separate Repository**: Keep infrastructure configs in a separate Git repository for security or organizational reasons
- **Multi-Environment Management**: Point to different infrastructure configurations for dev, staging, and production
- **Testing**: Test CLI changes against different infrastructure configurations without modifying your main setup

## Configuration Methods

You can specify a custom targets configuration directory in three ways, with the following precedence (highest to lowest):

1. **CLI Flag**: `--targets-config-dir`
2. **Environment Variable**: `PTD_TARGETS_CONFIG_DIR`
3. **Config File**: `targets_config_dir` in `ptdconfig.yaml` (see `ptd config` command for details on using a config file)
4. **Default**: `./infra` (relative to project root)

### Method 1: CLI Flag

Use the `--targets-config-dir` flag with any command:

```bash
# Absolute path
ptd --targets-config-dir /home/user/my-targets ensure workload01

# Relative path (resolved relative to project root)
ptd --targets-config-dir ../separate-infra-repo ensure workload01
```

This is useful for one-off commands or when you want to temporarily use a different configuration.

### Method 2: Environment Variable

Set the `PTD_TARGETS_CONFIG_DIR` environment variable:

```bash
# In your shell
export PTD_TARGETS_CONFIG_DIR=/home/user/my-targets
ptd ensure workload01

# Or inline for a single command
PTD_TARGETS_CONFIG_DIR=/home/user/my-targets ptd ensure workload01
```

This is useful for:
- CI/CD pipelines
- Different configurations per shell session
- Temporary overrides without modifying config files

### Method 3: Config File

Edit your PTD config file (`~/.config/ptd/ptdconfig.yaml`):

```yaml
# Absolute path
targets_config_dir: /home/user/my-targets

# Or relative to project root (TOP)
targets_config_dir: ../separate-infra-repo
```

This is the recommended method for permanent configuration changes.

## Directory Structure

Your custom targets configuration directory must contain the expected structure:

```
<targets_config_dir>/
├── __ctrl__/          # Control room configurations
│   └── main01/
│       └── ptd.yaml
└── __work__/          # Workload configurations
    ├── workload01/
    │   └── ptd.yaml
    └── workload02/
        └── ptd.yaml
```

## Path Resolution

### Absolute Paths

Absolute paths are used as-is:

```bash
# Unix/Linux/macOS
ptd --targets-config-dir /home/user/my-targets ensure workload01

# Windows
ptd --targets-config-dir C:\Users\user\my-targets ensure workload01
```

### Relative Paths

Relative paths are resolved relative to the project root (TOP), not the current working directory:

```yaml
# In ptdconfig.yaml
targets_config_dir: ../separate-infra-repo
```

If your project root is `/home/user/ptd`, this resolves to `/home/user/separate-infra-repo`.

## Verification

### Check Current Configuration

Use `config show` to see your current configuration:

```bash
ptd config show
```

This displays:
- The current targets configuration directory path
- How it was configured (CLI flag, environment variable, config file, or default)
- Other configuration settings

Example output:
```
Targets configuration directory: /home/user/my-targets
  (configured via config file)
```

## Common Use Cases

### Scenario 1: Separate Repository

Keep infrastructure configurations in a separate Git repository:

```yaml
# ~/.config/ptd/ptdconfig.yaml
targets_config_dir: /home/user/repos/ptd-infrastructure
```

### Scenario 2: Development vs Production

Use different configurations for different environments:

```bash
# Development
export PTD_TARGETS_CONFIG_DIR=~/ptd-configs-dev
ptd ensure workload01

# Production
export PTD_TARGETS_CONFIG_DIR=/prod/ptd-configs
ptd ensure workload01
```

### Scenario 3: CI/CD Pipeline

Configure in your CI/CD environment:

```yaml
# .gitlab-ci.yml
variables:
  PTD_TARGETS_CONFIG_DIR: ${CI_PROJECT_DIR}/deployed-configs
```

### Scenario 4: Testing Different Configurations

Test CLI changes against different infrastructure setups:

```bash
# Test against config set 1
ptd --targets-config-dir ./test-configs-1 ensure workload01

# Test against config set 2
ptd --targets-config-dir ./test-configs-2 ensure workload01
```

## Integration with Python

The PTD CLI automatically passes the targets configuration directory to Python Pulumi stacks via the `PTD_ROOT` environment variable. You don't need to configure anything in Python - it automatically receives the correct path from the Go CLI.

## Troubleshooting

### Error: Directory does not exist

If you see an error like "targets configuration directory does not exist", verify:

1. The path is correct (check for typos)
2. The directory exists on your filesystem
3. You have read permissions for the directory

### Error: Missing expected structure

If you see an error about missing `__ctrl__` or `__work__` directories, ensure your targets configuration directory contains at least one of these subdirectories with the correct structure.

### Check Configuration Priority

If you're not sure which configuration is being used, run:

```bash
ptd config show
```

This shows the active configuration and its source.

## Backward Compatibility

This feature is fully backward compatible:

- If you don't configure `targets_config_dir`, PTD uses the default `./infra` directory
- Existing config files without this setting continue to work
- All existing commands and workflows remain unchanged

## Related Documentation

- [PTD CLI Reference](../cli/PTD_CLI_REFERENCE.md)
