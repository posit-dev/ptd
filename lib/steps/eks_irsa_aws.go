package steps

import (
	"context"
	"fmt"
	"sort"
	"strings"

	awsiam "github.com/pulumi/pulumi-aws/sdk/v6/go/aws/iam"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/posit-dev/ptd/lib/types"
)

// workloadIRSAParams bundles the data needed to create the 8 workload-scoped
// IRSA roles (FSx, LBC, ExternalDNS, TraefikForwardAuth, Mimir, Loki, EBS-CSI,
// Alloy) in phase 2 of the eks deploy. These roles previously lived in the
// persistent step; they were relocated to the eks step (where they belong by
// lifecycle: the trust policy binds to the cluster's OIDC issuer) so the
// persistent_reprise step could be removed. The role NAME strings and the
// trust serviceAccounts/namespaces are copied verbatim from the old persistent
// call sites so the AWS physical identity is unchanged and a state migration
// can adopt rather than replace each role.
type workloadIRSAParams struct {
	compoundName string
	accountID    string
	region       string
	// callerARN is the IRSA trust fallback principal (only used when no OIDC
	// issuer is supplied — should not happen here, the cluster always exists).
	callerARN string
	// iamPermissionsBoundaryARN is the workload permissions-boundary policy ARN.
	iamPermissionsBoundaryARN string
	// requiredTags mirror the eks step's required_tags (resource_tags +
	// posit.team/{true-name,environment,managed-by}).
	requiredTags map[string]string

	// externalDNSEnabled gates the ExternalDNS role (Python external_dns_enabled,
	// *bool default true).
	externalDNSEnabled bool
	// hostedZoneManagementEnabled gates whether the ExternalDNS policy gets any
	// zone ARNs in its ChangeResourceRecordSets resource list (Python default
	// true). When false, persistent created no zones, so the list is empty.
	hostedZoneManagementEnabled bool
	// siteZoneIDs maps a SITE NAME to its Route53 hosted-zone ID, read from the
	// persistent step's `hosted_zone_name_servers` stack output. For a zone
	// persistent CREATED this is the AWS-assigned id; for an ADOPTED zone the
	// configured id — i.e. exactly the id behind the route53.Zone .Arn the
	// persistent ExternalDNS policy used. The policy formats
	// arn:aws:route53:::hostedzone/<id> from these, so the eks-side ARN set is
	// byte-identical to persistent's with no runtime route53 lookup.
	siteZoneIDs map[string]string
	// zoneOutputPresent is false when the persistent hosted_zone_name_servers
	// output could not be read; deployIRSAExternalDNS turns that into a hard error
	// (persistent must apply before eks — same ordering constraint as bastion_id).
	zoneOutputPresent bool
	// extraClusterOidcURLs are configured external OIDC issuer URLs
	// (cfg.ExtraClusterOidcUrls) folded into every IRSA role's trust policy, in
	// addition to each managed cluster's own OIDC provider. persistent appended
	// these before building the trust, so they must be carried over for parity.
	extraClusterOidcURLs []string

	// Role + policy physical names (== logical names, verbatim from persistent).
	fsxOpenzfsRoleName                  string
	lbcRoleName                         string
	lbcPolicyName                       string
	externalDNSRoleName                 string
	dnsUpdatePolicyName                 string
	traefikForwardAuthRoleName          string
	traefikForwardAuthReadSecretsPolicy string
	mimirRoleName                       string
	mimirS3BucketName                   string
	mimirS3BucketPolicyName             string
	lokiRoleName                        string
	lokiS3BucketName                    string
	lokiS3BucketPolicyName              string
	ebsCsiRoleName                      string
	alloyRoleName                       string
	alloyPolicyName                     string
}

