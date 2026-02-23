package verify

import (
	"bytes"
	"fmt"

	"github.com/BurntSushi/toml"
)

// VIPConfig represents the vip.toml configuration structure
type VIPConfig struct {
	General        GeneralConfig        `toml:"general"`
	Connect        ProductConfig        `toml:"connect"`
	Workbench      ProductConfig        `toml:"workbench"`
	PackageManager ProductConfig        `toml:"package_manager"`
	Auth           AuthConfig           `toml:"auth"`
	Email          DisableableConfig    `toml:"email"`
	Monitoring     DisableableConfig    `toml:"monitoring"`
	Security       SecurityConfig       `toml:"security"`
}

type GeneralConfig struct {
	DeploymentName string `toml:"deployment_name"`
}

type ProductConfig struct {
	Enabled bool   `toml:"enabled"`
	URL     string `toml:"url,omitempty"`
}

type AuthConfig struct {
	Provider string `toml:"provider"`
}

type DisableableConfig struct {
	Enabled bool `toml:"enabled"`
}

type SecurityConfig struct {
	PolicyChecksEnabled bool `toml:"policy_checks_enabled"`
}

// SiteCR represents the Kubernetes Site custom resource
type SiteCR struct {
	Spec SiteSpec `yaml:"spec"`
}

type SiteSpec struct {
	Domain         string              `yaml:"domain"`
	Connect        *ProductSpec        `yaml:"connect,omitempty"`
	Workbench      *ProductSpec        `yaml:"workbench,omitempty"`
	PackageManager *ProductSpec        `yaml:"packageManager,omitempty"`
	Keycloak       *KeycloakSpec       `yaml:"keycloak,omitempty"`
}

type ProductSpec struct {
	DomainPrefix string   `yaml:"domainPrefix,omitempty"`
	BaseDomain   string   `yaml:"baseDomain,omitempty"`
	Auth         *AuthSpec `yaml:"auth,omitempty"`
}

type AuthSpec struct {
	Type string `yaml:"type"`
}

type KeycloakSpec struct {
	Enabled bool `yaml:"enabled"`
}

// GenerateConfig generates a vip.toml configuration from a parsed Site CR
func GenerateConfig(site *SiteCR, targetName string) (string, error) {
	config := VIPConfig{
		General: GeneralConfig{
			DeploymentName: targetName,
		},
		Email: DisableableConfig{
			Enabled: false,
		},
		Monitoring: DisableableConfig{
			Enabled: false,
		},
		Security: SecurityConfig{
			PolicyChecksEnabled: false,
		},
	}

	// Determine auth provider
	authProvider := "oidc" // default
	if site.Spec.Connect != nil && site.Spec.Connect.Auth != nil {
		authProvider = site.Spec.Connect.Auth.Type
	} else if site.Spec.Workbench != nil && site.Spec.Workbench.Auth != nil {
		authProvider = site.Spec.Workbench.Auth.Type
	}
	config.Auth = AuthConfig{Provider: authProvider}

	// Configure Connect
	if site.Spec.Connect != nil {
		productURL := buildProductURL(site.Spec.Connect, "connect", site.Spec.Domain)
		config.Connect = ProductConfig{
			Enabled: true,
			URL:     productURL,
		}
	} else {
		config.Connect = ProductConfig{Enabled: false}
	}

	// Configure Workbench
	if site.Spec.Workbench != nil {
		productURL := buildProductURL(site.Spec.Workbench, "workbench", site.Spec.Domain)
		config.Workbench = ProductConfig{
			Enabled: true,
			URL:     productURL,
		}
	} else {
		config.Workbench = ProductConfig{Enabled: false}
	}

	// Configure Package Manager
	if site.Spec.PackageManager != nil {
		productURL := buildProductURL(site.Spec.PackageManager, "packagemanager", site.Spec.Domain)
		config.PackageManager = ProductConfig{
			Enabled: true,
			URL:     productURL,
		}
	} else {
		config.PackageManager = ProductConfig{Enabled: false}
	}

	// Encode to TOML
	var buf bytes.Buffer
	encoder := toml.NewEncoder(&buf)
	if err := encoder.Encode(config); err != nil {
		return "", fmt.Errorf("failed to encode TOML: %w", err)
	}

	return buf.String(), nil
}

// buildProductURL constructs the product URL from the product spec
func buildProductURL(spec *ProductSpec, defaultPrefix, baseDomain string) string {
	prefix := defaultPrefix
	if spec.DomainPrefix != "" {
		prefix = spec.DomainPrefix
	}

	domain := baseDomain
	if spec.BaseDomain != "" {
		domain = spec.BaseDomain
	}

	return fmt.Sprintf("https://%s.%s", prefix, domain)
}
