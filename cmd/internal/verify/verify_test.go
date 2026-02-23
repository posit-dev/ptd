package verify

import (
	"strings"
	"testing"
)

func TestGenerateConfig_ConnectOnly(t *testing.T) {
	site := &SiteCR{
		Spec: SiteSpec{
			Domain: "example.com",
			Connect: &ProductSpec{
				Auth: &AuthSpec{Type: "saml"},
			},
		},
	}

	config, err := GenerateConfig(site, "my-deployment")
	if err != nil {
		t.Fatalf("GenerateConfig returned error: %v", err)
	}

	if config == "" {
		t.Fatal("expected non-empty config")
	}

	// Auth provider should come from Connect when present
	if !strings.Contains(config, `provider = "saml"`) {
		t.Errorf("expected saml auth provider, got:\n%s", config)
	}

	// Connect should be enabled with the correct URL
	if !strings.Contains(config, `url = "https://connect.example.com"`) {
		t.Errorf("expected connect URL, got:\n%s", config)
	}

	// Workbench section should be present (disabled)
	if !strings.Contains(config, `[workbench]`) {
		t.Errorf("expected workbench section, got:\n%s", config)
	}
}

func TestGenerateConfig_AuthProviderPrecedence(t *testing.T) {
	// Connect auth takes precedence over Workbench auth
	site := &SiteCR{
		Spec: SiteSpec{
			Domain: "example.com",
			Connect: &ProductSpec{
				Auth: &AuthSpec{Type: "saml"},
			},
			Workbench: &ProductSpec{
				Auth: &AuthSpec{Type: "ldap"},
			},
		},
	}

	config, err := GenerateConfig(site, "test")
	if err != nil {
		t.Fatalf("GenerateConfig returned error: %v", err)
	}

	if !strings.Contains(config, `provider = "saml"`) {
		t.Errorf("expected Connect auth (saml) to win, got:\n%s", config)
	}
}

func TestGenerateConfig_WorkbenchAuthFallback(t *testing.T) {
	// When Connect has no auth spec, fall back to Workbench auth
	site := &SiteCR{
		Spec: SiteSpec{
			Domain: "example.com",
			Connect: &ProductSpec{},
			Workbench: &ProductSpec{
				Auth: &AuthSpec{Type: "ldap"},
			},
		},
	}

	config, err := GenerateConfig(site, "test")
	if err != nil {
		t.Fatalf("GenerateConfig returned error: %v", err)
	}

	if !strings.Contains(config, `provider = "ldap"`) {
		t.Errorf("expected Workbench auth (ldap) as fallback, got:\n%s", config)
	}
}

func TestGenerateConfig_DefaultAuth(t *testing.T) {
	// When no product has an auth spec, default to oidc
	site := &SiteCR{
		Spec: SiteSpec{
			Domain:  "example.com",
			Connect: &ProductSpec{},
		},
	}

	config, err := GenerateConfig(site, "test")
	if err != nil {
		t.Fatalf("GenerateConfig returned error: %v", err)
	}

	if !strings.Contains(config, `provider = "oidc"`) {
		t.Errorf("expected default oidc auth, got:\n%s", config)
	}
}

func TestGenerateConfig_CustomDomainPrefix(t *testing.T) {
	site := &SiteCR{
		Spec: SiteSpec{
			Domain: "example.com",
			Connect: &ProductSpec{
				DomainPrefix: "rsconnect",
			},
		},
	}

	config, err := GenerateConfig(site, "test")
	if err != nil {
		t.Fatalf("GenerateConfig returned error: %v", err)
	}

	if !strings.Contains(config, `url = "https://rsconnect.example.com"`) {
		t.Errorf("expected custom domain prefix in URL, got:\n%s", config)
	}
}

func TestGenerateConfig_EmptyAuthType(t *testing.T) {
	// Auth.Type == "" should fall through to the default "oidc", not produce provider = ""
	site := &SiteCR{
		Spec: SiteSpec{
			Domain: "example.com",
			Connect: &ProductSpec{
				Auth: &AuthSpec{Type: ""},
			},
		},
	}

	config, err := GenerateConfig(site, "test")
	if err != nil {
		t.Fatalf("GenerateConfig returned error: %v", err)
	}

	if !strings.Contains(config, `provider = "oidc"`) {
		t.Errorf("expected oidc default when Auth.Type is empty, got:\n%s", config)
	}
}

func TestGenerateConfig_EmptyDomain(t *testing.T) {
	// Empty domain should produce a URL with an empty domain segment, not panic
	site := &SiteCR{
		Spec: SiteSpec{
			Domain:  "",
			Connect: &ProductSpec{},
		},
	}

	config, err := GenerateConfig(site, "test")
	if err != nil {
		t.Fatalf("GenerateConfig returned error: %v", err)
	}

	if !strings.Contains(config, `url = "https://connect."`) {
		t.Errorf("expected connect URL with empty domain, got:\n%s", config)
	}
}

func TestBuildProductURL_BaseDomainOverride(t *testing.T) {
	spec := &ProductSpec{
		BaseDomain: "custom.org",
	}
	got := buildProductURL(spec, "connect", "example.com")
	want := "https://connect.custom.org"
	if got != want {
		t.Errorf("buildProductURL with BaseDomain = %q, want %q", got, want)
	}
}

func TestBuildProductURL_DomainPrefixAndBaseDomain(t *testing.T) {
	spec := &ProductSpec{
		DomainPrefix: "rsc",
		BaseDomain:   "custom.org",
	}
	got := buildProductURL(spec, "connect", "example.com")
	want := "https://rsc.custom.org"
	if got != want {
		t.Errorf("buildProductURL with DomainPrefix+BaseDomain = %q, want %q", got, want)
	}
}

func TestParseJobStatus(t *testing.T) {
	tests := []struct {
		name        string
		output      string
		wantDone    bool
		wantSuccess bool
	}{
		{
			name:        "job completed successfully",
			output:      "True ",
			wantDone:    true,
			wantSuccess: true,
		},
		{
			name:        "job failed",
			output:      "False True",
			wantDone:    true,
			wantSuccess: false,
		},
		{
			name:        "both conditions set - complete wins",
			output:      "True True",
			wantDone:    true,
			wantSuccess: true,
		},
		{
			name:        "job still running (no conditions yet)",
			output:      "",
			wantDone:    false,
			wantSuccess: false,
		},
		{
			name:        "job still running (False conditions)",
			output:      "False False",
			wantDone:    false,
			wantSuccess: false,
		},
		{
			name:        "whitespace only",
			output:      "   \n  ",
			wantDone:    false,
			wantSuccess: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotDone, gotSuccess := parseJobStatus(tt.output)
			if gotDone != tt.wantDone || gotSuccess != tt.wantSuccess {
				t.Errorf("parseJobStatus(%q) = (%v, %v), want (%v, %v)",
					tt.output, gotDone, gotSuccess, tt.wantDone, tt.wantSuccess)
			}
		})
	}
}