// buildWorkloadIRSAParams assembles workloadIRSAParams from the awsEKSParams +
// the workload config, computing the 8 role names (fmt.Sprintf, identical to
// persistent_aws.go). siteZoneIDs maps site name → hosted-zone id, sourced from
// the persistent step's hosted_zone_name_servers stack output (see
// persistentSiteZoneIDs); zoneOutputPresent reports whether that output existed.
func buildWorkloadIRSAParams(p awsEKSParams, cfg awsWorkloadConfigForIRSA, siteZoneIDs map[string]string, zoneOutputPresent bool) workloadIRSAParams {
	cn := p.compoundName

	return workloadIRSAParams{
		compoundName:                cn,
		accountID:                   p.accountID,
		region:                      p.region,
		callerARN:                   p.callerARN,
		iamPermissionsBoundaryARN:   p.iamPermissionsBoundaryARN,
		requiredTags:                p.requiredTags,
		externalDNSEnabled:          cfg.ExternalDNSEnabled,
		hostedZoneManagementEnabled: cfg.HostedZoneManagementEnabled,
		siteZoneIDs:                 siteZoneIDs,
		zoneOutputPresent:           zoneOutputPresent,
		extraClusterOidcURLs:        cfg.ExtraClusterOidcUrls,

		fsxOpenzfsRoleName:                  fmt.Sprintf("aws-fsx-openzfs-csi-driver.%s.posit.team", cn),
		lbcRoleName:                         fmt.Sprintf("aws-load-balancer-controller.%s.posit.team", cn),
		lbcPolicyName:                       fmt.Sprintf("lbc.%s.posit.team", cn),
		externalDNSRoleName:                 fmt.Sprintf("external-dns.%s.posit.team", cn),
		dnsUpdatePolicyName:                 fmt.Sprintf("dns-update.%s.posit.team", cn),
		traefikForwardAuthRoleName:          fmt.Sprintf("traefik-forward-auth.%s.posit.team", cn),
		traefikForwardAuthReadSecretsPolicy: fmt.Sprintf("traefik-forward-auth-read-secrets.%s.posit.team", cn),
		mimirRoleName:                       fmt.Sprintf("mimir.%s.posit.team", cn),
		mimirS3BucketName:                   fmt.Sprintf("%s-mimir", cn),
		mimirS3BucketPolicyName:             fmt.Sprintf("mimir-s3-bucket.%s.posit.team", cn),
		lokiRoleName:                        fmt.Sprintf("loki.%s.posit.team", cn),
		lokiS3BucketName:                    fmt.Sprintf("%s-loki", cn),
		lokiS3BucketPolicyName:              fmt.Sprintf("loki-s3-bucket.%s.posit.team", cn),
		ebsCsiRoleName:                      fmt.Sprintf("aws-ebs-csi.%s.posit.team", cn),
		alloyRoleName:                       fmt.Sprintf("alloy.%s.posit.team", cn),
		alloyPolicyName:                     fmt.Sprintf("alloy.%s.posit.team", cn),
	}
}

// awsWorkloadConfigForIRSA is the slice of AWSWorkloadConfig the IRSA builder
// needs. The per-site zone identities come from the persistent stack output, not
// config, so only the two gating flags are required here. Decoupling from the
// full config keeps buildWorkloadIRSAParams easy to construct in tests.
type awsWorkloadConfigForIRSA struct {
	// ExternalDNSEnabled gates the ExternalDNS role (Python default true).
	ExternalDNSEnabled bool
	// HostedZoneManagementEnabled gates whether the ExternalDNS policy lists any
	// zone ARNs (Python default true); when false persistent created no zones.
	HostedZoneManagementEnabled bool
	// ExtraClusterOidcUrls are configured external OIDC issuer URLs folded into
	// every IRSA role's trust policy (cfg.extra_cluster_oidc_urls).
	ExtraClusterOidcUrls []string
}

// irsaConfigFromWorkload extracts the IRSA-relevant slice of an AWSWorkloadConfig,
// resolving the *bool defaults the way the persistent step did
// (external_dns_enabled and hosted_zone_management_enabled both default true).
func irsaConfigFromWorkload(cfg types.AWSWorkloadConfig) awsWorkloadConfigForIRSA {
	return awsWorkloadConfigForIRSA{
		ExternalDNSEnabled:          cfg.ExternalDNSEnabled == nil || *cfg.ExternalDNSEnabled,
		HostedZoneManagementEnabled: cfg.HostedZoneManagementEnabled == nil || *cfg.HostedZoneManagementEnabled,
		ExtraClusterOidcUrls:        cfg.ExtraClusterOidcUrls,
	}
}

