package eject

import (
	"fmt"
	"io"
	"strings"
	"text/template"
)

// SiteData holds per-site information for runbook templates.
type SiteData struct {
	Name   string
	Domain string
}

// RunbookData contains all data needed to render the operational runbooks.
type RunbookData struct {
	WorkloadName  string
	Cloud         string // "aws" or "azure"
	Region        string
	ClusterName   string
	ResourceGroup string
	Sites         []SiteData
}

var runbookFuncMap = template.FuncMap{
	"upper": strings.ToUpper,
}

var dayToDayOpsTemplate = template.Must(template.New("day-to-day-ops").Funcs(runbookFuncMap).Parse(
	`# Day-to-Day Operations — {{.WorkloadName}}

**Workload:** {{.WorkloadName}}
**Cloud:** {{.Cloud | upper}}
**Region:** {{.Region}}
{{- range .Sites}}
**Site:** {{.Name}} ({{.Domain}})
{{- end}}

## Running PTD Ensure Steps

Each infrastructure change is applied by running the relevant ` + "`ptd ensure`" + ` step. Each step shows a preview of planned changes and prompts for confirmation before applying.

` + "```" + `bash
ptd ensure {{.WorkloadName}} --only-steps <step>
` + "```" + `

| Step | When to Re-Run | What It Changes |
|---|---|---|
{{- if eq .Cloud "aws"}}
| bootstrap | Initial setup only; rarely re-run | S3 state bucket, KMS key, IAM bootstrap roles |
| persistent | VPC, RDS, S3, FSx, IAM, DNS, or TLS changes | VPC, subnets, RDS instance, S3 buckets, FSx filesystem, IAM roles, Route53 zones, ACM certificates |
| postgres_config | Database user/grant changes | PostgreSQL users, databases, grants |
| eks | Cluster or node group changes | EKS cluster, managed node groups, OIDC provider, storage classes |
| clusters | Namespace, RBAC, operator, or ingress controller changes | K8s namespaces, network policies, IAM-to-K8s bindings, Team Operator, Traefik |
| helm | Monitoring, cert-manager, or CSI driver changes | Loki, Grafana, Mimir, Alloy, cert-manager, Secrets Store CSI |
| sites | Product deployment, ingress, or site config changes | TeamSite CRDs, ingress resources, site-specific configuration |
{{- else}}
| bootstrap | Initial setup only; rarely re-run | Blob state container, Key Vault encryption key |
| persistent | VNet, PostgreSQL, storage, Key Vault, or identity changes | VNet, Azure PostgreSQL, storage accounts, NetApp Files, Key Vault, managed identities, NSGs |
| postgres_config | Database user/grant changes | PostgreSQL users, databases, grants |
| aks | Cluster or node pool changes | AKS cluster, node pools, managed identity, storage classes |
| clusters | Namespace, RBAC, operator, or ingress controller changes | K8s namespaces, network policies, workload identity bindings, Team Operator, Traefik |
| helm | Monitoring, cert-manager, or CSI driver changes | Loki, Grafana, Mimir, Alloy, cert-manager, Secrets Store CSI |
| sites | Product deployment, ingress, or site config changes | TeamSite CRDs, ingress resources, site-specific configuration |
{{- end}}

## Scaling Product Replicas

1. Edit the product replica count in the site's ` + "`site.yaml`" + `:

` + "```" + `yaml
spec:
  connect:
    replicas: 3
` + "```" + `

2. Run the sites step:

` + "```" + `bash
ptd ensure {{.WorkloadName}} --only-steps sites
` + "```" + `

3. Verify the new replica count:

` + "```" + `bash
ptd workon {{.WorkloadName}} -- kubectl get pods -n posit-team -l app.kubernetes.io/managed-by=team-operator
` + "```" + `

## Updating Product Versions

1. Edit the product image tag in the site's ` + "`site.yaml`" + `:

` + "```" + `yaml
spec:
  connect:
    image: ghcr.io/rstudio/rstudio-connect:2025.01.0
` + "```" + `

2. Run the sites step:

` + "```" + `bash
ptd ensure {{.WorkloadName}} --only-steps sites
` + "```" + `

3. Verify pods roll to the new version:

` + "```" + `bash
ptd workon {{.WorkloadName}} -- kubectl rollout status deployment -n posit-team -l app.kubernetes.io/managed-by=team-operator
` + "```" + `

## Rotating TLS Certificates

### ACM/Azure-Managed Certificates

{{- if eq .Cloud "aws"}}

ACM certificates auto-renew when DNS validation records are in place. To change the certificate:

1. Update the certificate configuration in ` + "`ptd.yaml`" + `.
2. Re-run the persistent and sites steps:

` + "```" + `bash
ptd ensure {{.WorkloadName}} --only-steps persistent
ptd ensure {{.WorkloadName}} --only-steps sites
` + "```" + `

{{- else}}

Azure-managed certificates are handled by the platform. To change the certificate configuration:

1. Update the certificate configuration in ` + "`ptd.yaml`" + `.
2. Re-run the persistent and sites steps:

` + "```" + `bash
ptd ensure {{.WorkloadName}} --only-steps persistent
ptd ensure {{.WorkloadName}} --only-steps sites
` + "```" + `

{{- end}}

### cert-manager Certificates

cert-manager automatically renews certificates before expiry. To force renewal:

` + "```" + `bash
ptd workon {{.WorkloadName}} -- kubectl delete certificate <cert-name> -n posit-team
` + "```" + `

cert-manager will detect the missing certificate and issue a new one.

## Rotating Secrets

### Database Passwords

{{- if eq .Cloud "aws"}}

Database passwords are stored in AWS Secrets Manager. To rotate:

1. Update the password value in the relevant secret in Secrets Manager.
2. Update the password on the PostgreSQL server to match.
3. Re-run the persistent step to reconcile:

` + "```" + `bash
ptd ensure {{.WorkloadName}} --only-steps persistent
` + "```" + `

{{- else}}

Database passwords are stored in Azure Key Vault. To rotate:

1. Update the password value in the relevant Key Vault secret.
2. Update the password on the PostgreSQL server to match.
3. Re-run the persistent step to reconcile:

` + "```" + `bash
ptd ensure {{.WorkloadName}} --only-steps persistent
` + "```" + `

{{- end}}

### Product Licenses

Update the license key in the secret store ({{if eq .Cloud "aws"}}Secrets Manager{{else}}Key Vault{{end}}) and restart the affected product:

` + "```" + `bash
ptd workon {{.WorkloadName}} -- kubectl rollout restart deployment/<product>-deployment -n posit-team
` + "```" + `

### RSA Keys

**Warning:** Rotating RSA keys for Connect or Package Manager will invalidate all content signed with the previous key. Plan accordingly.

Update the key in the secret store and restart the affected product.

## Checking Workload Health

### Using ptd workon

` + "```" + `bash
ptd workon {{.WorkloadName}} -- kubectl get pods -n posit-team
ptd workon {{.WorkloadName}} -- kubectl get pods -n posit-team-system
ptd workon {{.WorkloadName}} -- kubectl get ingressroute -n posit-team
` + "```" + `

### Using kubectl directly

{{- if eq .Cloud "aws"}}

` + "```" + `bash
aws eks update-kubeconfig --name {{.ClusterName}} --region {{.Region}}
kubectl get pods -n posit-team
kubectl get pods -n posit-team-system
kubectl get ingressroute -n posit-team
` + "```" + `

{{- else}}

` + "```" + `bash
az aks get-credentials --resource-group {{.ResourceGroup}} --name {{.ClusterName}}
kubectl get pods -n posit-team
kubectl get pods -n posit-team-system
kubectl get ingressroute -n posit-team
` + "```" + `

{{- end}}

## Restarting Products

Restart a product deployment:

` + "```" + `bash
ptd workon {{.WorkloadName}} -- kubectl rollout restart deployment/<product>-deployment -n posit-team
` + "```" + `

Monitor the rollout:

` + "```" + `bash
ptd workon {{.WorkloadName}} -- kubectl rollout status deployment/<product>-deployment -n posit-team
` + "```" + `

Using kubectl directly:

` + "```" + `bash
kubectl rollout restart deployment/<product>-deployment -n posit-team
kubectl rollout status deployment/<product>-deployment -n posit-team
` + "```" + `
`))

