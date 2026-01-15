# PTD CLI

The `cmd` directory contains the main PTD command-line interface implementation in Go.

## Building

```bash
# From repository root
just build-cmd

# The binary is created at .local/bin/ptd
```

## Structure

- `main.go` - Entry point and CLI setup
- `ensure.go` - The `ptd ensure` command for deploying workloads
- `proxy.go` - The `ptd proxy` command for cluster access
- `workon.go` - The `ptd workon` command for deployment status
- `admin.go` - Administrative commands
- `assume.go` - AWS role assumption
- `config.go` - Configuration management
- `k9s.go` - K9s integration
- `hash.go` - Hash utilities
- `version.go` - Version information
- `internal/` - Internal packages

## Testing

```bash
just test-cmd
```

## Usage

See the [CLI Reference](../docs/cli/PTD_CLI_REFERENCE.md) for complete documentation.
