package attestation

import (
	"strings"
	"testing"
)

func TestGenerateAzurePersistentProse(t *testing.T) {
	t.Run("corrected storage facts, chronicle off", func(t *testing.T) {
		cfg := &InfraConfig{Cloud: "azure", VnetCidr: "10.0.0.0/16", ChronicleEnabled: false}
		got := generateAzurePersistentProse(cfg)

		// Must contain the corrected facts.
		mustContain := []string{
			"Azure Files (NFS) share for Posit Package Manager",
			"Azure NetApp Files volumes",
			"Posit Connect",
			"Posit Workbench user home directories",
			"PostgreSQL Flexible Server",
			"application metadata for Posit Connect, Posit Workbench, and Posit Package Manager",
			"`loki` blob container",
			"`mimir-blocks` blob container",
			"created by the Mimir application at runtime",
			"shared Azure Storage account",
		}
		for _, s := range mustContain {
			if !strings.Contains(got, s) {
				t.Errorf("expected prose to contain %q\n--- prose ---\n%s", s, got)
			}
		}

		// Must NOT contain the old, wrong phrasing.
		mustNotContain := []string{
			"Azure Storage accounts for Loki logs, Mimir metrics, Package Manager cache, and Chronicle telemetry",
			"`chronicle` blob container",
			"chronicle",
		}
		for _, s := range mustNotContain {
			if strings.Contains(strings.ToLower(got), strings.ToLower(s)) {
				t.Errorf("expected prose NOT to contain %q\n--- prose ---\n%s", s, got)
			}
		}
	})

	t.Run("chronicle on adds chronicle container", func(t *testing.T) {
		cfg := &InfraConfig{Cloud: "azure", VnetCidr: "10.0.0.0/16", ChronicleEnabled: true}
		got := generateAzurePersistentProse(cfg)
		if !strings.Contains(got, "`chronicle` blob container") {
			t.Errorf("expected prose to contain chronicle container when enabled\n--- prose ---\n%s", got)
		}
	})
}

func TestGenerateClustersProseNamespaces(t *testing.T) {
	cfg := &InfraConfig{Cloud: "azure"}
	got := generateClustersProse(cfg)
	for _, ns := range []string{"`posit-team`", "`posit-team-system`", "`helm-controller`", "`cert-manager`", "`traefik`", "`coredns-custom`"} {
		if !strings.Contains(got, ns) {
			t.Errorf("expected clusters prose to enumerate %s\n--- prose ---\n%s", ns, got)
		}
	}
	// Observability namespaces are described in the helm stack, not clusters.
	for _, ns := range []string{"`loki`", "`mimir`", "`grafana`", "`alloy`"} {
		if strings.Contains(got, ns) {
			t.Errorf("clusters prose should not mention observability namespace %s\n--- prose ---\n%s", ns, got)
		}
	}
	// Azure external-dns is described in helm, not clusters.
	if strings.Contains(strings.ToLower(got), "external dns") || strings.Contains(strings.ToLower(got), "external-dns") {
		t.Errorf("azure clusters prose should not mention external-dns\n--- prose ---\n%s", got)
	}
}

func TestGenerateClustersProseAWSNamespaces(t *testing.T) {
	cfg := &InfraConfig{Cloud: "aws"}
	got := generateClustersProse(cfg)
	// The shared cluster namespaces are enumerated on AWS too.
	for _, ns := range []string{"`posit-team`", "`posit-team-system`", "`helm-controller`", "`cert-manager`", "`traefik`"} {
		if !strings.Contains(got, ns) {
			t.Errorf("expected AWS clusters prose to enumerate %s\n--- prose ---\n%s", ns, got)
		}
	}
	// On AWS the grafana namespace is created in the clusters stack
	// (lib/steps/clusters_aws.go), so it must be enumerated here.
	if !strings.Contains(got, "`grafana`") {
		t.Errorf("expected AWS clusters prose to enumerate the grafana namespace\n--- prose ---\n%s", got)
	}
	if !strings.Contains(got, "Grafana observability stack") {
		t.Errorf("expected AWS clusters prose to describe the grafana namespace as the Grafana observability stack\n--- prose ---\n%s", got)
	}
	// The other observability namespaces remain in the helm stack.
	for _, ns := range []string{"`loki`", "`mimir`", "`alloy`"} {
		if strings.Contains(got, ns) {
			t.Errorf("AWS clusters prose should not mention observability namespace %s (helm stack)\n--- prose ---\n%s", ns, got)
		}
	}
	// kube-system CoreDNS patch is Azure-only; AWS must not mention coredns-custom.
	if strings.Contains(got, "coredns-custom") {
		t.Errorf("AWS clusters prose should not mention coredns-custom (Azure-only)\n--- prose ---\n%s", got)
	}
}

