package eject

import (
	"fmt"
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
| helm | Monitoring or CSI driver changes | Loki, Grafana, Mimir, Alloy, Secrets Store CSI |
| sites | Product deployment, ingress, or site config changes | TeamSite CRDs, ingress resources, site-specific configuration |
| persistent_reprise | After eks/cluster changes; run as the final pass | Second persistent pass — completes IRSA trust policies against the cluster OIDC issuer and refreshes the workload secret |
{{- else}}
| bootstrap | Initial setup only; rarely re-run | Blob state container, Key Vault encryption key |
| persistent | VNet, PostgreSQL, storage, Key Vault, or identity changes | VNet, Azure PostgreSQL, storage accounts, NetApp Files, Key Vault, managed identities, NSGs |
| postgres_config | Database user/grant changes | PostgreSQL users, databases, grants |
| aks | Cluster or node pool changes | AKS cluster, node pools, managed identity, storage classes |
| clusters | Namespace, RBAC, operator, ingress controller, or cert-manager changes | K8s namespaces, network policies, workload identity bindings, Team Operator, Traefik, cert-manager (when ` + "`use_lets_encrypt`" + ` is enabled) |
| helm | Monitoring or CSI driver changes | Loki, Grafana, Mimir, Alloy, Secrets Store CSI |
| sites | Product deployment, ingress, or site config changes | TeamSite CRDs, ingress resources, site-specific configuration |
| persistent_reprise | After aks/cluster changes; run as the final pass | Second persistent pass — completes workload identity federation against the cluster OIDC issuer and refreshes the workload secret |
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

## Rotating TLS Certificates

{{- if eq .Cloud "aws"}}

This workload terminates customer-facing TLS at the AWS load balancer (ALB) using **AWS Certificate Manager (ACM)** certificates. cert-manager is **not** deployed on AWS, so the cert-manager instructions you may see elsewhere do not apply here.

How a certificate is managed depends on the workload-level ` + "`hosted_zone_management_enabled`" + ` flag (default ` + "`true`" + `):

**PTD-managed certificates (default — ` + "`hosted_zone_management_enabled: true`" + `)**

PTD creates one ACM certificate per site domain and validates it via DNS records in the Route53 zone it manages. ACM auto-renews these certificates as long as the validation records stay in place — **no operator action is required for routine renewal**.

To change the domain on a site, edit its ` + "`domain`" + ` under ` + "`sites`" + ` in ` + "`ptd.yaml`" + ` (this replaces the certificate, since the ACM cert is keyed to the domain):

` + "```" + `yaml
sites:
  <site>:
    spec:
      domain: connect.example.com   # new domain → new ACM certificate
` + "```" + `

**Customer-managed certificate (bring-your-own — ` + "`hosted_zone_management_enabled: false`" + `)**

When PTD does not manage the hosted zone, you supply an existing ACM certificate instead. Set these fields under the site in ` + "`ptd.yaml`" + `:

` + "```" + `yaml
sites:
  <site>:
    spec:
      domain: connect.example.com
      certificate_arn: arn:aws:acm:{{.Region}}:<account-id>:certificate/<cert-id>
      certificate_validation_enabled: false   # you own validation/renewal in ACM
` + "```" + `

- ` + "`certificate_arn`" + ` — ARN of the ACM certificate to attach to the ALB. Issue or replace the certificate in ACM yourself, then update this value.
- ` + "`certificate_validation_enabled: false`" + ` — disables PTD-driven DNS validation; you are responsible for validating and renewing the certificate in ACM.

**Apply the change (either case):** re-run the ` + "`persistent`" + ` step (creates/updates the ACM cert and publishes its ARN) followed by the ` + "`helm`" + ` step (applies the ARN to the ALB ingress via the ` + "`alb.ingress.kubernetes.io/certificate-arn`" + ` annotation):

` + "```" + `bash
ptd ensure {{.WorkloadName}} --only-steps persistent
ptd ensure {{.WorkloadName}} --only-steps helm
` + "```" + `

{{- else}}

This workload terminates customer-facing TLS at the Traefik ingress using **Let's Encrypt certificates issued by cert-manager** (enabled by ` + "`use_lets_encrypt: true`" + ` on the cluster). There are no Azure platform-managed certificates involved.

cert-manager requests a certificate per site domain through a Let's Encrypt ` + "`ClusterIssuer`" + ` (named ` + "`letsencrypt-<domain>`" + `) and stores it in a TLS secret in the ` + "`traefik`" + ` namespace. It **auto-renews each certificate roughly 30 days before expiry — no operator action is required for routine renewal**.

**Force renewal** (e.g., after a mis-issued or revoked certificate) by deleting the Certificate object; cert-manager re-issues it immediately:

` + "```" + `bash
# Inspect certificates, their readiness, and expiry
ptd workon {{.WorkloadName}} -- kubectl get certificate -n traefik
# Delete one to force re-issuance
ptd workon {{.WorkloadName}} -- kubectl delete certificate <cert-name> -n traefik
` + "```" + `

**Change the domain** on a site by editing its ` + "`domain`" + ` under ` + "`sites`" + ` in ` + "`ptd.yaml`" + ` (or set ` + "`root_domain`" + ` to share a single issuer domain across all sites). Re-run the ` + "`clusters`" + ` step (updates the issuer and ingress) followed by the ` + "`sites`" + ` step:

` + "```" + `yaml
sites:
  <site>:
    spec:
      domain: connect.example.com
` + "```" + `

` + "```" + `bash
ptd ensure {{.WorkloadName}} --only-steps clusters
ptd ensure {{.WorkloadName}} --only-steps sites
` + "```" + `

{{- end}}

## Rotating Secrets

### Product Licenses

Update the license key in the secret store ({{if eq .Cloud "aws"}}Secrets Manager{{else}}Key Vault{{end}}) and restart the affected product:

` + "```" + `bash
ptd workon {{.WorkloadName}} -- kubectl rollout restart deployment/<site>-<product> -n posit-team
` + "```" + `

### RSA Keys

**Warning:** Rotating these keys makes data the product encrypted with the previous key (e.g. stored credentials and variables) unrecoverable. Plan accordingly.

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

Product deployments are named ` + "`<site>-<product>`" + ` (e.g. ` + "`main-connect`" + `, ` + "`main-workbench`" + `, ` + "`main-packagemanager`" + `). List them with:

` + "```" + `bash
ptd workon {{.WorkloadName}} -- kubectl get deploy -n posit-team -l app.kubernetes.io/managed-by=team-operator
` + "```" + `

Restart a product deployment:

` + "```" + `bash
ptd workon {{.WorkloadName}} -- kubectl rollout restart deployment/<site>-<product> -n posit-team
` + "```" + `

Monitor the rollout:

` + "```" + `bash
ptd workon {{.WorkloadName}} -- kubectl rollout status deployment/<site>-<product> -n posit-team
` + "```" + `

Using kubectl directly:

` + "```" + `bash
kubectl rollout restart deployment/<site>-<product> -n posit-team
kubectl rollout status deployment/<site>-<product> -n posit-team
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

The state bucket does not have object versioning enabled. Recovery depends on whether the state is drifted or corrupted (still present) versus lost entirely:

1. **State drifted or corrupted (still present)** — re-run the affected step with ` + "`--refresh`" + ` so Pulumi reconciles its state against the actual cloud resources before applying. This is the primary recovery path whenever state still exists:

` + "```" + `bash
ptd ensure {{.WorkloadName}} --only-steps <step> --refresh
` + "```" + `

2. **State lost entirely** — with no state, Pulumi has no record of the existing resources, so a plain re-run would try to create duplicates of resources that already exist (and fail). Recovery requires re-importing the existing resources into a fresh stack. The "Resource Inventory" section of the handoff document (` + "`../{{.WorkloadName}}_handoff.md`" + `) lists every managed resource with its physical ID to guide that re-import.

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

A point-in-time or snapshot restore creates a **new** {{if eq .Cloud "aws"}}instance{{else}}server{{end}} with a new identifier and hostname that Pulumi state does not track — state still references the original. Products do not read the database host directly: the Team Operator reads it from the ` + "`main-database-url`" + ` key of the workload secret ({{if eq .Cloud "aws"}}` + "`{{.WorkloadName}}.posit.team`" + ` in Secrets Manager{{else}}the workload secret in Key Vault{{end}}) at reconcile time and renders it into each product's configuration. The persistent step writes that secret value from the database resource it tracks in state. Repointing the workload is therefore:

1. Bring the restored {{if eq .Cloud "aws"}}instance{{else}}server{{end}} under Pulumi management by importing it into the persistent stack — a manual state operation via ` + "`ptd workon {{.WorkloadName}} persistent`" + ` (see Pulumi State Recovery). Do **not** simply re-run persistent against the old state: it keeps writing the original hostname, or — if the original is gone — provisions a new, empty {{if eq .Cloud "aws"}}instance{{else}}server{{end}}.
2. Re-run the persistent step so it writes the restored endpoint into the workload secret:

` + "```" + `bash
ptd ensure {{.WorkloadName}} --only-steps persistent
` + "```" + `

3. Restart the Team Operator so it reconciles, re-reads ` + "`main-database-url`" + `, re-renders each product's database configuration, and rolls the product pods. Re-running the sites step alone does **not** trigger this — the Site spec references the secret by name, so its generation is unchanged and the operator does not reconcile.

` + "```" + `bash
ptd workon {{.WorkloadName}} -- kubectl rollout restart deployment/team-operator-controller-manager -n posit-team-system
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

A restored filesystem has a new ID and DNS name that Pulumi state does not track. As with the database, import it into the persistent stack — a manual state operation via ` + "`ptd workon {{.WorkloadName}} persistent`" + ` (see Pulumi State Recovery) — rather than re-running persistent against the old state. Then re-run persistent followed by the sites step:

` + "```" + `bash
ptd ensure {{.WorkloadName}} --only-steps persistent
ptd ensure {{.WorkloadName}} --only-steps sites
` + "```" + `

The persistent step writes the new ` + "`fs-dns-name`" + ` into the workload secret. Unlike the database hostname, the filesystem DNS name is written **into** the TeamSite spec by the sites step, so re-running sites changes the spec and the Team Operator reconciles automatically — remounting products on the restored filesystem (no operator restart needed).

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

Persistent data (database, storage) survives cluster loss. Rebuild the cluster and redeploy. Each step is run with ` + "`--refresh`" + ` because Pulumi state still records the now-deleted cluster resources as existing; refresh reconciles state against reality (resources gone) so the apply recreates them. Each step previews and prompts before applying:

` + "```" + `bash
{{- if eq .Cloud "aws"}}
ptd ensure {{.WorkloadName}} --only-steps eks --refresh
{{- else}}
ptd ensure {{.WorkloadName}} --only-steps aks --refresh
{{- end}}
ptd ensure {{.WorkloadName}} --only-steps clusters --refresh
ptd ensure {{.WorkloadName}} --only-steps helm --refresh
ptd ensure {{.WorkloadName}} --only-steps sites --refresh
ptd ensure {{.WorkloadName}} --only-steps persistent_reprise --refresh
` + "```" + `

A rebuilt cluster has a new OIDC issuer, so the final ` + "`persistent_reprise`" + ` step is required to re-establish {{if eq .Cloud "aws"}}IRSA trust policies{{else}}workload identity federation{{end}} against the new issuer and refresh the workload secret.

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

{{- if eq .Cloud "aws"}}

Customer-facing TLS terminates at the ALB using ACM certificates (cert-manager is not deployed on AWS), so check the certificate in ACM rather than in the cluster:

` + "```" + `bash
aws acm list-certificates --region {{.Region}}
aws acm describe-certificate --certificate-arn <cert-arn> --region {{.Region}}
` + "```" + `

{{- else}}

cert-manager issues certificates into the ` + "`traefik`" + ` namespace, alongside the ingress that references them:

` + "```" + `bash
ptd workon {{.WorkloadName}} -- kubectl get certificate -n traefik
ptd workon {{.WorkloadName}} -- kubectl describe certificate -n traefik
` + "```" + `

{{- end}}

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
