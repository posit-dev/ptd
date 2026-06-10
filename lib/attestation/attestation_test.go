package attestation

import (
	"reflect"
	"sort"
	"testing"

	"github.com/posit-dev/ptd/lib/azure"
	"github.com/posit-dev/ptd/lib/pulumistate"
	"github.com/posit-dev/ptd/lib/types"
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

func TestResourceNameFromURN(t *testing.T) {
	tests := []struct {
		urn  string
		want string
	}{
		{"urn:pulumi:prod::proj::aws:s3/bucket:Bucket::my-bucket", "my-bucket"},
		{"urn:pulumi:prod::proj::azure-native:containerservice:ManagedCluster::aks", "aks"},
		// Component resources nest the type chain; the name is still the last segment.
		{"urn:pulumi:prod::proj::my:component:Thing$aws:ec2/vpc:Vpc::my-vpc", "my-vpc"},
		{"", ""},
		{"no-delimiters", "no-delimiters"},
	}
	for _, tt := range tests {
		t.Run(tt.urn, func(t *testing.T) {
			if got := resourceNameFromURN(tt.urn); got != tt.want {
				t.Errorf("resourceNameFromURN(%q) = %q, want %q", tt.urn, got, tt.want)
			}
		})
	}
}

func TestParseStateFileResources(t *testing.T) {
	stateJSON := `{
		"version": 3,
		"checkpoint": {
			"latest": {
				"manifest": {"time": "2025-01-15T10:30:00Z", "version": "3.100.0"},
				"resources": [
					{"type": "pulumi:pulumi:Stack", "urn": "urn:pulumi:prod::proj::pulumi:pulumi:Stack::proj-prod"},
					{"type": "pulumi:providers:aws", "urn": "urn:pulumi:prod::proj::pulumi:providers:aws::default"},
					{"type": "aws:s3/bucket:Bucket", "urn": "urn:pulumi:prod::proj::aws:s3/bucket:Bucket::my-bucket", "id": "my-bucket-1234"},
					{"type": "aws:ec2/vpc:Vpc", "urn": "urn:pulumi:prod::proj::aws:ec2/vpc:Vpc::my-vpc", "id": "vpc-0abc"}
				]
			}
		}
	}`

	summary, err := parseStateFile([]byte(stateJSON), ".pulumi/stacks/ptd-aws-workload-persistent/prod.json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Internal resources are excluded; only the two cloud resources remain.
	if len(summary.Resources) != 2 {
		t.Fatalf("got %d resources, want 2", len(summary.Resources))
	}

	// Resources are sorted by type, so the VPC precedes the bucket.
	want := []ResourceDetail{
		{Name: "my-vpc", Type: "aws:ec2/vpc:Vpc", ID: "vpc-0abc", URN: "urn:pulumi:prod::proj::aws:ec2/vpc:Vpc::my-vpc"},
		{Name: "my-bucket", Type: "aws:s3/bucket:Bucket", ID: "my-bucket-1234", URN: "urn:pulumi:prod::proj::aws:s3/bucket:Bucket::my-bucket"},
	}
	for i, w := range want {
		got := summary.Resources[i]
		if got != w {
			t.Errorf("Resources[%d] = %+v, want %+v", i, got, w)
		}
	}
}

func TestResourceDetailDisplayID(t *testing.T) {
	urn := "urn:pulumi:prod::proj::ptd:AzureBastion::bastion"
	tests := []struct {
		name string
		res  ResourceDetail
		want string
	}{
		{"cloud id present", ResourceDetail{ID: "vpc-0abc", URN: urn}, "vpc-0abc"},
		{"empty id falls back to urn", ResourceDetail{ID: "", URN: urn}, urn},
		{"literal none falls back to urn", ResourceDetail{ID: "none", URN: urn}, urn},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.res.DisplayID(); got != tt.want {
				t.Errorf("DisplayID() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBootstrapSiteSecretFields(t *testing.T) {
	got := bootstrapSiteSecretFields("main")
	want := []string{
		"dev-db-password",
		"keycloak-db-password",
		"keycloak-db-user",
		"pkg-db-password",
		"pkg-secret-key",
		"pub-db-password",
		"pub-secret-key",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("bootstrapSiteSecretFields(main) = %v, want %v", got, want)
	}
}

func TestCollectBootstrapResourcesAzure(t *testing.T) {
	sites := map[string]types.SiteConfig{
		"main": {Spec: types.SiteConfigSpec{Domain: "example.com"}},
	}
	target := azure.NewTarget("example01-production", "sub-id", "tenant-id", "westeurope", sites, "admin-group-id", "", nil)

	res := collectBootstrapResources(target)

	// 5 foundational resources + 3 role assignments + 7 per-site secrets.
	if len(res) != 15 {
		t.Fatalf("got %d bootstrap resources, want 15", len(res))
	}

	// Spot-check that key foundational resources are present with derived IDs.
	byName := make(map[string]ResourceDetail, len(res))
	for _, r := range res {
		byName[r.Name] = r
	}
	for _, name := range []string{
		target.ResourceGroupName(),
		target.StateBucketName(),
		target.VaultName(),
		"posit-team-dedicated", // consts.AzKeyName
		"main-pub-db-password", // a per-site secret
	} {
		r, ok := byName[name]
		if !ok {
			t.Errorf("expected bootstrap resource %q to be present", name)
			continue
		}
		if r.DisplayID() == "" {
			t.Errorf("bootstrap resource %q has empty DisplayID", name)
		}
	}

	// Results must be sorted by type then name.
	if !sort.SliceIsSorted(res, func(i, j int) bool {
		if res[i].Type != res[j].Type {
			return res[i].Type < res[j].Type
		}
		return res[i].Name < res[j].Name
	}) {
		t.Error("bootstrap resources are not sorted by type then name")
	}
}

func TestExtractKubernetesObject(t *testing.T) {
	tests := []struct {
		name string
		res  pulumistate.PulumiResource
		want KubernetesObject
	}{
		{
			name: "namespaced object with uid",
			res: pulumistate.PulumiResource{
				Type: "kubernetes:core/v1:ConfigMap",
				URN:  "urn:pulumi:p::proj::kubernetes:core/v1:ConfigMap::cm",
				Outputs: map[string]interface{}{
					"kind": "ConfigMap",
					"metadata": map[string]interface{}{
						"name":      "alloy-config",
						"namespace": "alloy",
						"uid":       "abc-123",
					},
				},
			},
			want: KubernetesObject{Kind: "ConfigMap", Namespace: "alloy", Name: "alloy-config", UID: "abc-123"},
		},
		{
			name: "cluster-scoped object has no namespace",
			res: pulumistate.PulumiResource{
				Type: "kubernetes:core/v1:Namespace",
				URN:  "urn:pulumi:p::proj::kubernetes:core/v1:Namespace::ns",
				Outputs: map[string]interface{}{
					"kind":     "Namespace",
					"metadata": map[string]interface{}{"name": "posit-team", "uid": "ns-1"},
				},
			},
			want: KubernetesObject{Kind: "Namespace", Namespace: "", Name: "posit-team", UID: "ns-1"},
		},
		{
			name: "helm release: top-level name/namespace, no uid",
			res: pulumistate.PulumiResource{
				Type: "kubernetes:helm.sh/v3:Release",
				URN:  "urn:pulumi:p::proj::kubernetes:helm.sh/v3:Release::traefik",
				Outputs: map[string]interface{}{
					"name":      "traefik",
					"namespace": "traefik",
				},
			},
			want: KubernetesObject{Kind: "Helm Release", Namespace: "traefik", Name: "traefik", UID: ""},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractKubernetesObject(tt.res)
			if got != tt.want {
				t.Errorf("extractKubernetesObject() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestKubernetesObjectDisplay(t *testing.T) {
	ns := KubernetesObject{Kind: "Namespace", Name: "posit-team", UID: "ns-1"}
	if ns.DisplayNamespace() != "—" {
		t.Errorf("cluster-scoped DisplayNamespace() = %q, want —", ns.DisplayNamespace())
	}
	if ns.DisplayUID() != "ns-1" {
		t.Errorf("DisplayUID() = %q, want ns-1", ns.DisplayUID())
	}

	release := KubernetesObject{Kind: "Helm Release", Namespace: "traefik", Name: "traefik"}
	if release.DisplayUID() != "— (release)" {
		t.Errorf("Helm release DisplayUID() = %q, want — (release)", release.DisplayUID())
	}
	if release.DisplayNamespace() != "traefik" {
		t.Errorf("DisplayNamespace() = %q, want traefik", release.DisplayNamespace())
	}

	orphan := KubernetesObject{Kind: "ConfigMap", Name: "x"}
	if orphan.DisplayUID() != "—" {
		t.Errorf("missing-uid non-release DisplayUID() = %q, want —", orphan.DisplayUID())
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

func TestSummarizeStateFiles(t *testing.T) {
	realStack := `{
		"version": 3,
		"checkpoint": {
			"latest": {
				"manifest": {"time": "2025-01-15T10:30:00Z", "version": "3.100.0"},
				"resources": [
					{"type": "pulumi:pulumi:Stack", "urn": "urn:pulumi:prod::proj::pulumi:pulumi:Stack::proj-prod"},
					{"type": "azure-native:containerservice:ManagedCluster", "urn": "urn:pulumi:prod::proj::azure-native:containerservice:ManagedCluster::aks"}
				]
			}
		}
	}`
	emptyStack := `{
		"version": 3,
		"checkpoint": {
			"latest": {
				"manifest": {"time": "2025-01-15T10:30:00Z", "version": "3.100.0"},
				"resources": []
			}
		}
	}`

	t.Run("filters out (N) backup files", func(t *testing.T) {
		files := map[string][]byte{
			".pulumi/stacks/ptd-azure-workload-aks/prod.json":    []byte(realStack),
			".pulumi/stacks/ptd-azure-workload-aks/prod(1).json": []byte(realStack),
		}
		summaries, err := summarizeStateFiles(files)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(summaries) != 1 {
			t.Fatalf("got %d summaries, want 1", len(summaries))
		}
		if summaries[0].StackName != "prod" {
			t.Errorf("StackName = %q, want %q", summaries[0].StackName, "prod")
		}
	})

	t.Run("filters out stacks with zero resources", func(t *testing.T) {
		files := map[string][]byte{
			".pulumi/stacks/ptd-azure-workload-aks/prod.json":       []byte(realStack),
			".pulumi/stacks/ptd-azure-workload-acr-cache/prod.json": []byte(emptyStack),
		}
		summaries, err := summarizeStateFiles(files)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(summaries) != 1 {
			t.Fatalf("got %d summaries, want 1", len(summaries))
		}
		if summaries[0].ProjectName != "ptd-azure-workload-aks" {
			t.Errorf("ProjectName = %q, want %q", summaries[0].ProjectName, "ptd-azure-workload-aks")
		}
	})

	t.Run("returns all valid stacks with resources", func(t *testing.T) {
		files := map[string][]byte{
			".pulumi/stacks/ptd-azure-workload-aks/prod.json":      []byte(realStack),
			".pulumi/stacks/ptd-azure-workload-clusters/prod.json": []byte(realStack),
		}
		summaries, err := summarizeStateFiles(files)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(summaries) != 2 {
			t.Fatalf("got %d summaries, want 2", len(summaries))
		}
		names := []string{summaries[0].ProjectName, summaries[1].ProjectName}
		sort.Strings(names)
		if names[0] != "ptd-azure-workload-aks" || names[1] != "ptd-azure-workload-clusters" {
			t.Errorf("got projects %v, want [ptd-azure-workload-aks ptd-azure-workload-clusters]", names)
		}
	})
}
