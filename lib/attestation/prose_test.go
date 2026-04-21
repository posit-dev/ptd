package attestation

import (
	"testing"
)

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