var disasterRecoveryTemplate = template.Must(template.New("disaster-recovery").Funcs(runbookFuncMap).Parse(
	`# Disaster Recovery — {{.WorkloadName}}

**Workload:** {{.WorkloadName}}
**Cloud:** {{.Cloud | upper}}
**Region:** {{.Region}}
{{- range .Sites}}
**Site:** {{.Name}} ({{.Domain}})
{{- end}}

## Pulumi State Recovery

{{- if eq .Cloud "aws"}}

**State backend:** S3 bucket ` + "`ptd-{{.WorkloadName}}`" + ` in {{.Region}}

{{- else}}

**State backend:** Azure Blob Storage container in storage account for {{.WorkloadName}}

{{- end}}

The state bucket does not have object versioning enabled. If Pulumi state is corrupted or lost, recovery options are:

1. **Re-run ` + "`ptd ensure`" + `** — Pulumi will detect drift between state and actual infrastructure and reconcile. This is the primary recovery path.
2. **Use the eject bundle resource inventory** — ` + "`state/resource-inventory.json`" + ` lists every managed resource with its physical ID. This can guide manual re-import if needed.

` + "```" + `bash
ptd ensure {{.WorkloadName}} --only-steps <step>
` + "```" + `

**Prevention:** Consider enabling versioning on the state bucket post-eject so you can recover from accidental state overwrites.

## Database Recovery

{{- if eq .Cloud "aws"}}

### RDS Point-in-Time Restore

RDS automated backups are enabled with a 7-day retention window. Point-in-time restore creates a new DB instance from any point within that window.

` + "```" + `bash
aws rds restore-db-instance-to-point-in-time \
  --source-db-instance-identifier <current-instance-id> \
  --target-db-instance-identifier <new-instance-id> \
  --restore-time <timestamp> \
  --region {{.Region}}
` + "```" + `

To restore from a manual snapshot instead:

` + "```" + `bash
aws rds describe-db-snapshots --db-instance-identifier <instance-id> --region {{.Region}}
aws rds restore-db-instance-from-db-snapshot \
  --db-snapshot-identifier <snapshot-id> \
  --db-instance-identifier <new-instance-id> \
  --region {{.Region}}
` + "```" + `

{{- else}}

### Azure PostgreSQL Point-in-Time Restore

Azure PostgreSQL Flexible Server has automated backups with the default 7-day retention window. Point-in-time restore creates a new server from any point within that window.

` + "```" + `bash
az postgres flexible-server restore \
  --resource-group {{.ResourceGroup}} \
  --name <new-server-name> \
  --source-server <current-server-name> \
  --restore-time <timestamp>
` + "```" + `

{{- end}}

### Post-Restore Steps

1. Update the database endpoint in the secret store ({{if eq .Cloud "aws"}}Secrets Manager{{else}}Key Vault{{end}}) if the restored instance has a new hostname.
2. Re-run the persistent step to reconcile Pulumi state with the new database:

` + "```" + `bash
ptd ensure {{.WorkloadName}} --only-steps persistent
` + "```" + `

## Storage Recovery

{{- if eq .Cloud "aws"}}

### FSx OpenZFS

FSx OpenZFS has automatic daily backups with a 30-day retention window.

List available backups:

` + "```" + `bash
aws fsx describe-backups --filters Name=file-system-id,Values=<fs-id> --region {{.Region}}
` + "```" + `

Restore from a backup (creates a new filesystem):

` + "```" + `bash
aws fsx create-file-system-from-backup --backup-id <backup-id> --region {{.Region}}
` + "```" + `

After restore, update the FSx DNS name in the workload secret and re-run the persistent step.

### S3 Buckets

S3 data buckets (chronicle, packagemanager) do not have versioning enabled. Deleted or overwritten objects cannot be recovered from S3 alone.

**Prevention:** Consider enabling versioning on critical data buckets post-eject.

{{- else}}

### Azure Storage

Azure storage accounts (file shares, blob containers) do not have soft delete or versioning enabled by default. Deleted or overwritten data cannot be recovered from Azure Storage alone.

**Prevention:** Consider enabling blob soft delete and versioning on critical storage accounts post-eject.

{{- end}}

## Kubernetes Cluster Recovery

### Total Cluster Loss

Persistent data (database, storage) survives cluster loss. Rebuild the cluster and redeploy:

` + "```" + `bash
{{- if eq .Cloud "aws"}}
ptd ensure {{.WorkloadName}} --only-steps eks
{{- else}}
ptd ensure {{.WorkloadName}} --only-steps aks
{{- end}}
ptd ensure {{.WorkloadName}} --only-steps clusters
ptd ensure {{.WorkloadName}} --only-steps helm
ptd ensure {{.WorkloadName}} --only-steps sites
` + "```" + `

### Partial Failure (Node Groups)

{{- if eq .Cloud "aws"}}

If a node group is unhealthy, cordon and drain the affected nodes, then re-run the eks step:

` + "```" + `bash
ptd workon {{.WorkloadName}} -- kubectl cordon <node>
ptd workon {{.WorkloadName}} -- kubectl drain <node> --ignore-daemonsets --delete-emptydir-data
ptd ensure {{.WorkloadName}} --only-steps eks
` + "```" + `

{{- else}}

If a node pool is unhealthy, cordon and drain the affected nodes, then re-run the aks step:

` + "```" + `bash
ptd workon {{.WorkloadName}} -- kubectl cordon <node>
ptd workon {{.WorkloadName}} -- kubectl drain <node> --ignore-daemonsets --delete-emptydir-data
ptd ensure {{.WorkloadName}} --only-steps aks
` + "```" + `

{{- end}}

### Stuck Pods

Delete stuck pods to let the controller recreate them:

` + "```" + `bash
ptd workon {{.WorkloadName}} -- kubectl delete pod <pod-name> -n posit-team
` + "```" + `

If a deployment is stuck, restart it:

` + "```" + `bash
ptd workon {{.WorkloadName}} -- kubectl rollout restart deployment/<deployment-name> -n posit-team
` + "```" + `

## DNS and Ingress Recovery

1. Verify DNS resolution:

` + "```" + `bash
{{- range .Sites}}
dig {{.Domain}}
{{- end}}
` + "```" + `

2. Check load balancer health:

` + "```" + `bash
{{- if eq .Cloud "aws"}}
aws elbv2 describe-target-health --target-group-arn <target-group-arn> --region {{.Region}}
{{- else}}
az network lb show --resource-group {{.ResourceGroup}} --name <lb-name>
{{- end}}
` + "```" + `

3. Check Traefik IngressRoutes:

` + "```" + `bash
ptd workon {{.WorkloadName}} -- kubectl get ingressroute -n posit-team
ptd workon {{.WorkloadName}} -- kubectl describe ingressroute -n posit-team
` + "```" + `

4. Check TLS certificates:

` + "```" + `bash
ptd workon {{.WorkloadName}} -- kubectl get certificate -n posit-team
ptd workon {{.WorkloadName}} -- kubectl describe certificate -n posit-team
` + "```" + `

5. If DNS or ingress is misconfigured, re-run the sites step:

` + "```" + `bash
ptd ensure {{.WorkloadName}} --only-steps sites
` + "```" + `

## Full Environment Rebuild

To rebuild the full environment from the eject bundle configuration:

1. Re-run the full infrastructure pipeline:

` + "```" + `bash
ptd ensure {{.WorkloadName}}
` + "```" + `

   This runs all steps in order (bootstrap through sites), including any custom steps.

2. Restore data from backups:

{{- if eq .Cloud "aws"}}
   - Restore RDS from snapshot or point-in-time recovery (see Database Recovery above).
   - Restore FSx from backup (see Storage Recovery above).
   - S3 data buckets have no versioning — data loss is permanent unless you have external backups.
{{- else}}
   - Restore Azure PostgreSQL from point-in-time recovery (see Database Recovery above).
   - Azure storage has no versioning or soft delete — data loss is permanent unless you have external backups.
{{- end}}

3. Re-populate manual secrets:

   The following secrets must be manually re-entered in {{if eq .Cloud "aws"}}Secrets Manager{{else}}Key Vault{{end}}:
   - Product license keys (Connect, Workbench, Package Manager)
   - OIDC client secrets
   - Any other manually-managed secrets listed in the eject bundle's secrets inventory
`))

// GenerateRunbooks renders both operational runbooks and returns them as a map
// keyed by filename.
func GenerateRunbooks(data *RunbookData) (map[string]string, error) {
	results := make(map[string]string, 2)

	ops, err := renderTemplate(dayToDayOpsTemplate, data)
	if err != nil {
		return nil, fmt.Errorf("failed to render day-to-day-ops runbook: %w", err)
	}
	results["day-to-day-ops.md"] = ops

	dr, err := renderTemplate(disasterRecoveryTemplate, data)
	if err != nil {
		return nil, fmt.Errorf("failed to render disaster-recovery runbook: %w", err)
	}
	results["disaster-recovery.md"] = dr

	return results, nil
}

func renderTemplate(tmpl *template.Template, data *RunbookData) (string, error) {
	var buf strings.Builder
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// RenderDayToDayOps writes the day-to-day operations runbook to the given writer.
func RenderDayToDayOps(w io.Writer, data *RunbookData) error {
	return dayToDayOpsTemplate.Execute(w, data)
}

// RenderDisasterRecovery writes the disaster recovery runbook to the given writer.
func RenderDisasterRecovery(w io.Writer, data *RunbookData) error {
	return disasterRecoveryTemplate.Execute(w, data)
}
