package verify

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestParseSecretData_InvalidFormat(t *testing.T) {
	cases := []struct {
		name   string
		output string
	}{
		{"empty", ""},
		{"whitespace only", "   "},
		{"single field", "dGVzdA=="},
		{"three fields", "dGVzdA== dGVzdA== dGVzdA=="},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := parseSecretData(tc.output)
			if err == nil {
				t.Fatalf("expected error for input %q, got nil", tc.output)
			}
		})
	}
}

func TestParseSecretData_InvalidBase64(t *testing.T) {
	_, _, err := parseSecretData("not-valid-base64!!! dGVzdA==")
	if err == nil {
		t.Fatal("expected error for invalid base64 in username field, got nil")
	}

	_, _, err = parseSecretData("dGVzdA== not-valid-base64!!!")
	if err == nil {
		t.Fatal("expected error for invalid base64 in password field, got nil")
	}
}

func TestParseSecretData_Valid(t *testing.T) {
	user := base64.StdEncoding.EncodeToString([]byte("admin"))
	pass := base64.StdEncoding.EncodeToString([]byte("s3cr3t"))

	gotUser, gotPass, err := parseSecretData(user + " " + pass)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotUser != "admin" {
		t.Errorf("got username %q, want %q", gotUser, "admin")
	}
	if gotPass != "s3cr3t" {
		t.Errorf("got password %q, want %q", gotPass, "s3cr3t")
	}
}

func TestWaitForJob_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately so ctx.Done() fires on first select

	_, err := WaitForJob(ctx, nil, "test-job", "test-namespace", time.Minute)
	if err == nil {
		t.Fatal("expected error for cancelled context, got nil")
	}
}

func TestGenerateConfig_NilSite(t *testing.T) {
	_, err := GenerateConfig(nil, "test")
	if err == nil {
		t.Fatal("expected error for nil site, got nil")
	}
}

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
	// Empty domain with a product that has no per-product baseDomain should return an error
	site := &SiteCR{
		Spec: SiteSpec{
			Domain:  "",
			Connect: &ProductSpec{},
		},
	}

	_, err := GenerateConfig(site, "test")
	if err == nil {
		t.Fatal("expected error for empty domain with configured product and no baseDomain, got nil")
	}
}

func TestGenerateConfig_EmptyDomainAllBaseDomains(t *testing.T) {
	// Empty site-level domain is valid when every product has its own baseDomain.
	// BaseDomain must be a bare parent domain (e.g. "custom.org"); buildProductURL
	// prepends the product prefix to produce "https://connect.custom.org".
	site := &SiteCR{
		Spec: SiteSpec{
			Domain: "",
			Connect: &ProductSpec{
				BaseDomain: "custom.org",
			},
		},
	}

	config, err := GenerateConfig(site, "test")
	if err != nil {
		t.Fatalf("expected no error when all products have baseDomain, got: %v", err)
	}
	if !strings.Contains(config, `url = "https://connect.custom.org"`) {
		t.Errorf("expected connect URL using baseDomain, got:\n%s", config)
	}
}

func TestGenerateConfig_BaseDomainWithSubdomainProducesDoublePrefix(t *testing.T) {
	// If BaseDomain is mistakenly set to a fully-qualified hostname like
	// "connect.custom.org" instead of the bare parent "custom.org", buildProductURL
	// prepends the product prefix again, producing a double-prefix URL.
	// This test documents that footgun so the behaviour is explicit and visible.
	site := &SiteCR{
		Spec: SiteSpec{
			Domain: "",
			Connect: &ProductSpec{
				BaseDomain: "connect.custom.org",
			},
		},
	}

	config, err := GenerateConfig(site, "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(config, `url = "https://connect.connect.custom.org"`) {
		t.Errorf("expected double-prefix URL from subdomain BaseDomain, got:\n%s", config)
	}
}

