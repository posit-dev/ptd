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

Each infrastructure change is applied by running the relevant ` + "`ptd ensure`" + ` step. Always preview first with ` + "`--dry-run`" + `, then apply.

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

**Preview a step (dry-run):**

` + "```" + `bash
ptd ensure {{.WorkloadName}} --only-steps <step> --dry-run
` + "```" + `

**Apply a step:**

` + "```" + `bash
ptd ensure {{.WorkloadName}} --only-steps <step>
` + "```" + `

## Scaling Product Replicas

1. Edit the product replica count in the site's ` + "`site.yaml`" + `:

` + "```" + `yaml
spec:
  connect:
    replicas: 3
` + "```" + `

2. Preview and apply the sites step:

` + "```" + `bash
ptd ensure {{.WorkloadName}} --only-steps sites --dry-run
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

2. Preview and apply the sites step:

` + "```" + `bash
ptd ensure {{.WorkloadName}} --only-steps sites --dry-run
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
ptd ensure {{.WorkloadName}} --only-steps persistent --dry-run
ptd ensure {{.WorkloadName}} --only-steps persistent
ptd ensure {{.WorkloadName}} --only-steps sites --dry-run
ptd ensure {{.WorkloadName}} --only-steps sites
` + "```" + `

{{- else}}

Azure-managed certificates are handled by the platform. To change the certificate configuration:

1. Update the certificate configuration in ` + "`ptd.yaml`" + `.
2. Re-run the persistent and sites steps:

` + "```" + `bash
ptd ensure {{.WorkloadName}} --only-steps persistent --dry-run
ptd ensure {{.WorkloadName}} --only-steps persistent
ptd ensure {{.WorkloadName}} --only-steps sites --dry-run
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
ptd ensure {{.WorkloadName}} --only-steps persistent --dry-run
ptd ensure {{.WorkloadName}} --only-steps persistent
` + "```" + `

{{- else}}

Database passwords are stored in Azure Key Vault. To rotate:

1. Update the password value in the relevant Key Vault secret.
2. Update the password on the PostgreSQL server to match.
3. Re-run the persistent step to reconcile:

` + "```" + `bash
ptd ensure {{.WorkloadName}} --only-steps persistent --dry-run
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

S3 versioning is enabled on the state bucket. If state is corrupted or accidentally overwritten, restore a previous version:

` + "```" + `bash
aws s3api list-object-versions --bucket ptd-{{.WorkloadName}} --prefix .pulumi/stacks/
aws s3api get-object --bucket ptd-{{.WorkloadName}} --key <state-key> --version-id <version-id> restored-state.json
` + "```" + `

{{- else}}

**State backend:** Azure Blob Storage container in storage account for {{.WorkloadName}}

Blob versioning is enabled on the state container. If state is corrupted or accidentally overwritten, restore a previous version:

` + "```" + `bash
az storage blob list --container-name <container> --account-name <storage-account> --prefix .pulumi/stacks/ --include v
az storage blob download --container-name <container> --account-name <storage-account> --name <state-key> --version-id <version-id> --file restored-state.json
` + "```" + `

{{- end}}

The eject bundle contains a resource inventory that lists every managed resource and its physical ID. Use this inventory to verify state consistency.

To reconcile infrastructure with state, re-run ` + "`ptd ensure`" + `:

` + "```" + `bash
ptd ensure {{.WorkloadName}} --only-steps <step> --dry-run
ptd ensure {{.WorkloadName}} --only-steps <step>
` + "```" + `

## Database Recovery

{{- if eq .Cloud "aws"}}

### RDS Point-in-Time Restore

RDS supports point-in-time recovery within the configured backup retention window.

` + "```" + `bash
aws rds restore-db-instance-to-point-in-time \
  --source-db-instance-identifier <current-instance-id> \
  --target-db-instance-identifier <new-instance-id> \
  --restore-time <timestamp> \
  --region {{.Region}}
` + "```" + `

{{- else}}

### Azure PostgreSQL Point-in-Time Restore

Azure PostgreSQL Flexible Server supports point-in-time recovery within the configured backup retention window.

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
2. Re-run the persistent step to reconcile infrastructure with the new database:

` + "```" + `bash
ptd ensure {{.WorkloadName}} --only-steps persistent --dry-run
ptd ensure {{.WorkloadName}} --only-steps persistent
` + "```" + `

## Storage Recovery

{{- if eq .Cloud "aws"}}

### FSx Backups

FSx OpenZFS creates automatic daily backups. To restore from a backup:

` + "```" + `bash
aws fsx describe-backups --filters Name=file-system-id,Values=<fs-id> --region {{.Region}}
aws fsx create-file-system-from-backup --backup-id <backup-id> --region {{.Region}}
` + "```" + `

### S3 Versioning

S3 buckets have versioning enabled. Recover deleted or overwritten objects:

