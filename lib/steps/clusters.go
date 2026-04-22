package steps

import (
	"context"
	"fmt"

	"github.com/posit-dev/ptd/lib/proxy"
	"github.com/posit-dev/ptd/lib/types"
)

const (
	clustersPositTeamNamespace       = "posit-team"
	clustersPositTeamSystemNamespace = "posit-team-system"
	clustersHelmControllerNamespace  = "helm-controller"
	clustersKubeSystemNamespace      = "kube-system"
	clustersKarpenterNamespace       = "kube-system"
	// clustersTeamOperatorServiceAccount is the Helm chart default service account name.
	clustersTeamOperatorServiceAccount = "team-operator-controller-manager"
	// clustersDefaultTeamOperatorChartVersion is used when no chart version is configured.
	clustersDefaultTeamOperatorChartVersion = "v1.23.1"
	// clustersTraefikForwardAuthSA matches the Python Roles.TRAEFIK_FORWARD_AUTH value.
	clustersTraefikForwardAuthSA = "traefik-forward-auth.posit.team"

	// Azure role definition IDs (from python-pulumi/src/ptd/azure_roles.py)
	azRoleACRPull                   = "7f951dda-4ed3-4680-a7ca-43fe172d538d"
	azRoleNetworkContributor        = "4d97b98b-1d4f-4787-a291-c67834d212e7"
	azRoleReader                    = "acdd72a7-3385-48ef-bd42-f606fba81ae7"
	azRoleDNSZoneContributor        = "befefa01-2a29-4197-83a8-272ff33ce314"
	azRoleStorageAccountContributor = "17d1049b-9a84-46fb-8f53-869881c3d3ab"

	// Azure K8s namespaces for CertManager and Traefik
	clustersCertManagerNamespace = "cert-manager"
	clustersTraefikNamespace     = "traefik"
)

// ClustersStep deploys the per-cluster resources (IAM roles, K8s operators, etc.)
// for both AWS and Azure workloads.
type ClustersStep struct {
	SrcTarget types.Target
	DstTarget types.Target
	Options   StepOptions
}

func (s *ClustersStep) Name() string {
	return "clusters"
}

func (s *ClustersStep) ProxyRequired() bool {
	return true
}

func (s *ClustersStep) Set(t types.Target, controlRoomTarget types.Target, options StepOptions) {
	s.SrcTarget = controlRoomTarget
	s.DstTarget = t
	s.Options = options
}

func (s *ClustersStep) Run(ctx context.Context) error {
	if s.DstTarget == nil {
		return fmt.Errorf("clusters step requires a destination target")
	}

	creds, err := s.DstTarget.Credentials(ctx)
	if err != nil {
		return err
	}
	envVars, err := prepareEnvVarsForPulumi(ctx, s.DstTarget, creds)
	if err != nil {
		return err
	}

	// clusters step always needs proxy for K8s connectivity
	if !s.DstTarget.TailscaleEnabled() {
		envVars["ALL_PROXY"] = fmt.Sprintf("socks5://localhost:%d", proxy.WorkloadPort(s.DstTarget.Name()))
	}

	switch s.DstTarget.CloudProvider() {
	case types.AWS:
		return s.runAWSInlineGo(ctx, creds, envVars)
	case types.Azure:
		return s.runAzureInlineGo(ctx, creds, envVars)
	default:
		return fmt.Errorf("unsupported cloud provider for clusters: %s", s.DstTarget.CloudProvider())
	}
}