func TestDeriveKeycloakURL(t *testing.T) {
	// Override takes precedence over domain.
	got, err := deriveKeycloakURL("https://custom.example.com", "", true)
	if err != nil || got != "https://custom.example.com" {
		t.Fatalf("expected override URL, got %q, err %v", got, err)
	}

	// Empty domain with Keycloak enabled and no override returns an error.
	_, err = deriveKeycloakURL("", "", true)
	if err == nil {
		t.Fatal("expected error for empty domain when Keycloak is enabled, got nil")
	}

	// Empty domain with Keycloak disabled is not an error (URL won't be used).
	// Returns "" rather than a malformed "https://key." URL.
	got, err = deriveKeycloakURL("", "", false)
	if err != nil {
		t.Fatalf("unexpected error when Keycloak disabled: %v", err)
	}
	if got != "" {
		t.Errorf("unexpected URL %q when Keycloak disabled", got)
	}

	// Domain is used when no override is set and Keycloak is enabled.
	got, err = deriveKeycloakURL("", "example.com", true)
	if err != nil || got != "https://key.example.com" {
		t.Fatalf("expected derived URL, got %q, err %v", got, err)
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

func TestBuildJobSpec(t *testing.T) {
	tests := []struct {
		name             string
		opts             JobOptions
		wantAPIVersion   string
		wantKind         string
		wantContainerEnv bool
		wantCategories   bool
	}{
		{
			name: "basic spec without credentials",
			opts: JobOptions{
				Image:                "vip:latest",
				JobName:              "vip-test-123",
				ConfigName:           "vip-config-123",
				Namespace:            "default",
				CredentialsAvailable: false,
			},
			wantAPIVersion:   "batch/v1",
			wantKind:         "Job",
			wantContainerEnv: false,
		},
		{
			name: "spec with credentials injects env vars",
			opts: JobOptions{
				Image:                "vip:latest",
				JobName:              "vip-test-456",
				ConfigName:           "vip-config-456",
				Namespace:            "test-ns",
				CredentialsAvailable: true,
			},
			wantAPIVersion:   "batch/v1",
			wantKind:         "Job",
			wantContainerEnv: true,
		},
		{
			name: "spec with categories adds -m flag",
			opts: JobOptions{
				Image:      "vip:latest",
				JobName:    "vip-test-789",
				ConfigName: "vip-config-789",
				Namespace:  "default",
				Categories: "smoke",
			},
			wantAPIVersion: "batch/v1",
			wantKind:       "Job",
			wantCategories: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := buildJobSpec(tt.opts)

			// Verify by round-tripping through JSON (same path as production code).
			data, err := json.Marshal(spec)
			if err != nil {
				t.Fatalf("json.Marshal failed: %v", err)
			}
			var parsed map[string]interface{}
			if err := json.Unmarshal(data, &parsed); err != nil {
				t.Fatalf("json.Unmarshal failed: %v", err)
			}

			if parsed["apiVersion"] != tt.wantAPIVersion {
				t.Errorf("apiVersion = %v, want %v", parsed["apiVersion"], tt.wantAPIVersion)
			}
			if parsed["kind"] != tt.wantKind {
				t.Errorf("kind = %v, want %v", parsed["kind"], tt.wantKind)
			}

			// Verify metadata name and labels.
			meta := parsed["metadata"].(map[string]interface{})
			if meta["name"] != tt.opts.JobName {
				t.Errorf("metadata.name = %v, want %v", meta["name"], tt.opts.JobName)
			}
			labels := meta["labels"].(map[string]interface{})
			if labels["app.kubernetes.io/managed-by"] != "ptd" {
				t.Errorf("managed-by label = %v, want ptd", labels["app.kubernetes.io/managed-by"])
			}

			// Drill down to the container spec.
			jobSpec := parsed["spec"].(map[string]interface{})
			podTemplate := jobSpec["template"].(map[string]interface{})
			podSpec := podTemplate["spec"].(map[string]interface{})
			containers := podSpec["containers"].([]interface{})
			if len(containers) != 1 {
				t.Fatalf("expected 1 container, got %d", len(containers))
			}
			container := containers[0].(map[string]interface{})

			if container["image"] != tt.opts.Image {
				t.Errorf("container image = %v, want %v", container["image"], tt.opts.Image)
			}

			// Check volume mount path.
			mounts := container["volumeMounts"].([]interface{})
			if len(mounts) != 1 {
				t.Fatalf("expected 1 volumeMount, got %d", len(mounts))
			}
			mount := mounts[0].(map[string]interface{})
			if mount["mountPath"] != "/app/vip.toml" {
				t.Errorf("mountPath = %v, want /app/vip.toml", mount["mountPath"])
			}

			// Check env vars are present/absent based on CredentialsAvailable.
			_, hasEnv := container["env"]
			if hasEnv != tt.wantContainerEnv {
				t.Errorf("container env present = %v, want %v", hasEnv, tt.wantContainerEnv)
			}

			// Check categories flag.
			args := container["args"].([]interface{})
			hasM := false
			for _, a := range args {
				if a == "-m" {
					hasM = true
					break
				}
			}
			if hasM != tt.wantCategories {
				t.Errorf("args contains -m = %v, want %v", hasM, tt.wantCategories)
			}
		})
	}
}

func TestBuildSecretSpec(t *testing.T) {
	spec := buildSecretSpec("vip-test-credentials", "test-ns", "alice", "s3cr3t")

	data, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	if parsed["apiVersion"] != "v1" {
		t.Errorf("apiVersion = %v, want v1", parsed["apiVersion"])
	}
	if parsed["kind"] != "Secret" {
		t.Errorf("kind = %v, want Secret", parsed["kind"])
	}
	if parsed["type"] != "Opaque" {
		t.Errorf("type = %v, want Opaque", parsed["type"])
	}

	meta := parsed["metadata"].(map[string]interface{})
	if meta["name"] != "vip-test-credentials" {
		t.Errorf("metadata.name = %v, want vip-test-credentials", meta["name"])
	}
	if meta["namespace"] != "test-ns" {
		t.Errorf("metadata.namespace = %v, want test-ns", meta["namespace"])
	}

	secretData := parsed["data"].(map[string]interface{})
	gotUser, _ := base64.StdEncoding.DecodeString(secretData["username"].(string))
	gotPass, _ := base64.StdEncoding.DecodeString(secretData["password"].(string))
	if string(gotUser) != "alice" {
		t.Errorf("username = %v, want alice", string(gotUser))
	}
	if string(gotPass) != "s3cr3t" {
		t.Errorf("password = %v, want s3cr3t", string(gotPass))
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
			output:      "True,",
			wantDone:    true,
			wantSuccess: true,
		},
		{
			name:        "job failed - only Failed condition present (was false-positive before fix)",
			output:      ",True",
			wantDone:    true,
			wantSuccess: false,
		},
		{
			name:        "job failed - both conditions present",
			output:      "False,True",
			wantDone:    true,
			wantSuccess: false,
		},
		{
			name:        "both conditions set - complete wins",
			output:      "True,True",
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
			output:      "False,False",
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