// persistentSiteZoneIDs reads the persistent step's hosted_zone_name_servers
// stack output and returns a per-site (siteName → hosted-zone-id) map plus a
// `present` flag indicating the output existed.
//
// The output is a map keyed by site name, each value a map with "domain",
// "name_servers", "zone_id" (see exportPersistentOutputs). zone_id is the id
// behind the route53.Zone .Arn the persistent ExternalDNS policy used — the
// AWS-assigned id for a created zone, the configured id for an adopted zone.
// persistent attaches the per-domain primary's zone to every site of that
// domain, so sibling sites of a domain carry the SAME zone_id here; keying by
// site therefore reproduces persistent's per-site ARN multiplicity directly.
//
// hosted_zone_name_servers is written on the persistent step's normal (pass-1)
// run, which always precedes eks (same ordering constraint as bastion_id), so
// the value is available when eks reads it. `present` is false only when the
// output is missing/unreadable (e.g. a stack that has not applied the export);
// the caller turns that into a hard error rather than a silently empty policy.
func persistentSiteZoneIDs(ctx context.Context, target types.Target) (zoneIDs map[string]string, present bool) {
	zoneIDs = map[string]string{}
	outs, err := getPersistentStackOutputs(ctx, target)
	if err != nil {
		return zoneIDs, false
	}
	raw, ok := outs["hosted_zone_name_servers"]
	if !ok {
		return zoneIDs, false
	}
	// hzManagement off exports an empty array (not a map) for this key — that is a
	// valid "present, no zones" state, so report present=true with an empty map.
	sites, ok := raw.Value.(map[string]interface{})
	if !ok {
		return zoneIDs, true
	}
	for siteName, v := range sites {
		entry, ok := v.(map[string]interface{})
		if !ok {
			continue
		}
		if zoneID, _ := entry["zone_id"].(string); zoneID != "" {
			zoneIDs[siteName] = zoneID
		}
	}
	return zoneIDs, true
}

// hostedZoneARN formats a route53 hosted-zone ARN from a zone id, matching the
// zone resource .Arn persistent's ExternalDNS policy used. The Pulumi ZoneId is
// the bare id (e.g. "Z123"), but a value may arrive prefixed with "/hostedzone/"
// from some route53 paths; strip it defensively so the ARN is byte-identical.
func hostedZoneARN(zoneID string) string {
	zoneID = strings.TrimPrefix(zoneID, "/hostedzone/")
	return fmt.Sprintf("arn:aws:route53:::hostedzone/%s", zoneID)
}

// deployWorkloadIRSARoles creates the 8 workload-scoped IRSA roles ONCE, after
// every cluster's OIDC provider has been created (phase 2 of awsEKSDeploy). The
// trust policy is built from the accumulated OIDC issuer URLs via
// irsaTrustPolicyOutput. Each role's logical name, physical Name, trust service
// accounts/namespace, and permission policy are copied verbatim from the old
// persistent call sites so a state migration can adopt rather than replace.
//
// These are standalone functions (NOT EKSCluster builder methods) because the
// roles are workload-scoped, not per-cluster. The persistent-era cross-stack
// ParentURN aliases are intentionally NOT carried over (the migration is
// import-based, not alias-based).
func deployWorkloadIRSARoles(ctx *pulumi.Context, p workloadIRSAParams, oidcURLs []pulumi.StringOutput) error {
	if err := deployIRSAFsx(ctx, p, oidcURLs); err != nil {
		return fmt.Errorf("eks: FSx IRSA: %w", err)
	}
	if err := deployIRSALBC(ctx, p, oidcURLs); err != nil {
		return fmt.Errorf("eks: LBC IRSA: %w", err)
	}
	if p.externalDNSEnabled {
		if err := deployIRSAExternalDNS(ctx, p, oidcURLs); err != nil {
			return fmt.Errorf("eks: ExternalDNS IRSA: %w", err)
		}
	}
	if err := deployIRSATraefikForwardAuth(ctx, p, oidcURLs); err != nil {
		return fmt.Errorf("eks: traefik-forward-auth IRSA: %w", err)
	}
	if err := deployIRSAMimir(ctx, p, oidcURLs); err != nil {
		return fmt.Errorf("eks: mimir IRSA: %w", err)
	}
	if err := deployIRSALoki(ctx, p, oidcURLs); err != nil {
		return fmt.Errorf("eks: loki IRSA: %w", err)
	}
	if err := deployIRSAEBSCsi(ctx, p, oidcURLs); err != nil {
		return fmt.Errorf("eks: EBS-CSI IRSA: %w", err)
	}
	if err := deployIRSAAlloy(ctx, p, oidcURLs); err != nil {
		return fmt.Errorf("eks: alloy IRSA: %w", err)
	}
	return nil
}

