package steps

import (
	"context"
	"fmt"

	"github.com/posit-dev/ptd/lib/proxy"
	"github.com/posit-dev/ptd/lib/types"
)

const (
	helmAlloyNamespace       = "alloy"
	helmLokiNamespace        = "loki"
	helmMimirNamespace       = "mimir"
	helmGrafanaNamespace     = "grafana"
	helmExternalDNSNamespace = "external-dns"
	helmNvidiaNamespace      = "nvidia-device-plugin"
	helmTraefikNamespace     = clustersTraefikNamespace
)

// HelmStep deploys Helm charts (observability stack, ingress, etc.) to workload clusters.
type HelmStep struct {
	SrcTarget types.Target
	DstTarget types.Target
	Options   StepOptions
}

func (s *HelmStep) Name() string {
	return "helm"
}

func (s *HelmStep) ProxyRequired() bool {
	return true
}

func (s *HelmStep) Set(t types.Target, controlRoomTarget types.Target, options StepOptions) {
	s.SrcTarget = controlRoomTarget
	s.DstTarget = t
	s.Options = options
}

func (s *HelmStep) Run(ctx context.Context) error {
	if s.DstTarget == nil {
		return fmt.Errorf("helm step requires a destination target")
	}

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

	switch s.DstTarget.CloudProvider() {
	case types.AWS:
		return s.runAWSInlineGo(ctx, creds, envVars)
	case types.Azure:
		return s.runAzureInlineGo(ctx, creds, envVars)
	default:
		return fmt.Errorf("unsupported cloud provider for helm: %s", s.DstTarget.CloudProvider())
	}
}
