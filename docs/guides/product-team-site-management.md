# Guide: Product Team Site Management

## Overview

This guide is intended for product teams who have kubectl access to the `duplicado03-staging` PTD cluster. It covers how to safely update product images for testing, and how to request configuration changes.

## Shared Environment Warning

**duplicado03-staging is a shared development environment.** Multiple product teams (Workbench, Connect, Platform) use this cluster simultaneously. Actions you take can affect other teams' work. Please exercise caution and coordinate with other teams when making changes that could impact shared resources.

## Dangers of Namespace Admin Access

As an admin in the `posit-team` namespace, you have broad permissions. Here are actions that can cause serious problems:

### Do NOT do these things directly via kubectl:

| Action | Risk |
|--------|------|
| `kubectl delete site <name>` | **Catastrophic** - Deletes the entire site, all products go down |
| `kubectl delete pod/deployment/service` | Pods restart, but can cause temporary outages for all users |
| Editing Deployments, Services, ConfigMaps | Team Operator will conflict with your changes during reconciliation |
| Editing Site spec fields other than images | Can break authentication, networking, or product functionality |
| Modifying Secrets | Can break database connections, auth, or inter-service communication |

### The Team Operator is constantly reconciling

The Team Operator watches the Site CRD and reconciles the cluster state to match it. If you manually edit Kubernetes resources (Deployments, Services, etc.), your changes will either be:
- **Overwritten** by the operator on the next reconcile cycle
- **Cause conflicts** that result in unexpected behavior

**Bottom line:** Only edit the Site spec, and only the image-related fields for testing.

## Prerequisites

- kubectl access to the `duplicado03-staging` PTD cluster

## Updating Product Images

### 1. List Available Sites

```bash
kubectl get sites -n posit-team
```

Example output:
```
NAME    AGE
main    45d
test    12d
```

### 2. Edit the Site Spec

Open the Site spec in your editor:

```bash
kubectl edit site main -n posit-team
```

### 3. Update Product Images

Locate the product section you need to update and **ONLY modify the image related fields**:

#### For Workbench:
```yaml
spec:
  workbench:
    defaultSessionImage: ghcr.io/rstudio/rstudio-workbench:ubuntu2204-2025.12.0
    image: ghcr.io/rstudio/rstudio-workbench:ubuntu2204-2025.12.0
    imagePullPolicy: Always  # Set to Always to ensure latest image is pulled
```

#### For Connect:
```yaml
spec:
  connect:
    image: ghcr.io/rstudio/rstudio-connect:ubuntu2204-2025.12.0
    sessionImage: ghcr.io/rstudio/rstudio-connect-content-init:ubuntu2204-2025.12.0
    imagePullPolicy: Always
```

#### For Package Manager:
```yaml
spec:
  packageManager:
    image: ghcr.io/rstudio/rstudio-package-manager:ubuntu2204-2025.12.0
    imagePullPolicy: Always
```

#### For Chronicle:
```yaml
spec:
  chronicle:
    agentImage: ghcr.io/rstudio/chronicle-agent:2025.08.0
    image: ghcr.io/rstudio/chronicle:2025.08.0
    imagePullPolicy: Always
```

### 4. Save and Exit

After making your changes:
- In vi/vim: Press `ESC`, then type `:wq` and press `Enter`
- The Team Operator will automatically detect the change and reconcile

### 5. Monitor the Rollout

Watch the pods restart with your new image:

```bash
# Watch all pods in the namespace
kubectl get pods -n posit-team -w

# Or watch specific product pods
kubectl get pods -n posit-team -l app.kubernetes.io/name=workbench -w
kubectl get pods -n posit-team -l app.kubernetes.io/name=connect -w
kubectl get pods -n posit-team -l app.kubernetes.io/name=package-manager -w
```

### 6. Verify the Update

Check that pods are running with your new image:

```bash
kubectl describe pod <pod-name> -n posit-team | grep Image:
```

## Changing Site Configuration (Non-Image Changes)

If you need to change anything beyond images (replicas, auth settings, feature flags, experimental features, etc.), **do not edit the Site spec directly via kubectl**.

Instead, open a PR against the appropriate `site.yaml` file in this repository:

### Site Configuration Files

