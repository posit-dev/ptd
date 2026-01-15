package consts

const (
	// Component Names

	Chronicle      = "chronicle"
	ChronicleAgent = "chronicle-agent"
	Connect        = "connect"
	Flightdeck     = "flightdeck"
	PackageManager = "package-manager"
	PtdController  = "ptd-controller"
	SiteHome       = "home"
	TeamOperator   = "team-operator"
	Workbench      = "rstudio-pro"

	KmsAlias = "alias/posit-team-dedicated"

	// IAM policy names
	PositTeamDedicatedAdminPolicyName = "PositTeamDedicatedAdmin"

	// DockerHub-related constants
	DockerHubIdentifier            = "posit"
	DockerHubControlRoomSecretName = "dockerhub-ro-oat"
	DockerHubAcrUsernameSecretName = "ptd-dockerhub-username"
	DockerHubAcrOatSecretName      = "ptd-dockerhub-oat"
	DockerHubEcrSecretName         = "ecr-pullthroughcache/ptd-dockerhub"

	// Azure specific constants
	AzKeyName                    = "posit-team-dedicated"
	KeyVaultAdminRoleId          = "00482a5a-887f-4fb3-b363-3b7fe8e74483" // Key Vault Administrator built-in role
	StorageBlobDataContribRoleId = "ba92f5b4-2d11-453d-a403-e96b0029c9fe" // Storage Blob Data Contributor built-in role

	// Tags
	POSIT_TEAM_ENVIRONMENT    = "posit.team/environment"
	POSIT_TEAM_MANAGED_BY_TAG = "posit.team/managed-by"
	POSIT_TEAM_TRUE_NAME      = "posit.team/true-name"
)
