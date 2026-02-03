# Configuration Reference

This document describes the configuration options for PTD deployments.

## Configuration Files

PTD uses YAML configuration files organized by deployment type:

```
targets/
├── __ctrl__/                    # Control room configurations
│   └── <control-room-name>/
│       └── ptd.yaml
│
└── __work__/                    # Workload configurations
    └── <workload-name>/
        ├── ptd.yaml             # Workload infrastructure
        └── site_<name>/
            └── site.yaml        # Site (products) configuration
```

## Control Room Configuration

**File:** `__ctrl__/<name>/ptd.yaml`

```yaml
apiVersion: posit.team/v1
kind: AWSControlRoomConfig
spec:
  # Required: AWS account ID
  account_id: "123456789012"

  # Required: AWS region
  region: us-east-2

  # Required: Primary domain
  domain: control-room.example.com

  # Required: External access domain
  front_door: cr.example.com

  # Amazon Elastic Kubernetes Service (EKS) cluster version
  eks_k8s_version: "1.33"

  # Enable Tailscale for remote access
  tailscale_enabled: true

  # IAM principals allowed to assume admin role
  trusted_principals:
    - admin@example.com
    - arn:aws:iam::123456789012:role/github-actions

  # Resource tags for all AWS resources
  resource_tags:
    rs:owner: team@example.com
    rs:project: posit-team
    rs:environment: production
```

## Workload Configuration

**File:** `__work__/<name>/ptd.yaml`

```yaml
apiVersion: posit.team/v1
kind: AWSWorkloadConfig
spec:
  # Required: AWS account ID
  account_id: "123456789012"

  # Required: Control room reference
  control_room_account_id: "123456789012"
  control_room_cluster_name: control-room-production
  control_room_domain: cr.example.com

  # Required: AWS region
  region: us-east-2

  # RDS PostgreSQL configuration
  db_engine_version: "15.12"
  db_instance_class: db.m5d.large
  db_performance_insights_enabled: true
  db_deletion_protection: true
  db_max_allocated_storage: 1024

  # Additional databases
  extra_postgres_dbs:
    - analytics

  # FSx for OpenZFS (shared storage)
  fsx_openzfs_storage_capacity: 900      # GB
  fsx_openzfs_throughput_capacity: 320   # MB/s

  # Domain configuration
  domain_source: ANNOTATION_JSON  # or ROUTE53_PRIVATE_ZONE

  # Authentication
  keycloak_enabled: false

  # Cluster autoscaling
  autoscaling_enabled: true

  # EKS clusters (supports blue/green)
  clusters:
    "20250115":  # Date-based naming recommended
      spec:
        cluster_version: "1.33"
        mp_min_size: 3
        mp_max_size: 10
        mp_instance_type: r6a.2xlarge
        root_disk_size: 200
        routing_weight: "100"  # For blue/green: 0-255
        components:
          traefik_forward_auth_version: "0.0.14"

  # Sites in this workload
  sites:
    main:
      spec:
        domain: analytics.example.com
        use_traefik_forward_auth: false
    dev:
      spec:
        domain: analytics-dev.example.com
```

## Site Configuration

**File:** `__work__/<workload>/site_<name>/site.yaml`

```yaml
apiVersion: core.posit.team/v1beta1
kind: Site
spec:
  # Disable pre-pulling images
  disablePrePullImages: true

  # Posit Workbench (IDE)
  workbench:
    replicas: 2
    image: ghcr.io/rstudio/rstudio-workbench:ubuntu2204-2026.01.0
    defaultSessionImage: ghcr.io/rstudio/workbench-session:ubuntu2204-r4.4.3_4.3.3-py3.12.11_3.11.13
    imagePullPolicy: Always
    domainPrefix: dev  # -> dev.analytics.example.com

    # Authentication
    auth:
      type: "oidc"  # oidc, saml, ldap, pam
      clientId: "your-client-id"
      issuer: "https://your-idp.com"
      scopes:
        - "offline_access"
        - "groups"

    createUsersAutomatically: true

    # API access
    apiSettings:
      workbenchApiEnabled: 1
      workbenchApiAdminEnabled: 1

    # Session timeouts (hours)
    vsCodeConfig:
      sessionTimeoutKillHours: 4
    positronConfig:
      sessionTimeoutKillHours: 4

  # Posit Connect (Publishing)
  connect:
    replicas: 2
    image: ghcr.io/rstudio/rstudio-connect:ubuntu2204-2025.12.1
    sessionImage: ghcr.io/rstudio/rstudio-connect-content-init:ubuntu2204-2025.12.1
    imagePullPolicy: Always
    domainPrefix: pub  # -> pub.analytics.example.com

    auth:
      type: "oidc"
      clientId: "your-client-id"
      issuer: "https://your-idp.com"

  # Posit Package Manager
  packageManager:
    image: ghcr.io/rstudio/rstudio-package-manager:ubuntu2204-2025.09.2
    imagePullPolicy: Always
    domainPrefix: pkg  # -> pkg.analytics.example.com

  # Posit Chronicle (Audit Logging)
  chronicle:
    image: ghcr.io/rstudio/chronicle:2025.08.0
    agentImage: ghcr.io/rstudio/chronicle-agent:2025.08.0

  # Custom ingress annotations
  ingressAnnotations:
    nginx.ingress.kubernetes.io/proxy-body-size: "100m"

  # Extra service accounts for custom integrations
  extraSiteServiceAccounts:
    - nameSuffix: custom-app
      annotations:
        eks.amazonaws.com/role-arn: arn:aws:iam::123456789012:role/custom-role
```

## CLI Configuration

**File:** `~/.config/ptd/ptdconfig.yaml`

```yaml
# Path to targets directory
targets_config_dir: /path/to/targets

# Default AWS profile (optional)
aws_profile: my-profile

# Default region (optional)
region: us-east-2
```

## Environment Variables

| Variable | Description |
|----------|-------------|
| `PTD_TARGETS_CONFIG_DIR` | Path to targets directory |
| `PTD_AWS_ACCOUNT_STAGING` | AWS account ID for staging |
| `PTD_AWS_ACCOUNT_PRODUCTION` | AWS account ID for production |
| `AWS_PROFILE` | AWS credential profile to use |
| `AWS_REGION` | Default AWS region |

## Authentication Options

### OIDC (OpenID Connect)

```yaml
auth:
  type: "oidc"
  clientId: "your-client-id"
  issuer: "https://your-idp.com"
  scopes:
    - "offline_access"
    - "groups"
```

### Security Assertion Markup Language (SAML)

```yaml
auth:
  type: "saml"
  idpMetadataUrl: "https://your-idp.com/metadata"
```

### Lightweight Directory Access Protocol (LDAP)

```yaml
auth:
  type: "ldap"
  ldapUrl: "ldap://ldap.example.com"
  ldapBindDn: "cn=admin,dc=example,dc=com"
```

## Cloud Provider Integrations

### Databricks

```yaml
workbench:
  databricks:
    my-workspace:
      name: "Databricks Workspace"
      url: "https://workspace.cloud.databricks.com"
      clientId: "databricks-oauth-client-id"
```

### Snowflake

```yaml
workbench:
  snowflake:
    clientId: "snowflake-client-id"
    accountId: account-name
```

## See Also

- [Getting Started](GETTING_STARTED.md)
- [CLI Reference](cli/PTD_CLI_REFERENCE.md)
- [Examples](../examples/)