| Team | Site | Path |
|------|------|------|
| Workbench | main | `infra/__work__/duplicado03-staging/site_main/site.yaml` |
| Connect | connect | `infra/__work__/duplicado03-staging/site_connect/site.yaml` |
| Connect | connect-dev | `infra/__work__/duplicado03-staging/site_connect-dev/site.yaml` |
| Platform | chronicle | `infra/__work__/duplicado03-staging/site_chronicle/site.yaml` |
| Platform | chronicle-dev | `infra/__work__/duplicado03-staging/site_chronicle-dev/site.yaml` |

### Why PRs instead of direct edits?

1. **Change tracking** - PRs provide a record of what changed and why
2. **Review** - PTD team can catch potential issues before they affect the cluster
3. **Rollback** - Easy to revert via git if something goes wrong
4. **Coordination** - Other teams can see what's changing in the shared environment

## Example Site Spec

Below is an **example Site spec with fake data** showing the structure and where product images are specified:

```yaml
apiVersion: core.posit.team/v1beta1
kind: Site
metadata:
  name: main
  namespace: posit-team
  labels:
    app.kubernetes.io/name: site
    posit.team/site-name: main
spec:
  awsAccountId: "123456789012"

  # ============================================
  # CHRONICLE - UPDATE THIS IMAGE FOR TESTING
  # ============================================
  chronicle:
    agentImage: ghcr.io/rstudio/chronicle-agent:2025.08.0
    image: ghcr.io/rstudio/chronicle:2025.08.0
    s3Bucket: example-bucket-chronicle

  clusterDate: "20250101"

  # ============================================
  # CONNECT - UPDATE THIS IMAGE FOR TESTING
  # ============================================
  connect:
    image: ghcr.io/rstudio/rstudio-connect:ubuntu2204-2025.09.0
    sessionImage: ghcr.io/rstudio/rstudio-connect-content-init:ubuntu2204-2025.09.0
    imagePullPolicy: Always
    replicas: 2
    scheduleConcurrency: 2
    domainPrefix: pub
    auth:
      type: oidc
      issuer: https://example.okta.com
      clientId: abc123example
    experimentalFeatures:
      mailDisplayName: Example Connect
      mailSender: no-reply@example.com

  domain: example.posit.team
  enableFqdnHealthChecks: true

  mainDatabaseCredentialSecret:
    type: aws
    vaultName: arn:aws:secretsmanager:us-east-1:123456789012:secret:example-db-secret

  networkTrust: 100

  # ============================================
  # PACKAGE MANAGER - UPDATE THIS IMAGE FOR TESTING
  # ============================================
  packageManager:
    image: ghcr.io/rstudio/rstudio-package-manager:ubuntu2204-2025.09.0
    imagePullPolicy: Always
    domainPrefix: pkg
    s3Bucket: example-bucket-ppm

  secret:
    type: aws
    vaultName: example-site-secret

  secretType: aws

  siteHome:
    image: 123456789012.dkr.ecr.us-east-1.amazonaws.com/ptd-home@sha256:abcdef1234567890

  volumeSource:
    type: nfs
    dnsName: fs-example123.fsx.us-east-1.amazonaws.com

  # ============================================
  # WORKBENCH - UPDATE THIS IMAGE FOR TESTING
  # ============================================
  workbench:
    image: ghcr.io/rstudio/rstudio-workbench:ubuntu2204-2025.09.0
    imagePullPolicy: Always
    replicas: 2
    domainPrefix: dev
    createUsersAutomatically: true
    auth:
      type: oidc
      issuer: https://example.okta.com
      clientId: xyz789example
    apiSettings:
      workbenchApiEnabled: 1
      workbenchApiAdminEnabled: 1
    experimentalFeatures:
      cpuRequestRatio: "0.6"
      memoryRequestRatio: "0.8"

  workloadCompoundName: example-site
  workloadSecret:
    type: aws
    vaultName: example-workload-secret
```

## Troubleshooting

### Pods not restarting after image update
- Check the Team Operator logs: `kubectl logs -n posit-team deploy/team-operator`
- Verify the image exists and is accessible: `kubectl describe pod <pod-name> -n posit-team`

### Image pull errors
- Ensure `imagePullPolicy: Always` is set
- Verify the cluster has access to your image registry
- Check for typos in the image name/tag

### Site becomes unavailable after edit
- Try rolling back to the previous image
- If rolling back to the previous image does not work, contact the PTD team to restore the previous configuration