// deployIRSAFsx ports the FSx OpenZFS CSI driver IRSA role from persistent
// buildPersistentFSx. Logical name "aws-fsx-openzfs-csi-driver.posit.team",
// physical name fsxOpenzfsRoleName; trust in kube-system for the controller +
// nodes service accounts; managed AmazonFSxFullAccess attachment.
func deployIRSAFsx(ctx *pulumi.Context, p workloadIRSAParams, oidcURLs []pulumi.StringOutput) error {
	const fsxRoleLogical = "aws-fsx-openzfs-csi-driver.posit.team"
	trust := irsaTrustPolicyOutput(oidcURLs, "kube-system",
		[]string{
			"controller.aws-fsx-openzfs-csi-driver.posit.team",
			"nodes.aws-fsx-openzfs-csi-driver.posit.team",
		},
		p.accountID, p.callerARN)
	role, err := awsiam.NewRole(ctx, fsxRoleLogical, &awsiam.RoleArgs{
		Name:                pulumi.String(p.fsxOpenzfsRoleName),
		AssumeRolePolicy:    trust,
		PermissionsBoundary: pulumi.String(p.iamPermissionsBoundaryARN),
		Tags:                awsTagMap(p.requiredTags, nil),
	})
	if err != nil {
		return err
	}
	fsxAttachName := fmt.Sprintf("%s-fsx-openzfs", p.compoundName)
	_, err = awsiam.NewRolePolicyAttachment(ctx, fsxAttachName, &awsiam.RolePolicyAttachmentArgs{
		Role:      role.Name,
		PolicyArn: pulumi.String("arn:aws:iam::aws:policy/AmazonFSxFullAccess"),
	}, pulumi.Parent(role))
	return err
}

// deployIRSALBC ports the AWS Load Balancer Controller IRSA role from persistent
// buildPersistentLBCIAM. Trust in kube-system for aws-load-balancer-controller.posit.team;
// inline lbcPolicyJSON.
func deployIRSALBC(ctx *pulumi.Context, p workloadIRSAParams, oidcURLs []pulumi.StringOutput) error {
	trust := irsaTrustPolicyOutput(oidcURLs, "kube-system",
		[]string{"aws-load-balancer-controller.posit.team"},
		p.accountID, p.callerARN)
	role, err := awsiam.NewRole(ctx, p.lbcRoleName, &awsiam.RoleArgs{
		Name:                pulumi.String(p.lbcRoleName),
		AssumeRolePolicy:    trust,
		PermissionsBoundary: pulumi.String(p.iamPermissionsBoundaryARN),
		Tags:                awsTagMap(p.requiredTags, nil),
	})
	if err != nil {
		return err
	}
	pol, err := awsiam.NewPolicy(ctx, p.lbcPolicyName, &awsiam.PolicyArgs{
		Name:   pulumi.String(p.lbcPolicyName),
		Policy: pulumi.String(lbcPolicyJSON),
	}, pulumi.Parent(role))
	if err != nil {
		return err
	}
	attName := fmt.Sprintf("%s-att", p.lbcPolicyName)
	_, err = awsiam.NewRolePolicyAttachment(ctx, attName, &awsiam.RolePolicyAttachmentArgs{
		Role:      role.Name,
		PolicyArn: pol.Arn,
	}, pulumi.Parent(role))
	return err
}

