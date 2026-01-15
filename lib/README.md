# PTD Libraries

The `lib` directory contains shared Go libraries used by the PTD CLI.

## Packages

### Cloud Providers
- `aws/` - AWS SDK wrappers, credentials management, EKS, ECR, IAM utilities
- `azure/` - Azure SDK wrappers, credentials, AKS utilities

### Infrastructure
- `pulumi/` - Pulumi automation API integration
- `steps/` - Deployment step orchestration (EKS, AKS, Helm, sites)
- `types/` - Configuration types (workload, control room)

### Utilities
- `helpers/` - Common utility functions
- `secrets/` - Secret management
- `proxy/` - Cluster proxy implementation
- `containers/` - Container image utilities
- `customization/` - Custom step manifest handling
- `consts/` - Shared constants

### Testing
- `testdata/` - Test fixtures and mock data

## Testing

```bash
just test-lib
```

## Usage

These libraries are imported by the CLI (`cmd/`) and are not intended for direct external use.
