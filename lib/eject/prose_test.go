package eject

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPtdCommandDescription(t *testing.T) {
	tests := []struct {
		step string
		want string
	}{
		{"persistent", "Provisions foundational infrastructure (VPC/VNet, RDS/PostgreSQL, S3/Storage, FSx/NetApp, IAM, DNS, certificates)"},
		{"postgres-config", "Configures PostgreSQL databases, users, and grants"},
		{"eks", "Provisions EKS cluster, node groups, OIDC provider, and storage classes"},
		{"aks", "Provisions AKS cluster, node pools, managed identity, and storage classes"},
		{"clusters", "Configures Kubernetes namespaces, network policies, Team Operator, Traefik, and external DNS"},
		{"helm", "Deploys supporting Helm charts (monitoring, cert-manager, Secrets Store CSI)"},
		{"sites", "Deploys Posit products (TeamSite CRDs), ingress resources, and site configuration"},
		{"unknown-step", "Custom infrastructure step"},
	}

	for _, tt := range tests {
		t.Run(tt.step, func(t *testing.T) {
			assert.Equal(t, tt.want, ptdCommandDescription(tt.step))
		})
	}
}