// deployIRSAExternalDNS ports the ExternalDNS IRSA role from persistent
// buildPersistentExternalDNSIAM. Trust in kube-system for external-dns.posit.team;
// inline policy granting route53:ChangeResourceRecordSets on the workload's
// hosted-zone ARNs plus a wildcard list statement.
//
// The zone-ARN SET must be byte-identical to what the persistent step's policy
// produced (this is a pure relocation, not a re-implementation). Persistent
// derived each ARN from a concrete route53.Zone resource's .Arn (created OR
// adopted). We recover that SAME identity without any runtime route53 lookup by
// reading the per-site hosted-zone IDs the persistent step already exported in
// its `hosted_zone_name_servers` stack output (p.siteZoneIDs, keyed by site):
//
//   - For a CREATED zone, the exported zone_id is the AWS-assigned id behind
//     persistent's route53.NewZone .Arn.
//   - For an ADOPTED zone, it is the configured id behind persistent's
//     route53.GetZone(id) .Arn.
//
// Either way hostedZoneARN(id) equals persistent's zone .Arn, and this avoids the
// by-name LookupZone path (which would mis-resolve or error for private hosted
// zones — route53 getZone requires exactly one match and a naive by-name filter
// does not disambiguate public vs private).
//
// Persistent's policy shape is reproduced exactly: persistent attaches the
// per-domain primary's zone to every site of that domain and collects an ARN for
// each site whose zone is non-nil, iterating sites sorted by site name. Because
// the export carries the same (shared) zone_id under each such site, iterating
// p.siteZoneIDs sorted by site name reproduces that ARN set, multiplicity, and
// order directly. When hosted-zone management is off, persistent created/adopted
// no zones (the export is empty) and the ChangeResourceRecordSets list is empty.
func deployIRSAExternalDNS(ctx *pulumi.Context, p workloadIRSAParams, oidcURLs []pulumi.StringOutput) error {
	// The hosted-zone ids come from the persistent stack output; if that output is
	// missing, fail loudly rather than emit an empty policy (persistent must apply
	// before eks reads it — the same ordering constraint as bastion_id). An empty
	// map WITH the output present is valid (hosted-zone management off / no zones).
	if !p.zoneOutputPresent {
		return fmt.Errorf(
			"eks: persistent stack output %q not found for %s — run the persistent step first "+
				"(it exports the hosted-zone ids the ExternalDNS IRSA policy needs)",
			"hosted_zone_name_servers", p.compoundName)
	}

	trust := irsaTrustPolicyOutput(oidcURLs, "kube-system",
		[]string{"external-dns.posit.team"},
		p.accountID, p.callerARN)
	role, err := awsiam.NewRole(ctx, p.externalDNSRoleName, &awsiam.RoleArgs{
		Name:                pulumi.String(p.externalDNSRoleName),
		AssumeRolePolicy:    trust,
		PermissionsBoundary: pulumi.String(p.iamPermissionsBoundaryARN),
		Tags:                awsTagMap(p.requiredTags, nil),
	})
	if err != nil {
		return err
	}

	// Collect ARNs in persistent's policy order: sites (with a zone) sorted by
	// site name, one ARN per site. When hosted-zone management is off, persistent
	// created no zones so the export — and thus this list — is empty. The slice is
	// non-nil so the empty case marshals to "Resource":[] (persistent's shape),
	// not null.
	zones := []string{}
	if p.hostedZoneManagementEnabled {
		siteNames := make([]string, 0, len(p.siteZoneIDs))
		for name := range p.siteZoneIDs {
			siteNames = append(siteNames, name)
		}
		sort.Strings(siteNames)
		for _, name := range siteNames {
			zones = append(zones, hostedZoneARN(p.siteZoneIDs[name]))
		}
	}

	doc := map[string]interface{}{
		"Version": "2012-10-17",
		"Statement": []map[string]interface{}{
			{
				"Effect":   "Allow",
				"Action":   []string{"route53:ChangeResourceRecordSets"},
				"Resource": zones,
			},
			{
				"Effect": "Allow",
				"Action": []string{
					"route53:ListHostedZones",
					"route53:ListResourceRecordSets",
					"route53:ListTagsForResource",
				},
				"Resource": []string{"*"},
			},
		},
	}
	policyJSON, err := jsonMarshal(doc)
	if err != nil {
		return err
	}

	pol, err := awsiam.NewPolicy(ctx, p.dnsUpdatePolicyName, &awsiam.PolicyArgs{
		Name:   pulumi.String(p.dnsUpdatePolicyName),
		Policy: pulumi.String(policyJSON),
	}, pulumi.Parent(role))
	if err != nil {
		return err
	}
	attName := fmt.Sprintf("%s-att", p.dnsUpdatePolicyName)
	_, err = awsiam.NewRolePolicyAttachment(ctx, attName, &awsiam.RolePolicyAttachmentArgs{
		Role:      role.Name,
		PolicyArn: pol.Arn,
	}, pulumi.Parent(role))
	return err
}

