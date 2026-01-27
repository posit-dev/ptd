package secrets

import (
	"fmt"
	"strings"

	"github.com/posit-dev/ptd/lib/helpers"
)

type SiteSecret struct {
	DevAdminToken      string `json:"dev-admin-token"` // likely to be removed
	DevClientSecret    string `json:"dev-client-secret"`
	DevDBPassword      string `json:"dev-db-password"`
	DevLicense         string `json:"dev-license"`
	DevUserToken       string `json:"dev-user-token"` // likely to be removed
	DevChronicleApiKey string `json:"dev-chronicle-api-key"`
	HomeAuthMap        string `json:"home-auth-map"`
	KeycloakDBUser     string `json:"keycloak-db-user"`
	KeycloakDBPassword string `json:"keycloak-db-password"`
	PkgDBPassword      string `json:"pkg-db-password"`
	PkgLicense         string `json:"pkg-license"`
	PkgSecretKey       string `json:"pkg-secret-key"`
	PubClientSecret    string `json:"pub-client-secret"`
	PubDBPassword      string `json:"pub-db-password"`
	PubLicense         string `json:"pub-license"`
	PubSecretKey       string `json:"pub-secret-key"`
	PubChronicleApiKey string `json:"pub-chronicle-api-key"`
}

func NewSiteSecret(siteName string) SiteSecret {
	return SiteSecret{
		DevDBPassword:      helpers.GenerateRandomString(30),
		KeycloakDBUser:     fmt.Sprintf("%s_keycloak", strings.Replace(siteName, "-", "_", -1)),
		KeycloakDBPassword: helpers.GenerateRandomString(30),
		PkgDBPassword:      helpers.GenerateRandomString(30),
		PkgSecretKey:       helpers.RsKeyGenerate(),
		PubDBPassword:      helpers.GenerateRandomString(30),
		PubSecretKey:       helpers.RsKeyGenerate(),
	}
}

type SiteSessionSecret map[string]interface{}

// SSHVaultSecret holds SSH private keys for Package Manager Git authentication
// Each field represents a different Git host (e.g., github, gitlab, bitbucket)
// The keys are stored as strings containing the full SSH private key content
type SSHVaultSecret map[string]interface{}

type AWSWorkloadSecret struct {
	ChronicleBucket      string `json:"chronicle-bucket"`
	FsDnsName            string `json:"fs-dns-name"`
	FsRootVolumeID       string `json:"fs-root-volume-id"`
	MainDatabaseID       string `json:"main-database-id"`
	MainDatabaseURL      string `json:"main-database-url"`
	PackageManagerBucket string `json:"packagemanager-bucket"`
	MimirPassword        string `json:"mimir-password"`
}

type AzureWorkloadSecret struct {
	MainDbFqdn string `json:"main_db_fqdn"`
}
