package steps

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/posit-dev/ptd/lib/aws"
	"github.com/posit-dev/ptd/lib/helpers"
	"github.com/posit-dev/ptd/lib/types"
	acm "github.com/pulumi/pulumi-aws/sdk/v6/go/aws/acm"
	awsroute53 "github.com/pulumi/pulumi-aws/sdk/v6/go/aws/route53"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"gopkg.in/yaml.v3"
)

// clusterManagedByValue is the posit.team/managed-by tag value Python set on
// every control-room cluster resource (the __name__ of aws_control_room_cluster.py).
const clusterManagedByValue = "ptd.pulumi_resources.aws_control_room_cluster"

// awsClusterParams bundles all pre-fetched data for the AWS control-room cluster
// deploy function.
type awsClusterParams struct {
	compoundName string
	// clusterName is the literal EKS cluster name "default_{compound}-control-plane".
	clusterName               string
	region                    string
	accountID                 string
	iamPermissionsBoundaryARN string
	requiredTags              map[string]string
	cfg                       types.AWSControlRoomConfig
	credentials               *aws.Credentials

	// Live cluster state.
	subnetIDs       []string
	vpcID           string
	clusterSGID     string
	clusterExists   bool
	currentAuthMode string
	kubeconfig      string
	accessEntries   aws.AccessEntryData
	// oidcThumbprint is the OIDC provider CA thumbprint, pre-fetched via
	// aws.GetOIDCThumbprint (replicates Python ptd.oidc.get_thumbprint). Empty on a
	// greenfield cluster that does not exist yet.
	oidcThumbprint string

	// Grafana / Mimir inputs.
	grafanaDBConnection string
	opsgenieKey         string
	mimirSalt           string
	mimirCreds          map[string]string
	wlAccountIDs        []string
	grafanaAlerts       []aws.GrafanaConfigMapFile
	grafanaDashboards   []aws.GrafanaConfigMapFile

	protectPersistentResources bool
}

// buildClusterRequiredTags mirrors AWSControlRoomCluster.required_tags:
//
//	resource_tags | { posit.team/true-name, posit.team/environment } | { posit.team/managed-by }
func buildClusterRequiredTags(cfg types.AWSControlRoomConfig) map[string]string {
	tags := map[string]string{}
	for k, v := range cfg.ResourceTags {
		tags[k] = v
	}
	tags["posit.team/true-name"] = cfg.TrueName
	tags["posit.team/environment"] = cfg.Environment
	tags["posit.team/managed-by"] = clusterManagedByValue
	return tags
}