// deployIRSATraefikForwardAuth ports the traefik-forward-auth IRSA role from
// persistent buildPersistentTraefikForwardAuthIAM. Trust in kube-system for
// traefik-forward-auth.posit.team; inline traefikForwardAuthSecretsPolicyJSON.
func deployIRSATraefikForwardAuth(ctx *pulumi.Context, p workloadIRSAParams, oidcURLs []pulumi.StringOutput) error {
	trust := irsaTrustPolicyOutput(oidcURLs, "kube-system",
		[]string{"traefik-forward-auth.posit.team"},
		p.accountID, p.callerARN)
	role, err := awsiam.NewRole(ctx, p.traefikForwardAuthRoleName, &awsiam.RoleArgs{
		Name:                pulumi.String(p.traefikForwardAuthRoleName),
		AssumeRolePolicy:    trust,
		PermissionsBoundary: pulumi.String(p.iamPermissionsBoundaryARN),
		Tags:                awsTagMap(p.requiredTags, nil),
	})
	if err != nil {
		return err
	}
	pol, err := awsiam.NewPolicy(ctx, p.traefikForwardAuthReadSecretsPolicy, &awsiam.PolicyArgs{
		Name:   pulumi.String(p.traefikForwardAuthReadSecretsPolicy),
		Policy: pulumi.String(traefikForwardAuthSecretsPolicyJSON(p.region, p.accountID)),
	}, pulumi.Parent(role))
	if err != nil {
		return err
	}
	attName := fmt.Sprintf("%s-att", p.traefikForwardAuthReadSecretsPolicy)
	_, err = awsiam.NewRolePolicyAttachment(ctx, attName, &awsiam.RolePolicyAttachmentArgs{
		Role:      role.Name,
		PolicyArn: pol.Arn,
	}, pulumi.Parent(role))
	return err
}

// deployIRSAMimir ports the Mimir IRSA role from persistent buildPersistentMimir
// (role + bucket read/write policy + attachment ONLY — the random password and
// the S3 bucket itself stay in the persistent step). Trust in the mimir namespace
// for mimir.posit.team; the read/write policy Resource is the LIVE bucket ARN
// arn:aws:s3:::ptd-<cn>-mimir (persistent's defineNamedBucket prefixes prefix="ptd",
// so the physical bucket is ptd-<cn>-mimir and the original policy read its
// bucket.Arn). The policy TAG keeps the un-prefixed bucket name to match persistent.
func deployIRSAMimir(ctx *pulumi.Context, p workloadIRSAParams, oidcURLs []pulumi.StringOutput) error {
	trust := irsaTrustPolicyOutput(oidcURLs, "mimir",
		[]string{"mimir.posit.team"},
		p.accountID, p.callerARN)
	role, err := awsiam.NewRole(ctx, p.mimirRoleName, &awsiam.RoleArgs{
		Name:                pulumi.String(p.mimirRoleName),
		AssumeRolePolicy:    trust,
		PermissionsBoundary: pulumi.String(p.iamPermissionsBoundaryARN),
		Tags:                awsTagMap(p.requiredTags, nil),
	})
	if err != nil {
		return err
	}
	pol, err := awsiam.NewPolicy(ctx, p.mimirS3BucketPolicyName, &awsiam.PolicyArgs{
		Name:        pulumi.String(p.mimirS3BucketPolicyName),
		Description: pulumi.String(fmt.Sprintf("Posit Team Dedicated policy for %s to read the Mimir S3 bucket", p.compoundName)),
		Policy:      bucketReadWritePolicyJSON(physicalBucketName(p.mimirS3BucketName)),
		Tags:        awsTagMap(p.requiredTags, map[string]string{"Name": fmt.Sprintf("%s-%s-s3-bucket-policy", p.compoundName, p.mimirS3BucketName)}),
	}, pulumi.Parent(role))
	if err != nil {
		return err
	}
	attName := fmt.Sprintf("%s-att", p.mimirS3BucketPolicyName)
	_, err = awsiam.NewRolePolicyAttachment(ctx, attName, &awsiam.RolePolicyAttachmentArgs{
		Role:      role.Name,
		PolicyArn: pol.Arn,
	}, pulumi.Parent(role))
	return err
}

