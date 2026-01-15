# PTD Pulumi Infrastructure

The `python-pulumi` directory contains Pulumi Infrastructure-as-Code resources for deploying Posit Team products on AWS and Azure.

## Structure

```
python-pulumi/
├── src/ptd/
│   ├── pulumi_resources/     # Pulumi component resources
│   │   ├── aws_*.py          # AWS infrastructure components
│   │   ├── azure_*.py        # Azure infrastructure components
│   │   └── *.py              # Shared components (Helm, Traefik, etc.)
│   ├── aws_*.py              # AWS-specific utilities
│   ├── azure_*.py            # Azure-specific utilities
│   └── *.py                  # Shared utilities
└── tests/                    # Unit tests
```

## Setup

```bash
cd python-pulumi
uv sync
```

## Testing

```bash
just test-python-pulumi
# or from python-pulumi directory:
uv run pytest
```

## Key Components

### AWS Resources
- VPC, EKS clusters, FSx for OpenZFS
- IAM roles and policies
- RDS PostgreSQL configuration
- Karpenter node provisioning

### Azure Resources
- AKS clusters
- Azure Files CSI
- Key Vault integration

### Shared Resources
- Team Operator deployment
- Traefik ingress
- Cert-manager
- External DNS
- Grafana Alloy monitoring

## Usage

These resources are invoked by the Go CLI via Pulumi automation API. They are not typically run directly.
