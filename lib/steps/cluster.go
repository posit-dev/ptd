package steps

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/posit-dev/ptd/lib/aws"
	"github.com/posit-dev/ptd/lib/helpers"
	"github.com/posit-dev/ptd/lib/kube"
	"github.com/posit-dev/ptd/lib/proxy"
	"github.com/posit-dev/ptd/lib/types"
	awspulumi "github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

type ClusterStep struct {
	SrcTarget types.Target
	DstTarget types.Target
	Options   StepOptions
}

func (s *ClusterStep) Name() string {
	return "cluster"
}

func (s *ClusterStep) ProxyRequired() bool {
	return true
}

func (s *ClusterStep) Set(t types.Target, controlRoomTarget types.Target, options StepOptions) {
	s.SrcTarget = controlRoomTarget
	s.DstTarget = t
	s.Options = options
}

func (s *ClusterStep) Run(ctx context.Context) error {
	if s.DstTarget == nil {
		return fmt.Errorf("cluster step requires a destination target")
	}
	if !s.DstTarget.ControlRoom() {
		return fmt.Errorf("cluster step can only be run on control room targets")
	}

	// cluster (control room) is AWS-only.
	switch s.DstTarget.CloudProvider() {
	case types.AWS:
		return s.runAWSInlineGo(ctx)
	default:
		return fmt.Errorf("unsupported cloud provider for cluster: %s", s.DstTarget.CloudProvider())
	}
}

