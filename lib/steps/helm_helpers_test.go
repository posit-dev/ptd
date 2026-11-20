package steps

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestBuildAlloyConfigKubernetesEvents verifies that the generated Alloy config ships
// Kubernetes events from all namespaces to the in-cluster Loki with a cluster label,
// for both cloud providers (the generator is shared by the AWS and Azure helm steps).
func TestBuildAlloyConfigKubernetesEvents(t *testing.T) {
	for _, provider := range []string{"aws", "azure"} {
		t.Run(provider, func(t *testing.T) {
			config := buildAlloyConfig(alloyConfigParams{
				compoundName:        "test-workload",
				clusterName:         "test-cluster-name",
				accountIDOrTenantID: "123456789012",
				cloudProvider:       provider,
			})

			assert.NotContains(t, config, "%!", "config should have no fmt verb/argument mismatches")

			assert.Contains(t, config, `loki.source.kubernetes_events "events" {
    forward_to = [loki.relabel.kubernetes_events.receiver]
}`)
			assert.Contains(t, config, `loki.relabel "kubernetes_events" {
    forward_to = [loki.write.local.receiver]

    rule {
        action       = "replace"
        target_label = "cluster"
        replacement  = "test-cluster-name"
    }
}`)

			// Events are collected cluster-wide, so no namespaces restriction is set.
			start := strings.Index(config, `loki.source.kubernetes_events "events"`)
			end := strings.Index(config[start:], "}")
			assert.NotContains(t, config[start:start+end], "namespaces")

			// The loki.write receiver the events forward to still exists.
			assert.Contains(t, config, `loki.write "local" {`)
		})
	}
}
