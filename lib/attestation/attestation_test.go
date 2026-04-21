package attestation

import (
	"testing"
)

func TestParseStateFile(t *testing.T) {
	tests := []struct {
		name          string
		json          string
		key           string
		wantProject   string
		wantStack     string
		wantResources int
		wantVersion   string
		wantErr       bool
	}{
		{
			name: "typical state file",
			json: `{
				"version": 3,
				"checkpoint": {
					"latest": {
						"manifest": {"time": "2025-01-15T10:30:00Z", "version": "3.100.0"},
						"resources": [
							{"type": "pulumi:pulumi:Stack", "urn": "urn:pulumi:prod::proj::pulumi:pulumi:Stack::proj-prod"},
							{"type": "pulumi:providers:aws", "urn": "urn:pulumi:prod::proj::pulumi:providers:aws::default"},
							{"type": "aws:s3:Bucket", "urn": "urn:pulumi:prod::proj::aws:s3:Bucket::my-bucket"},
							{"type": "aws:ec2:Vpc", "urn": "urn:pulumi:prod::proj::aws:ec2:Vpc::my-vpc"}
						]
					}
				}
			}`,
			key:           ".pulumi/stacks/ptd-aws-workload-persistent/prod.json",
			wantProject:   "ptd-aws-workload-persistent",
			wantStack:     "prod",
			wantResources: 2, // excludes pulumi:pulumi: and pulumi:providers:
			wantVersion:   "3.100.0",
		},
		{
			name: "empty resources",
			json: `{
				"version": 3,
				"checkpoint": {
					"latest": {
						"manifest": {"time": "2025-01-15T10:30:00Z", "version": "3.100.0"},
						"resources": []
					}
				}
			}`,
			key:           ".pulumi/stacks/ptd-aws-workload-eks/staging.json",
			wantProject:   "ptd-aws-workload-eks",
			wantStack:     "staging",
			wantResources: 0,
			wantVersion:   "3.100.0",
		},
		{
			name: "only internal resources",
			json: `{
				"version": 3,
				"checkpoint": {
					"latest": {
						"manifest": {"time": "2025-01-15T10:30:00Z", "version": "3.100.0"},
						"resources": [
							{"type": "pulumi:pulumi:Stack", "urn": "urn:pulumi:prod::proj::pulumi:pulumi:Stack::proj"},
							{"type": "pulumi:providers:aws", "urn": "urn:pulumi:prod::proj::pulumi:providers:aws::default"}
						]
					}
				}
			}`,
			key:           ".pulumi/stacks/proj/prod.json",
			wantProject:   "proj",
			wantStack:     "prod",
			wantResources: 0,
		},
		{
			name: "missing timestamp uses zero time",
			json: `{
				"version": 3,
				"checkpoint": {
					"latest": {
						"manifest": {"time": "", "version": "3.100.0"},
						"resources": []
					}
				}
			}`,
			key:         ".pulumi/stacks/proj/prod.json",
			wantProject: "proj",
			wantStack:   "prod",
			wantVersion: "3.100.0",
		},
		{
			name:    "malformed JSON",
			json:    `{not valid json`,
			key:     ".pulumi/stacks/proj/prod.json",
			wantErr: true,
		},
		{
			name: "short key path",
			json: `{
				"version": 3,
				"checkpoint": {
					"latest": {
						"manifest": {"time": "2025-01-15T10:30:00Z", "version": "3.100.0"},
						"resources": []
					}
				}
			}`,
			key:         "short.json",
			wantProject: "unknown",
			wantStack:   "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			summary, err := parseStateFile([]byte(tt.json), tt.key)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if summary.ProjectName != tt.wantProject {
				t.Errorf("ProjectName = %q, want %q", summary.ProjectName, tt.wantProject)
			}
			if summary.StackName != tt.wantStack {
				t.Errorf("StackName = %q, want %q", summary.StackName, tt.wantStack)
			}
			if summary.ResourceCount != tt.wantResources {
				t.Errorf("ResourceCount = %d, want %d", summary.ResourceCount, tt.wantResources)
			}
			if tt.wantVersion != "" && summary.PulumiVersion != tt.wantVersion {
				t.Errorf("PulumiVersion = %q, want %q", summary.PulumiVersion, tt.wantVersion)
			}
			if summary.StateKey != tt.key {
				t.Errorf("StateKey = %q, want %q", summary.StateKey, tt.key)
			}
		})
	}
}

