package eject

import (
	"fmt"
	"strings"

	"github.com/posit-dev/ptd/lib/attestation"
)

// HandoffData combines attestation data with eject-specific data for document rendering.
type HandoffData struct {
	*attestation.AttestationData
	ControlRoom *ControlRoomDetails
	Resources   []ResourceInventoryEntry
	Secrets     []SecretReference
	DryRun      bool
	PTDVersion  string
}

// Resource categories for the detail sections of the handoff document.
const (
	CategoryNetwork  = "Network"
	CategoryDatabase = "Database"
	CategoryStorage  = "Storage"
	CategoryDNS      = "DNS"
	CategoryIAM      = "IAM"
	CategoryOther    = "Other"
)

// OrderedCategories defines the display order and titles for resource categories.
// Both the PDF and markdown renderers consume this list.
var OrderedCategories = []struct {
	Category string
	Title    string
}{
	{CategoryNetwork, "Network Topology"},
	{CategoryDatabase, "Database"},
	{CategoryStorage, "Storage"},
	{CategoryDNS, "DNS"},
	{CategoryIAM, "IAM"},
	{CategoryOther, "Other"},
}

// CategorizeResource maps a Pulumi resource type to a handoff document category.
func CategorizeResource(resourceType string) string {
	lower := strings.ToLower(resourceType)

	// Network
	for _, keyword := range []string{"vpc", "subnet", "securitygroup", "loadbalancer", "natgateway", "internetgateway", "routetable", "eip", "networkinterface", "vnet", "nsg", "publicip"} {
		if strings.Contains(lower, keyword) {
			return CategoryNetwork
		}
	}

	// Database
	for _, keyword := range []string{"rds", "dbinstance", "dbcluster", "dbsubnetgroup", "dbparametergroup", "postgresql", "flexibleserver"} {
		if strings.Contains(lower, keyword) {
			return CategoryDatabase
		}
	}

	// Storage
	for _, keyword := range []string{"s3/bucket", "s3:bucket", "fsx", "storageaccount", "blobcontainer", "netapp"} {
		if strings.Contains(lower, keyword) {
			return CategoryStorage
		}
	}

	// DNS
	for _, keyword := range []string{"route53", "hostedzone", "dnszone", "dnsrecord"} {
		if strings.Contains(lower, keyword) {
			return CategoryDNS
		}
	}

	// IAM
	for _, keyword := range []string{"iam/role", "iam:role", "iam/policy", "iam:policy", "iam/openidconnect", "managedidentity", "roleassignment", "federatedidentity"} {
		if strings.Contains(lower, keyword) {
			return CategoryIAM
		}
	}

	return CategoryOther
}

const (
	AWSKMSKeyAlias    = "alias/posit-team-dedicated"
	AzureKeyVaultName = "posit-team-dedicated"
)

// StateBackendURL returns the Pulumi state backend URL for a given cloud and target.
func StateBackendURL(cloud, targetName string) string {
	if cloud == "azure" {
		return fmt.Sprintf("azblob://<container>?storage_account=%s", targetName)
	}
	return fmt.Sprintf("s3://ptd-%s", targetName)
}

// ResourcesByCategory groups resource inventory entries by their document category.
func ResourcesByCategory(resources []ResourceInventoryEntry) map[string][]ResourceInventoryEntry {
	result := make(map[string][]ResourceInventoryEntry)
	for _, r := range resources {
		cat := CategorizeResource(r.Type)
		result[cat] = append(result[cat], r)
	}
	return result
}
