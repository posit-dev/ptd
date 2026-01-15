# Workload Example

This directory contains an example configuration for a PTD Workload with a site.

## What is a Workload?

A Workload is a complete Posit Team deployment including:
- AWS infrastructure (VPC, EKS, RDS, FSx)
- Kubernetes cluster with required components
- One or more Sites (Posit Team environments)

## Structure

```
workload/
├── README.md
├── ptd.yaml           # Main workload configuration
└── site_main/
    └── site.yaml      # Site configuration (Workbench, Connect, Package Manager)
```

## Configuration Files

### `ptd.yaml`
Defines the AWS infrastructure:
- EKS cluster settings
- RDS PostgreSQL database
- FSx for OpenZFS shared storage
- Networking and security

### `site_main/site.yaml`
Defines the Posit Team products:
- Workbench (IDE environment)
- Connect (publishing platform)
- Package Manager (package repository)

## Usage

1. Copy this directory to your infrastructure repository:
   ```bash
   cp -r examples/workload infra/__work__/my-workload
   ```

2. Edit `ptd.yaml` with your infrastructure settings:
   - AWS account IDs
   - Region
   - Cluster sizing
   - Domain names

3. Edit `site_main/site.yaml` with your product settings:
   - Authentication (OIDC/SAML provider)
   - Replicas for high availability
   - Integrations (Databricks, Snowflake)

4. Deploy the workload:
   ```bash
   ptd ensure my-workload
   ```

## Multiple Sites

A workload can have multiple sites for different environments:

```
my-workload/
├── ptd.yaml
├── site_main/
│   └── site.yaml      # Production environment
├── site_dev/
│   └── site.yaml      # Development environment
└── site_staging/
    └── site.yaml      # Staging environment
```

Reference each site in `ptd.yaml`:
```yaml
sites:
  main:
    spec:
      domain: analytics.example.com
  dev:
    spec:
      domain: analytics-dev.example.com
  staging:
    spec:
      domain: analytics-staging.example.com
```

## Required Values to Update

### ptd.yaml

| Field | Description |
|-------|-------------|
| `spec.account_id` | Your workload AWS account ID |
| `spec.control_room_account_id` | Control room AWS account ID |
| `spec.control_room_cluster_name` | Name of your control room |
| `spec.control_room_domain` | Control room domain |
| `spec.region` | AWS region |
| `spec.sites.*.spec.domain` | Domain for each site |

### site.yaml

| Field | Description |
|-------|-------------|
| `spec.workbench.auth.*` | OIDC/SAML provider settings |
| `spec.connect.auth.*` | OIDC/SAML provider settings |
| `spec.*.image` | Container image versions |