` + "```" + `bash
aws s3api list-object-versions --bucket <bucket-name> --prefix <key-prefix>
aws s3api get-object --bucket <bucket-name> --key <key> --version-id <version-id> restored-file
` + "```" + `

{{- else}}

### Azure Files / Managed Disk Snapshots

Restore from Azure file share or managed disk snapshots:

` + "```" + `bash
az snapshot list --resource-group {{.ResourceGroup}}
az disk create --resource-group {{.ResourceGroup}} --name <new-disk> --source <snapshot-id>
` + "```" + `

### Blob Versioning

Azure Blob Storage has versioning enabled. Recover previous versions:

` + "```" + `bash
az storage blob list --container-name <container> --account-name <storage-account> --include v
az storage blob download --container-name <container> --account-name <storage-account> --name <key> --version-id <version-id> --file restored-file
` + "```" + `

{{- end}}

## Kubernetes Cluster Recovery

### Total Cluster Loss

If the cluster is completely lost, rebuild from the eject bundle configuration:

` + "```" + `bash
{{- if eq .Cloud "aws"}}
ptd ensure {{.WorkloadName}} --only-steps eks --dry-run
ptd ensure {{.WorkloadName}} --only-steps eks
{{- else}}
ptd ensure {{.WorkloadName}} --only-steps aks --dry-run
ptd ensure {{.WorkloadName}} --only-steps aks
{{- end}}
ptd ensure {{.WorkloadName}} --only-steps clusters --dry-run
ptd ensure {{.WorkloadName}} --only-steps clusters
ptd ensure {{.WorkloadName}} --only-steps helm --dry-run
ptd ensure {{.WorkloadName}} --only-steps helm
ptd ensure {{.WorkloadName}} --only-steps sites --dry-run
ptd ensure {{.WorkloadName}} --only-steps sites
` + "```" + `

### Partial Failure (Node Groups)

{{- if eq .Cloud "aws"}}

If a node group is unhealthy, cordon and replace it:

` + "```" + `bash
ptd workon {{.WorkloadName}} -- kubectl cordon <node>
ptd workon {{.WorkloadName}} -- kubectl drain <node> --ignore-daemonsets --delete-emptydir-data
` + "```" + `

Then re-run the eks step to reconcile the node group:

` + "```" + `bash
ptd ensure {{.WorkloadName}} --only-steps eks --dry-run
ptd ensure {{.WorkloadName}} --only-steps eks
` + "```" + `

{{- else}}

If a node pool is unhealthy, cordon and replace it:

` + "```" + `bash
ptd workon {{.WorkloadName}} -- kubectl cordon <node>
ptd workon {{.WorkloadName}} -- kubectl drain <node> --ignore-daemonsets --delete-emptydir-data
` + "```" + `

Then re-run the aks step to reconcile the node pool:

` + "```" + `bash
ptd ensure {{.WorkloadName}} --only-steps aks --dry-run
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
ptd ensure {{.WorkloadName}} --only-steps sites --dry-run
ptd ensure {{.WorkloadName}} --only-steps sites
` + "```" + `

## Full Environment Rebuild

To rebuild the full environment from the eject bundle configuration:

1. Re-run the infrastructure pipeline in order:

` + "```" + `bash
ptd ensure {{.WorkloadName}} --only-steps bootstrap --dry-run
ptd ensure {{.WorkloadName}} --only-steps bootstrap
ptd ensure {{.WorkloadName}} --only-steps persistent --dry-run
ptd ensure {{.WorkloadName}} --only-steps persistent
ptd ensure {{.WorkloadName}} --only-steps postgres_config --dry-run
ptd ensure {{.WorkloadName}} --only-steps postgres_config
{{- if eq .Cloud "aws"}}
ptd ensure {{.WorkloadName}} --only-steps eks --dry-run
ptd ensure {{.WorkloadName}} --only-steps eks
{{- else}}
ptd ensure {{.WorkloadName}} --only-steps aks --dry-run
ptd ensure {{.WorkloadName}} --only-steps aks
{{- end}}
ptd ensure {{.WorkloadName}} --only-steps clusters --dry-run
ptd ensure {{.WorkloadName}} --only-steps clusters
ptd ensure {{.WorkloadName}} --only-steps helm --dry-run
ptd ensure {{.WorkloadName}} --only-steps helm
ptd ensure {{.WorkloadName}} --only-steps sites --dry-run
ptd ensure {{.WorkloadName}} --only-steps sites
` + "```" + `

2. Restore data from backups:

{{- if eq .Cloud "aws"}}
   - Restore RDS from snapshot or point-in-time recovery (see Database Recovery above).
   - Restore FSx from backup (see Storage Recovery above).
   - Restore S3 objects from versioned copies if needed.
{{- else}}
   - Restore Azure PostgreSQL from point-in-time recovery (see Database Recovery above).
   - Restore Azure Files or managed disks from snapshots (see Storage Recovery above).
   - Restore blob objects from versioned copies if needed.
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
