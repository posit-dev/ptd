# Known Issues and Limitations

This document describes known design constraints, problems, and rough edges in the PTD CLI. Understanding these limitations will help set appropriate expectations when using the tool.

## Overview

PTD is a complex system that orchestrates infrastructure, Kubernetes resources, and Posit Team products. While we strive for a smooth user experience, there are known limitations and quirks that users should be aware of.


## Infrastructure Management

### Control Room and Workload Coupling

Workloads have a required dependency on control rooms and cannot be deployed independently. This coupling has several practical implications:

**Required Configuration:**
Every workload must explicitly reference its control room in `ptd.yaml`:
- `control_room_account_id`: The AWS account ID or Azure subscription ID of the control room
- `control_room_cluster_name`: The name of the control room cluster
- `control_room_domain`: The domain where control room services are hosted
- `control_room_region`: The region where the control room is deployed

**Why This Matters:**
- Workloads send metrics to the control room's Mimir instance at `https://mimir.{control_room_domain}/api/v1/push`
- This monitoring integration is not optional and requires valid control room configuration
- Authentication secrets must be accessible from the control room
- PTD will not validate or deploy a workload configuration without these fields

**What's Independent:**
Despite this coupling, workloads run their own infrastructure independently:
- Separate Kubernetes clusters (EKS/AKS)
- Their own storage (RDS, FSx/Azure Files)
- Isolated networking (VPCs/VNets)
- Self-contained application deployments
- Independent Pulumi state management

**Bottom Line:**
You cannot deploy a standalone workload without first having a control room deployed. The control room provides centralized observability, and this architectural decision prioritizes operational visibility over deployment flexibility. In the future this may be made more flexible such that workloads can configure where they send metrics and not require the concept of a control room.



### AWS Certificate Manager Validation

**The Problem:**
When deploying AWS workloads, the persistent step attempts to provision TLS certificates through AWS Certificate Manager (ACM) and validate them automatically. On the first deployment, this validation typically fails because you haven't configured DNS delegation yet.

**Why It Happens:**
- ACM uses DNS-based validation to verify domain ownership
- The workload creates DNS records for certificate validation
- However, you must manually configure DNS delegation from your parent domain to the workload's hosted zone
- Until DNS delegation is in place, ACM cannot complete the DNS validation challenge
- This causes the persistent step to hang or timeout waiting for certificate validation

**The Solution:**
1. Let the initial deployment proceed (it will create the necessary DNS records and hosted zone)
2. Configure DNS delegation from your parent domain to point to the workload's Route53 hosted zone name servers
3. Once DNS delegation is properly configured, ACM will be able to validate the certificates

**Workaround:**
If you want to deploy infrastructure first and handle certificate validation later, you can disable automatic validation by setting the `certificate_validation_enabled` field to `false` in your site configuration within `ptd.yaml`:

```yaml
sites:
  - name: my-site
    certificate_validation_enabled: false
```

With this setting disabled, the infrastructure will deploy successfully without waiting for certificate validation. You can enable it later once DNS delegation is configured and run `ptd ensure` again to complete the validation process.




## Workarounds and Best Practices

### Direct Pulumi Stack Access with `ptd workon`

**When You Need It:**
Sometimes you need to directly interact with Pulumi stack state for a specific deployment step - whether to inspect resources, manually fix state issues, or perform advanced Pulumi operations that aren't exposed through the PTD CLI.

**The Command:**
```bash
ptd workon {target} {step}
```

This command drops you into an authenticated shell session configured for the specified target and step.

**What It Does:**
- Sets up all necessary authentication (AWS credentials, Pulumi state backend access, etc.)
- Configures the environment to point to the correct Pulumi stack for that step
- Activates the Python virtual environment if needed
- Allows you to run Pulumi CLI commands directly against the stack

**Common Use Cases:**
- **Inspect stack state**: `pulumi stack` to view current state
- **View outputs**: `pulumi stack output` to see stack outputs
- **Export state**: `pulumi stack export` to examine full state
- **Import resources**: `pulumi import` to bring existing resources under management
- **Refresh state**: `pulumi refresh` to sync state with actual infrastructure
- **Manual state edits**: `pulumi state delete` or similar state surgery operations

**Example Workflow:**
```bash
# Enter the authenticated shell for the persistent step of workload01
ptd workon workload01 persistent

# Now you're in a shell where you can use Pulumi CLI directly
pulumi stack
pulumi stack output
pulumi refresh

# Exit when done
exit
```

**Important Notes:**
- Be careful when manually modifying stack state - incorrect changes can cause deployment issues
- Changes made directly via Pulumi CLI may be overwritten by subsequent `ptd ensure` runs if they conflict with your configuration
- This is an advanced troubleshooting tool - use it when the standard PTD commands aren't sufficient


### External Secrets Operator: ClusterSecretStore Fails on First Run

**The Problem:**
When enabling `enable_external_secrets_operator` on a fresh cluster, the `ClusterSecretStore` resource
may fail to apply with `no matches for kind "ClusterSecretStore"`. This happens because Pulumi registers
the ESO HelmChart CR but the CRDs installed by the chart have not yet converged before Pulumi attempts
to create the `ClusterSecretStore`.

**Why It Happens:**
`depends_on` the HelmChart CR only ensures the CR is accepted by the API server, not that the ESO
controller has finished installing its CRDs. On a fresh cluster, CRD propagation can take several
minutes. Pulumi will retry for up to 10 minutes via `CustomTimeouts(create="10m")`, but may still
time out on very slow clusters or under resource pressure.

**The Solution:**
Re-run `ptd ensure` after the initial failure. By that point the CRDs will be available and the
`ClusterSecretStore` will apply successfully.

---