func TestStepNameFromProject(t *testing.T) {
	tests := []struct {
		project string
		want    string
	}{
		{"ptd-aws-workload-persistent", "persistent"},
		{"ptd-aws-workload-eks", "eks"},
		{"ptd-azure-workload-aks", "aks"},
		{"ptd-aws-workload-postgres-config", "postgres-config"},
		{"ptd-aws-workload-clusters", "clusters"},
		{"ptd-azure-workload-helm", "helm"},
		{"ptd-aws-workload-sites", "sites"},
		// Short names with fewer than 4 parts return the full name
		{"short", "short"},
		{"two-parts", "two-parts"},
		{"three-parts-here", "three-parts-here"},
	}

	for _, tt := range tests {
		t.Run(tt.project, func(t *testing.T) {
			s := StackSummary{ProjectName: tt.project}
			got := s.StepNameFromProject()
			if got != tt.want {
				t.Errorf("StepNameFromProject(%q) = %q, want %q", tt.project, got, tt.want)
			}
		})
	}
}

func TestPurposeForStack(t *testing.T) {
	tests := []struct {
		name    string
		project string
		cloud   string
		want    string
	}{
		{"aws persistent", "ptd-aws-workload-persistent", "aws", stackPurposes["aws"]["persistent"]},
		{"aws eks", "ptd-aws-workload-eks", "aws", stackPurposes["aws"]["eks"]},
		{"azure aks", "ptd-azure-workload-aks", "azure", stackPurposes["azure"]["aks"]},
		{"azure persistent", "ptd-azure-workload-persistent", "azure", stackPurposes["azure"]["persistent"]},
		{"azure acr-cache", "ptd-azure-workload-acr-cache", "azure", stackPurposes["azure"]["acr-cache"]},
		{"unknown stack", "ptd-aws-workload-foobar", "aws", ""},
		{"unknown cloud falls back to aws", "ptd-gcp-workload-persistent", "gcp", stackPurposes["aws"]["persistent"]},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := purposeForStack(tt.project, tt.cloud)
			if got != tt.want {
				t.Errorf("purposeForStack(%q, %q) = %q, want %q", tt.project, tt.cloud, got, tt.want)
			}
		})
	}
}

func TestCleanVersion(t *testing.T) {
	tests := []struct {
		image string
		want  string
	}{
		{"ghcr.io/rstudio/rstudio-connect:ubuntu2204-2024.01.0", "2024.01.0"},
		{"ghcr.io/rstudio/rstudio-connect:ubuntu2404-2024.06.0", "2024.06.0"},
		{"ghcr.io/rstudio/rstudio-connect:centos7-2023.12.0", "2023.12.0"},
		{"ghcr.io/rstudio/rstudio-connect:rhel9-2024.03.0", "2024.03.0"},
		{"ghcr.io/rstudio/rstudio-connect:2024.01.0", "2024.01.0"},
		{"no-colon-image", "no-colon-image"},
		{"registry:5000/image:1.2.3", "1.2.3"},
	}

	for _, tt := range tests {
		t.Run(tt.image, func(t *testing.T) {
			got := cleanVersion(tt.image)
			if got != tt.want {
				t.Errorf("cleanVersion(%q) = %q, want %q", tt.image, got, tt.want)
			}
		})
	}
}

func TestDefaultPrefix(t *testing.T) {
	tests := []struct {
		name     string
		explicit string
		fallback string
		want     string
	}{
		{"explicit overrides fallback", "custom", "pub", "custom"},
		{"empty explicit returns fallback", "", "pub", "pub"},
		{"both empty", "", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := defaultPrefix(tt.explicit, tt.fallback)
			if got != tt.want {
				t.Errorf("defaultPrefix(%q, %q) = %q, want %q", tt.explicit, tt.fallback, got, tt.want)
			}
		})
	}
}