// awsClusterDeploy is the package-level AWS control-room cluster deploy function,
// callable from tests. It ports aws_control_room_cluster.py:_define_eks in order:
//
//	EKS cluster (+ IAM role + K8s provider + SG access) → control-room launch
//	template + node role + SSM attach + node group → aws-auth/access-entries →
//	gp3 → OIDC → Route53 parent-zone lookup → ACM cert + validation records +
//	CertificateValidation → Traefik (helm) + define_domains → EBS CSI → AWS LBC →
//	metrics-server → secrets-store CSI (+aws provider) → traefik-forward-auth →
//	Grafana → Mimir.
func awsClusterDeploy(ctx *pulumi.Context, _ types.Target, params awsClusterParams) error {
	cfg := params.cfg
	name := params.compoundName

	parentChain := clusterWrapperType + "$ptd:AWSEKSCluster"

	eksCluster, err := aws.NewEKSCluster(ctx, aws.EKSClusterConfig{
		Name:      params.clusterName,
		SubnetIDs: params.subnetIDs,
		Version:   cfg.EKSK8sVersion(),
		Tags:      params.requiredTags,
		EnabledClusterLogTypes: []string{
			"api", "audit", "authenticator", "controllerManager", "scheduler",
		},
		// Control room uses the legacy "silly hack" generated EKS role name (Python
		// passes no eks_role_name), so EksRoleName is left empty.
		IAMPermissionsBoundary:     params.iamPermissionsBoundaryARN,
		ProtectPersistentResources: params.protectPersistentResources,
		OIDCThumbprint:             params.oidcThumbprint,
		ClusterExists:              params.clusterExists,
		CurrentAuthMode:            params.currentAuthMode,
		Kubeconfig:                 params.kubeconfig,
		ProjectName:                clusterOldProjectName,
		ParentTypeChain:            parentChain,
		WrapperTypeChain:           clusterWrapperType,
		SgPrefix:                   name,
		Region:                     params.region,
		Credentials:                params.credentials,
		TailscaleEnabled:           cfg.TailscaleEnabled,
		VpcID:                      params.vpcID,
		ClusterSecurityGroupID:     params.clusterSGID,
		AccessEntries:              params.accessEntries,
		AccountID:                  params.accountID,
	})
	if err != nil {
		return err
	}

	// Node role + SSM attach + control-room node group (Python ordering:
	// with_node_role → _define_node_iam → with_node_group).
	eksCluster.
		WithNodeRole("").
		AttachNodeSSMPolicy().
		WithControlRoomNodeGroup(aws.ControlRoomNodeGroupParams{
			Name:         name,
			InstanceType: cfg.EKSNodeInstanceType(),
			AmiType:      "AL2023_x86_64_STANDARD",
			MinSize:      cfg.EKSNodeGroupMin(),
			MaxSize:      cfg.EKSNodeGroupMax(),
			DesiredSize:  cfg.EKSNodeGroupMax(), // Python passes eks_node_group_max as desired.
			Version:      cfg.EKSK8sVersion(),
			Tags:         params.requiredTags,
		})

	// aws-auth ConfigMap OR EKS access entries.
	useAccessEntries := cfg.UsesEksAccessEntries()
	authParams := aws.AwsAuthParams{UseEksAccessEntries: useAccessEntries}
	if cfg.EksAccessEntries != nil {
		authParams.AdditionalAccessEntries = cfg.EksAccessEntries.AdditionalEntries
		authParams.IncludePoweruser = cfg.EksAccessEntries.IncludeSameAccountPoweruser
	}
	eksCluster.WithAwsAuth(authParams)

	// gp3 storage class, then OIDC provider.
	eksCluster.WithGp3().WithOidcProvider()

	if err := eksCluster.Err(); err != nil {
		return err
	}

	// ── ACM certificate + Route53 validation ────────────────────────────────────
	domain := cfg.Domain
	wildcardDomain := "*." + domain
	frontDoor := ""
	if cfg.FrontDoor != nil {
		frontDoor = *cfg.FrontDoor
	}
	wildcardFrontDoor := "*." + frontDoor

	// Parent zone: by hosted_zone_id when set, else by the domain's parent name.
	var parentZoneID pulumi.StringOutput
	if cfg.HostedZoneID != nil && *cfg.HostedZoneID != "" {
		zid := *cfg.HostedZoneID
		z, zerr := awsroute53.LookupZone(ctx, &awsroute53.LookupZoneArgs{
			ZoneId:      &zid,
			PrivateZone: pulumi.BoolRef(false),
		}, nil)
		if zerr != nil {
			return fmt.Errorf("cluster: failed to look up hosted zone %s: %w", zid, zerr)
		}
		parentZoneID = pulumi.String(z.ZoneId).ToStringOutput()
	} else {
		// domain.split(".", 1)[-1] then ensure a trailing dot.
		parentName := domain
		if idx := strings.Index(domain, "."); idx >= 0 {
			parentName = domain[idx+1:]
		}
		parentName = strings.TrimSuffix(parentName, ".") + "."
		z, zerr := awsroute53.LookupZone(ctx, &awsroute53.LookupZoneArgs{
			Name:        &parentName,
			PrivateZone: pulumi.BoolRef(false),
		}, nil)
		if zerr != nil {
			return fmt.Errorf("cluster: failed to look up parent zone %q: %w", parentName, zerr)
		}
		parentZoneID = pulumi.String(z.ZoneId).ToStringOutput()
	}

	altDomains := pulumi.StringArray{pulumi.String(wildcardDomain)}
	if frontDoor != "" {
		altDomains = append(altDomains, pulumi.String(frontDoor), pulumi.String(wildcardFrontDoor))
	}

	certTags := map[string]string{}
	for k, v := range params.requiredTags {
		certTags[k] = v
	}
	certTags["Name"] = fmt.Sprintf("%s-%s", name, domain)

	clusterFullURNAlias := func(resourceType, resourceName string) pulumi.ResourceOption {
		urn := fmt.Sprintf("urn:pulumi:%s::%s::%s$%s::%s",
			ctx.Stack(), clusterOldProjectName, clusterWrapperType, resourceType, resourceName)
		return pulumi.Aliases([]pulumi.Alias{{URN: pulumi.URN(urn)}})
	}

	cert, err := acm.NewCertificate(ctx, fmt.Sprintf("%s-%s", name, domain), &acm.CertificateArgs{
		DomainName:              pulumi.String(domain),
		SubjectAlternativeNames: altDomains,
		ValidationMethod:        pulumi.String("DNS"),
		Tags:                    toStringMap(certTags),
	}, clusterFullURNAlias("aws:acm/certificate:Certificate", fmt.Sprintf("%s-%s", name, domain)))
	if err != nil {
		return fmt.Errorf("cluster: failed to create ACM certificate: %w", err)
	}

	// Validation records (one per unique resource_record_name), parented to cert.
	// validation_record_fqdns must list the created records' fqdns, matching Python's
	// records.apply(lambda r: [record.fqdn for record in r]). rec.Fqdn is an unresolved
	// Output, so collect into a pulumi.StringArray and flatten via the apply's returned
	// Output — a []interface{} holding Outputs would NOT resolve (the prior bug: the
	// fqdns came back empty, dropping validationRecordFqdns and forcing a replace).
	// Preserve DVO order (no sort) to match Python/state.
	validationFQDNs := cert.DomainValidationOptions.ApplyT(
		func(dvos []acm.CertificateDomainValidationOption) (pulumi.StringArrayOutput, error) {
			seen := map[string]bool{}
			fqdns := pulumi.StringArray{}
			for _, dvo := range dvos {
				recName := ""
				if dvo.ResourceRecordName != nil {
					recName = *dvo.ResourceRecordName
				}
				if seen[recName] {
					continue
				}
				seen[recName] = true
				recValue := ""
				if dvo.ResourceRecordValue != nil {
					recValue = *dvo.ResourceRecordValue
				}
				recType := ""
				if dvo.ResourceRecordType != nil {
					recType = *dvo.ResourceRecordType
				}
				rec, rerr := awsroute53.NewRecord(ctx, fmt.Sprintf("%s-%s", name, recName), &awsroute53.RecordArgs{
					Name:    pulumi.String(recName),
					Records: pulumi.StringArray{pulumi.String(recValue)},
					Ttl:     pulumi.Int(300),
					Type:    pulumi.String(recType),
					ZoneId:  parentZoneID,
				}, pulumi.Parent(cert))
				if rerr != nil {
					return pulumi.StringArrayOutput{}, rerr
				}
				fqdns = append(fqdns, rec.Fqdn)
			}
			return fqdns.ToStringArrayOutput(), nil
		}).(pulumi.StringArrayOutput)

	if _, err := acm.NewCertificateValidation(ctx, name, &acm.CertificateValidationArgs{
		CertificateArn:        cert.Arn,
		ValidationRecordFqdns: validationFQDNs,
	}, pulumi.Parent(cert)); err != nil {
		return fmt.Errorf("cluster: failed to create certificate validation: %w", err)
	}

	// ── Traefik (nested component) + define_domains ─────────────────────────────
	traefik, err := deployControlRoomTraefik(
		ctx,
		params.clusterName, "default", cfg.TraefikVersionOrDefault(),
		cfg.TraefikDeploymentReplicasOrDefault(),
		cert.Arn,
		cfg.TrueName, cfg.Environment,
		pulumi.Provider(eksCluster.Provider()),
		params.protectPersistentResources,
	)
	if err != nil {
		return err
	}

	domainsToCnames := map[string]string{domain: "", wildcardDomain: ""}
	if frontDoor != "" {
		domainsToCnames[domain] = frontDoor
		domainsToCnames[wildcardDomain] = wildcardFrontDoor
	}
	if err := traefik.defineDomains(parentZoneID, domainsToCnames); err != nil {
		return err
	}

	// ── EBS CSI → LBC → metrics-server → secret-store CSI (+aws) ─────────────────
	eksCluster.
		// Control room calls with_ebs_csi_driver() with no role_name, so Python
		// defaults to f"{self.name}-ebs-csi-driver" (NO .posit.team suffix). The
		// workload path passes the .posit.team-suffixed name explicitly; do NOT
		// conflate the two. State URN: …$aws:iam/role:Role::<name>-ebs-csi-driver.
		WithEbsCsiDriver(name+"-ebs-csi-driver", cfg.EBSCsiAddonVersion()).
		WithAwsLbc(cfg.AWSLbcVersion(), "").
		WithMetricsServer(cfg.MetricsServerVersionOrDefault()).
		WithSecretStoreCsi(cfg.SecretStoreCsiVersionOrDefault()).
		WithSecretStoreCsiAwsProvider(cfg.SecretStoreCsiAwsProviderVersionOrDefault())

	if err := eksCluster.Err(); err != nil {
		return err
	}

	// ── traefik-forward-auth (after Traefik) ────────────────────────────────────
	// Use the front-door domain when set so management UIs are served behind Okta.
	desiredDomain := domain
	if frontDoor != "" {
		desiredDomain = frontDoor
	}
	eksCluster.WithTraefikForwardAuth(desiredDomain, cfg.TraefikForwardAuthVersionOrDefault(),
		[]pulumi.Resource{traefik.release})

	// ── Grafana ─────────────────────────────────────────────────────────────────
	eksCluster.WithGrafana(aws.GrafanaParams{
		Domain:          desiredDomain,
		DBConnectionURL: params.grafanaDBConnection,
		OpsgenieKey:     params.opsgenieKey,
		WLAccountIDs:    params.wlAccountIDs,
		Version:         cfg.GrafanaVersionOrDefault(),
		Alerts:          params.grafanaAlerts,
		Dashboards:      params.grafanaDashboards,
	})

	// ── Mimir ───────────────────────────────────────────────────────────────────
	rulerBucketPrefix := fmt.Sprintf("%s-mrs-", name)
	eksCluster.WithMimir(aws.MimirParams{
		BucketPrefix: rulerBucketPrefix,
		Domain:       desiredDomain,
		Creds:        params.mimirCreds,
		Salt:         params.mimirSalt,
		Tags:         params.requiredTags,
		Region:       params.region,
		Version:      cfg.MimirVersionOrDefault(),
	})

	return eksCluster.Err()
}