// deployIRSALoki ports the Loki IRSA role from persistent buildPersistentLoki
// (role + bucket read/write policy + attachment ONLY — the S3 bucket itself stays
// in the persistent step). Trust in the loki namespace for loki.posit.team; the
// read/write policy Resource is the LIVE bucket ARN arn:aws:s3:::ptd-<cn>-loki
// (physical bucket is ptd-<cn>-loki via persistent's prefix="ptd"). The policy TAG
// keeps the un-prefixed bucket name to match persistent.
func deployIRSALoki(ctx *pulumi.Context, p workloadIRSAParams, oidcURLs []pulumi.StringOutput) error {
	trust := irsaTrustPolicyOutput(oidcURLs, "loki",
		[]string{"loki.posit.team"},
		p.accountID, p.callerARN)
	role, err := awsiam.NewRole(ctx, p.lokiRoleName, &awsiam.RoleArgs{
		Name:                pulumi.String(p.lokiRoleName),
		AssumeRolePolicy:    trust,
		PermissionsBoundary: pulumi.String(p.iamPermissionsBoundaryARN),
		Tags:                awsTagMap(p.requiredTags, nil),
	})
	if err != nil {
		return err
	}
	pol, err := awsiam.NewPolicy(ctx, p.lokiS3BucketPolicyName, &awsiam.PolicyArgs{
		Name:        pulumi.String(p.lokiS3BucketPolicyName),
		Description: pulumi.String(fmt.Sprintf("Posit Team Dedicated policy for %s to read the Loki S3 bucket", p.compoundName)),
		Policy:      bucketReadWritePolicyJSON(physicalBucketName(p.lokiS3BucketName)),
		Tags:        awsTagMap(p.requiredTags, map[string]string{"Name": fmt.Sprintf("%s-%s-s3-bucket-policy", p.compoundName, p.lokiS3BucketName)}),
	}, pulumi.Parent(role))
	if err != nil {
		return err
	}
	attName := fmt.Sprintf("%s-att", p.lokiS3BucketPolicyName)
	_, err = awsiam.NewRolePolicyAttachment(ctx, attName, &awsiam.RolePolicyAttachmentArgs{
		Role:      role.Name,
		PolicyArn: pol.Arn,
	}, pulumi.Parent(role))
	return err
}