// runAWSInlineGo is the inline-Go control-room cluster path. It mirrors the
// pre-fetch → createStack → runPulumi pattern of eks_aws.go and ports
// aws_control_room_cluster.py's AWSControlRoomCluster._define_eks. All external
// data (CR secret fields, postgres_config grafana connection, workload account/
// tenant ids, live cluster VPC config + kubeconfig, alert/dashboard files) is
// fetched here and passed to awsClusterDeploy via awsClusterParams.
func (s *ClusterStep) runAWSInlineGo(ctx context.Context) error {
	creds, err := s.DstTarget.Credentials(ctx)
	if err != nil {
		return err
	}
	envVars, err := prepareEnvVarsForPulumi(ctx, s.DstTarget, creds)
	if err != nil {
		return err
	}
	if !s.DstTarget.TailscaleEnabled() {
		envVars["ALL_PROXY"] = fmt.Sprintf("socks5://localhost:%d", proxy.WorkloadPort(s.DstTarget.Name()))
	}

	rawConfig, err := helpers.ConfigForTarget(s.DstTarget)
	if err != nil {
		return fmt.Errorf("cluster: failed to load config: %w", err)
	}
	cfg, ok := rawConfig.(types.AWSControlRoomConfig)
	if !ok {
		return fmt.Errorf("cluster: expected AWSControlRoomConfig")
	}

	// protect_persistent_resources defaults True in Python (aws_control_room.py) and
	// is never set false in any ptd.yaml. The Go config field is a plain bool, so an
	// absent value would resolve to false and UNPROTECT the OIDC provider — force it
	// true to match Python, mirroring the persistent step.
	cfg.ProtectPersistentResources = true

	awsCreds, err := aws.OnlyAwsCredentials(creds)
	if err != nil {
		return err
	}

	compoundName := s.DstTarget.Name()
	region := s.DstTarget.Region()
	// CRITICAL: the control-room EKS cluster logical name AND its `name` input are
	// BOTH the compound name (aws_control_room_cluster.py passes name=self.name =
	// control_room.compound_name; aws_eks_cluster.py then does
	// aws.eks.Cluster(name, name=name, ...)). This is NOT
	// "default_{compound}-control-plane" — that pattern is the WORKLOAD naming
	// convention (aws_workload.py:eks_cluster_name) and even there the Go workload
	// path uses "{compound}-{release}". A changed logical name REPLACES the live
	// control plane (data loss).
	clusterName := compoundName

	// ── CR secret ({compound}.ctrl.posit.team) → opsgenie + mimir salt ──────────
	crSecretName := compoundName + ".ctrl.posit.team"
	crSecretJSON, err := s.DstTarget.SecretStore().GetSecretValue(ctx, creds, crSecretName)
	if err != nil {
		return fmt.Errorf("cluster: failed to get control room secret %q: %w", crSecretName, err)
	}
	var crSecret map[string]string
	if err := json.Unmarshal([]byte(crSecretJSON), &crSecret); err != nil {
		return fmt.Errorf("cluster: failed to parse control room secret: %w", err)
	}
	opsgenieKey := crSecret["opsgenie-api-key"]
	if opsgenieKey == "" {
		return fmt.Errorf("cluster: opsgenie-api-key not found in control room secret")
	}
	mimirSalt := crSecret["mimir-password-salt"]
	if mimirSalt == "" {
		return fmt.Errorf("cluster: mimir-password-salt not found in control room secret")
	}

	// ── Mimir auth secret ({compound}.mimir-auth.posit.team) → user→password ────
	mimirAuthSecretName := compoundName + ".mimir-auth.posit.team"
	mimirAuthJSON, err := s.DstTarget.SecretStore().GetSecretValue(ctx, creds, mimirAuthSecretName)
	if err != nil {
		return fmt.Errorf("cluster: failed to get mimir-auth secret %q: %w", mimirAuthSecretName, err)
	}
	var mimirCreds map[string]string
	if err := json.Unmarshal([]byte(mimirAuthJSON), &mimirCreds); err != nil {
		return fmt.Errorf("cluster: failed to parse mimir-auth secret: %w", err)
	}

	// ── postgres_config stack → db_grafana_connection ───────────────────────────
	pgOutputs, err := getPostgresConfigStackOutputs(ctx, s.DstTarget, envVars)
	if err != nil {
		return fmt.Errorf("cluster: failed to get postgres_config stack outputs: %w", err)
	}
	grafanaDBConnection := ""
	if v, ok := pgOutputs["db_grafana_connection"]; ok {
		grafanaDBConnection = fmt.Sprintf("%v", v.Value)
	}
	if grafanaDBConnection == "" {
		return fmt.Errorf("cluster: db_grafana_connection not found in postgres_config outputs")
	}

	// ── Workload account/tenant ids (X-Scope-OrgID multi-tenant header) ─────────
	wlAccountIDs, err := collectWorkloadAccountIDs(cfg.AccountID)
	if err != nil {
		return fmt.Errorf("cluster: failed to collect workload account ids: %w", err)
	}

	// ── Live cluster VPC config + kubeconfig (the control plane always exists) ──
	exists, authMode, err := aws.GetClusterAuthMode(ctx, awsCreds, region, clusterName)
	if err != nil {
		return fmt.Errorf("cluster: failed to probe cluster %s auth mode: %w", clusterName, err)
	}

	var subnetIDs []string
	var vpcID, clusterSGID string
	if exists {
		vpcCfg, verr := aws.GetClusterVPCConfig(ctx, awsCreds, region, clusterName)
		if verr != nil {
			return fmt.Errorf("cluster: failed to get VPC config for %s: %w", clusterName, verr)
		}
		subnetIDs = vpcCfg.SubnetIDs
		vpcID = vpcCfg.VpcID
		clusterSGID = vpcCfg.ClusterSecurityGroupID
	} else {
		// Greenfield: look up the CR VPC + private subnets by Name tag (the way
		// Python aws_subnets_for_vpc(self.name, "private") did at create time).
		id, found, verr := aws.GetVpcID(ctx, awsCreds, region, compoundName)
		if verr != nil {
			return fmt.Errorf("cluster: failed to look up VPC for %s: %w", compoundName, verr)
		}
		if !found {
			return fmt.Errorf("cluster: no PTD-managed VPC found for %s (run persistent first)", compoundName)
		}
		vpcID = id
		subnetIDs, err = aws.GetWorkloadPrivateSubnetIDs(ctx, awsCreds, region, compoundName, id, nil)
		if err != nil {
			return fmt.Errorf("cluster: failed to look up private subnets for %s: %w", compoundName, err)
		}
	}

	kubeconfig := ""
	oidcThumbprint := ""
	if exists {
		endpoint, caCert, oidcIssuerURL, cerr := aws.GetClusterInfo(ctx, awsCreds, region, clusterName)
		if cerr != nil {
			return fmt.Errorf("cluster: failed to get cluster info for %s: %w", clusterName, cerr)
		}
		proxyURL := ""
		if !cfg.TailscaleEnabled {
			proxyURL = fmt.Sprintf("socks5://localhost:%d", proxy.WorkloadPort(compoundName))
		}
		kubeconfig, cerr = kube.BuildEKSKubeconfigString(endpoint, caCert, clusterName, region, proxyURL)
		if cerr != nil {
			return fmt.Errorf("cluster: %w", cerr)
		}
		// OIDC provider thumbprint: dial the issuer's jwks_uri netloc exactly as
		// Python's ptd.oidc.get_thumbprint does. The OIDC endpoint is public, so this
		// needs no proxy.
		if oidcIssuerURL != "" {
			oidcThumbprint, cerr = aws.GetOIDCThumbprint(ctx, oidcIssuerURL)
			if cerr != nil {
				return fmt.Errorf("cluster: failed to compute OIDC thumbprint for %s: %w", clusterName, cerr)
			}
		}
	}

	// ── Access-entry import state (only on the access-entries auth path) ────────
	var accessEntries aws.AccessEntryData
	if cfg.UsesEksAccessEntries() && exists {
		accessEntries, err = aws.GetAccessEntryData(ctx, awsCreds, region, clusterName)
		if err != nil {
			return fmt.Errorf("cluster: failed to fetch access entries for %s: %w", clusterName, err)
		}
	}

	// ── Grafana alert + dashboard files (read from the embedded assets) ─────────
	alerts, dashboards, err := loadGrafanaConfigMapFiles()
	if err != nil {
		return fmt.Errorf("cluster: failed to load grafana alert/dashboard files: %w", err)
	}

	params := awsClusterParams{
		compoundName: compoundName,
		clusterName:  clusterName,
		region:       region,
		accountID:    awsCreds.AccountID(),
		// Python's control-room _define_eks does NOT set a permissions_boundary on the
		// EKS cluster / node / IRSA roles (unlike the workload paths, which do), so the
		// live control-room roles carry no boundary. The admin identity used for
		// control-room applies also cannot set one (iam:PutRolePermissionsBoundary is
		// denied). Pass empty so the builder omits the boundary — the builder guards
		// each role with `if IAMPermissionsBoundary != ""`, so this is a no-op diff.
		iamPermissionsBoundaryARN:  "",
		requiredTags:               buildClusterRequiredTags(cfg),
		cfg:                        cfg,
		credentials:                awsCreds,
		subnetIDs:                  subnetIDs,
		vpcID:                      vpcID,
		clusterSGID:                clusterSGID,
		clusterExists:              exists,
		currentAuthMode:            authMode,
		kubeconfig:                 kubeconfig,
		accessEntries:              accessEntries,
		oidcThumbprint:             oidcThumbprint,
		grafanaDBConnection:        grafanaDBConnection,
		opsgenieKey:                opsgenieKey,
		mimirSalt:                  mimirSalt,
		mimirCreds:                 mimirCreds,
		wlAccountIDs:               wlAccountIDs,
		grafanaAlerts:              alerts,
		grafanaDashboards:          dashboards,
		protectPersistentResources: cfg.ProtectPersistentResources,
	}

	stack, err := createStack(ctx, s.Name(), s.DstTarget, func(pctx *awspulumi.Context, target types.Target) error {
		return awsClusterDeploy(pctx, target, params)
	}, envVars)
	if err != nil {
		return err
	}
	return runPulumi(ctx, stack, s.Options)
}