func toStringMap(m map[string]string) pulumi.StringMap {
	out := pulumi.StringMap{}
	for k, v := range m {
		out[k] = pulumi.String(v)
	}
	return out
}

// collectWorkloadAccountIDs enumerates every workload under __work__/, reads its
// ptd.yaml, and collects the AWS account_id / Azure tenant_id of workloads whose
// control_room_account_id matches this control room. Mirrors
// AWSControlRoom.workloads_index() + the AWS/Azure id collection in _define_eks.
// The returned list is sorted for deterministic X-Scope-OrgID ordering.
func collectWorkloadAccountIDs(controlRoomAccountID string) ([]string, error) {
	workDir := filepath.Join(helpers.GetTargetsConfigPath(), helpers.WorkDir)
	entries, err := os.ReadDir(workDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	idSet := map[string]bool{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		ptdYaml := filepath.Join(workDir, e.Name(), "ptd.yaml")
		raw, rerr := os.ReadFile(ptdYaml)
		if rerr != nil {
			continue // no ptd.yaml → skip (matches Python's exists() filter)
		}
		var doc struct {
			Kind string `yaml:"kind"`
			Spec struct {
				ControlRoomAccountID string `yaml:"control_room_account_id"`
				AccountID            string `yaml:"account_id"`
				TenantID             string `yaml:"tenant_id"`
			} `yaml:"spec"`
		}
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			continue
		}
		if doc.Spec.ControlRoomAccountID != controlRoomAccountID {
			continue
		}
		switch doc.Kind {
		case "AWSWorkloadConfig":
			if doc.Spec.AccountID != "" {
				idSet[doc.Spec.AccountID] = true
			}
		case "AzureWorkloadConfig":
			if doc.Spec.TenantID != "" {
				idSet[doc.Spec.TenantID] = true
			}
		}
	}

	ids := make([]string, 0, len(idSet))
	for id := range idSet {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}

// loadGrafanaConfigMapFiles reads the alert YAML and dashboard JSON files from the
// embedded grafana assets, mirroring _create_alert_configmaps /
// _create_dashboard_configmaps. Alert files keep their raw YAML; dashboard JSON is
// re-marshalled with uid=<stem> and id=null for idempotency (matching the Python
// normalization).
func loadGrafanaConfigMapFiles() (alerts, dashboards []aws.GrafanaConfigMapFile, err error) {
	alertFiles, err := fs.Glob(grafanaAssets, grafanaAlertsDir+"/*.yaml")
	if err != nil {
		return nil, nil, err
	}
	sort.Strings(alertFiles)
	for _, f := range alertFiles {
		content, rerr := grafanaAssets.ReadFile(f)
		if rerr != nil {
			return nil, nil, rerr
		}
		stem := strings.TrimSuffix(path.Base(f), ".yaml")
		alerts = append(alerts, aws.GrafanaConfigMapFile{
			LogicalSuffix: strings.ReplaceAll(stem, "_", "-"),
			DataKey:       "alerts.yaml",
			Content:       string(content),
		})
	}

	dashFiles, err := fs.Glob(grafanaAssets, grafanaDashboardsDir+"/*.json")
	if err != nil {
		return nil, nil, err
	}
	sort.Strings(dashFiles)
	for _, f := range dashFiles {
		raw, rerr := grafanaAssets.ReadFile(f)
		if rerr != nil {
			return nil, nil, rerr
		}
		stem := strings.TrimSuffix(path.Base(f), ".json")
		var dashboard map[string]interface{}
		if err := json.Unmarshal(raw, &dashboard); err != nil {
			return nil, nil, fmt.Errorf("invalid JSON in %s: %w", f, err)
		}
		dashboard["uid"] = stem
		dashboard["id"] = nil
		normalized, merr := json.MarshalIndent(dashboard, "", "  ")
		if merr != nil {
			return nil, nil, merr
		}
		dashboards = append(dashboards, aws.GrafanaConfigMapFile{
			LogicalSuffix: sanitizeK8sName(stem),
			DataKey:       stem + ".json",
			Content:       string(normalized),
		})
	}

	return alerts, dashboards, nil
}

// sanitizeK8sName ports ptd.pulumi_resources.lib.sanitize_k8s_name: lowercase,
// non-alphanumerics→hyphen, collapse repeats, trim leading/trailing hyphens.
func sanitizeK8sName(name string) string {
	lower := strings.ToLower(name)
	var b strings.Builder
	for _, r := range lower {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	collapsed := b.String()
	for strings.Contains(collapsed, "--") {
		collapsed = strings.ReplaceAll(collapsed, "--", "-")
	}
	return strings.Trim(collapsed, "-")
}