// deployIRSAEBSCsi ports the EBS-CSI IRSA role from persistent
// buildPersistentEBSCsiIAM. Trust in kube-system for aws-ebs-csi-driver.posit.team;
// managed AmazonEBSCSIDriverPolicyV2 attachment (logical name
// "ebs-csi-driver-policy-att"). NOTE: this is the workload-scoped EBS role, NOT
// the per-cluster EBS CSI add-on role created by the EKSCluster builder
// (WithEbsCsiDriver) — those are separate and left in place.
func deployIRSAEBSCsi(ctx *pulumi.Context, p workloadIRSAParams, oidcURLs []pulumi.StringOutput) error {
	trust := irsaTrustPolicyOutput(oidcURLs, "kube-system",
		[]string{"aws-ebs-csi-driver.posit.team"},
		p.accountID, p.callerARN)
	role, err := awsiam.NewRole(ctx, p.ebsCsiRoleName, &awsiam.RoleArgs{
		Name:                pulumi.String(p.ebsCsiRoleName),
		AssumeRolePolicy:    trust,
		PermissionsBoundary: pulumi.String(p.iamPermissionsBoundaryARN),
		Tags:                awsTagMap(p.requiredTags, nil),
	})
	if err != nil {
		return err
	}
	_, err = awsiam.NewRolePolicyAttachment(ctx, "ebs-csi-driver-policy-att", &awsiam.RolePolicyAttachmentArgs{
		Role:      role.Name,
		PolicyArn: pulumi.String("arn:aws:iam::aws:policy/AmazonEBSCSIDriverPolicyV2"),
	}, pulumi.Parent(role))
	return err
}

// deployIRSAAlloy ports the Alloy IRSA role from persistent buildPersistentAlloyIAM.
// Trust in the alloy namespace for alloy.posit.team; inline alloyPolicyJSON().
func deployIRSAAlloy(ctx *pulumi.Context, p workloadIRSAParams, oidcURLs []pulumi.StringOutput) error {
	trust := irsaTrustPolicyOutput(oidcURLs, "alloy",
		[]string{"alloy.posit.team"},
		p.accountID, p.callerARN)
	role, err := awsiam.NewRole(ctx, p.alloyRoleName, &awsiam.RoleArgs{
		Name:                pulumi.String(p.alloyRoleName),
		AssumeRolePolicy:    trust,
		PermissionsBoundary: pulumi.String(p.iamPermissionsBoundaryARN),
		Tags:                awsTagMap(p.requiredTags, nil),
	})
	if err != nil {
		return err
	}
	pol, err := awsiam.NewPolicy(ctx, p.alloyPolicyName, &awsiam.PolicyArgs{
		Name:   pulumi.String(p.alloyPolicyName),
		Policy: pulumi.String(alloyPolicyJSON()),
	}, pulumi.Parent(role))
	if err != nil {
		return err
	}
	attName := fmt.Sprintf("%s-att", p.alloyPolicyName)
	_, err = awsiam.NewRolePolicyAttachment(ctx, attName, &awsiam.RolePolicyAttachmentArgs{
		Role:      role.Name,
		PolicyArn: pol.Arn,
	}, pulumi.Parent(role))
	return err
}

// physicalBucketName returns the live S3 bucket name for a logical bucket name,
// applying persistent's prefix ("ptd"). persistent's defineNamedBucket sets the
// bucket's physical Bucket to "<prefix>-<name>" (persistent_aws.go), so a logical
// name "<cn>-mimir" becomes the physical bucket "ptd-<cn>-mimir". This must match
// the live bucket so the IRSA policy Resource ARN is byte-identical to the one
// persistent's policy built from the bucket.Arn (verified against live state:
// buckets are ptd-<cn>-mimir / ptd-<cn>-loki).
func physicalBucketName(logicalBucketName string) string {
	return "ptd-" + logicalBucketName
}

// bucketReadWritePolicyJSON builds the READ_WRITE bucket policy document for a
// bucket given its PHYSICAL name (deriving the ARN as arn:aws:s3:::<name>),
// matching persistent defineBucketReadWritePolicy. The persistent step built the
// same document from the live bucket.Arn Output; here the bucket lives in the
// persistent stack, so the ARN is derived from the live bucket name — pass the
// physical name (see physicalBucketName), not the logical one.
func bucketReadWritePolicyJSON(physicalName string) pulumi.StringOutput {
	arn := fmt.Sprintf("arn:aws:s3:::%s", physicalName)
	doc := map[string]interface{}{
		"Version": "2012-10-17",
		"Statement": []map[string]interface{}{
			{
				"Effect":   "Allow",
				"Action":   bucketReadWritePolicyActions,
				"Resource": []string{arn, arn + "/*"},
			},
		},
	}
	b, _ := jsonMarshal(doc)
	return pulumi.String(b).ToStringOutput()
}
