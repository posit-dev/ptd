package steps

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAzureSiteSecretKeyVaultName(t *testing.T) {
	// With external secrets enabled the name carries the compound prefix so the
	// site's ExternalSecret can select it via ^<compound>-<site>-.
	t.Run("external secrets enabled uses compound prefix", func(t *testing.T) {
		assert.Equal(t,
			"myworkload-main-dev-db-password",
			azureSiteSecretKeyVaultName("myworkload", "main", "dev-db-password", true))
	})

	// Unmigrated workloads keep the historical name so their vaults are untouched.
	t.Run("external secrets disabled keeps legacy name", func(t *testing.T) {
		assert.Equal(t,
			"main-dev-db-password",
			azureSiteSecretKeyVaultName("myworkload", "main", "dev-db-password", false))
	})
}