func TestGenerateHelmProse(t *testing.T) {
	t.Run("azure deploys external-dns unconditionally", func(t *testing.T) {
		// ExternalDNSEnabled false should NOT suppress the Azure external-dns prose.
		cfg := &InfraConfig{Cloud: "azure", ExternalDNSEnabled: false}
		got := generateHelmProse(cfg)
		if !strings.Contains(got, "external-dns") {
			t.Errorf("expected azure helm prose to mention external-dns\n--- prose ---\n%s", got)
		}
		if !strings.Contains(got, "azure-config-file") {
			t.Errorf("expected azure helm prose to mention azure-config-file secret\n--- prose ---\n%s", got)
		}
		if strings.Contains(strings.ToLower(got), "disabled") {
			t.Errorf("azure helm prose must not render a disabled branch\n--- prose ---\n%s", got)
		}
		for _, ns := range []string{"`grafana`", "`loki`", "`mimir`", "`alloy`", "`kube-state-metrics`"} {
			if !strings.Contains(got, ns) {
				t.Errorf("expected helm prose to mention observability namespace %s\n--- prose ---\n%s", ns, got)
			}
		}
	})

	t.Run("aws does not deploy external-dns in helm", func(t *testing.T) {
		cfg := &InfraConfig{Cloud: "aws", ExternalDNSEnabled: true}
		got := generateHelmProse(cfg)
		if strings.Contains(strings.ToLower(got), "external-dns") || strings.Contains(strings.ToLower(got), "external dns") {
			t.Errorf("aws helm prose should not mention external-dns (it is in clusters)\n--- prose ---\n%s", got)
		}
	})
}

func TestGenerateSitesProse(t *testing.T) {
	cfg := &InfraConfig{Cloud: "azure", SiteDomains: map[string]string{"main": "example.com"}}

	t.Run("uses per-site site.yaml path, chronicle off", func(t *testing.T) {
		cfg.ChronicleEnabled = false
		got := generateSitesProse(cfg)
		if !strings.Contains(got, "site_main/site.yaml") {
			t.Errorf("expected per-site site.yaml path\n--- prose ---\n%s", got)
		}
		if strings.Contains(got, "Chronicle observability agent") {
			t.Errorf("chronicle should not be mentioned when disabled\n--- prose ---\n%s", got)
		}
	})

	t.Run("chronicle on adds agent bullet", func(t *testing.T) {
		cfg.ChronicleEnabled = true
		got := generateSitesProse(cfg)
		if !strings.Contains(got, "Chronicle observability agent") {
			t.Errorf("expected chronicle agent bullet when enabled\n--- prose ---\n%s", got)
		}
	})
}

func TestProductDisplayName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"connect", "Posit Connect"},
		{"workbench", "Posit Workbench"},
		{"package-manager", "Posit Package Manager"},
		{"chronicle", "Chronicle"},
		{"chronicle-agent", "Chronicle Agent"},
		{"unknown-product", "unknown-product"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := ProductDisplayName(tt.input)
			if got != tt.want {
				t.Errorf("ProductDisplayName(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestStackOrder(t *testing.T) {
	tests := []struct {
		name string
		want int
	}{
		{"ptd-aws-workload-persistent", 1},
		{"ptd-azure-workload-persistent", 1},
		{"ptd-aws-workload-postgres-config", 2},
		{"ptd-aws-workload-eks", 3},
		{"ptd-azure-workload-aks", 3},
		{"ptd-aws-workload-clusters", 5},
		{"ptd-aws-workload-helm", 7},
		{"ptd-aws-workload-sites", 8},
		{"ptd-aws-workload-custom-thing", 4}, // unknown → 4
		{"completely-unknown", 4},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := StackOrder(tt.name)
			if got != tt.want {
				t.Errorf("StackOrder(%q) = %d, want %d", tt.name, got, tt.want)
			}
		})
	}
}
