package steps

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildControlRoomMetricFilter(t *testing.T) {
	result, err := BuildControlRoomMetricFilter()
	require.NoError(t, err)
	require.NotEmpty(t, result, "metric filter should be non-empty")

	// The result should be a pipe-separated list — verify it contains pipes.
	assert.Contains(t, result, "|", "result should contain pipe-separated metric names")

	// Verify a sample of known metric names from the alert files are present.
	knownMetrics := []string{
		"probe_http_status_code",
		"kube_node_status_condition",
		"aws_rds_cpuutilization_average",
		"aws_rds_free_storage_space_average",
		"aws_fsx_storage_capacity_average",
		"aws_applicationelb_httpcode_target_5_xx_count_sum",
		"loki_ingester_wal_disk_full_failures_total",
		"up",
	}
	parts := strings.Split(result, "|")
	partSet := make(map[string]bool, len(parts))
	for _, p := range parts {
		partSet[p] = true
	}
	for _, m := range knownMetrics {
		assert.True(t, partSet[m], "expected metric %q to be present in filter result", m)
	}

	// Verify that known PromQL function names are NOT present as top-level tokens.
	forbiddenTokens := []string{"rate", "sum", "by", "count", "avg", "last_over_time", "offset"}
	for _, f := range forbiddenTokens {
		assert.False(t, partSet[f], "PromQL keyword/function %q should not appear as a metric name in the filter", f)
	}
}

// TestBuildAlloyConfigFilterEnabled verifies that when filterControlRoomMetrics is
// true, the generated Alloy config builds the control_room_filter relabel component
// from the embedded grafana assets and forwards through it to the control room
// remote_write.
func TestBuildAlloyConfigFilterEnabled(t *testing.T) {
	config := buildAlloyConfig(alloyConfigParams{
		compoundName:             "test-workload",
		controlRoomDomain:        "ctrl.example.posit.team",
		accountIDOrTenantID:      "123456789012",
		cloudProvider:            "aws",
		filterControlRoomMetrics: true,
	})

	// The control room remote_write block must be present.
	assert.Contains(t, config, `prometheus.remote_write "control_room"`,
		"control room remote_write block should be present")

	// The filter relabel component must be present — it is built from the embedded assets.
	assert.Contains(t, config, `prometheus.relabel "control_room_filter"`,
		"control_room_filter relabel block should be present when filtering is enabled")

	// Metrics should be routed through the filter component.
	assert.Contains(t, config, "prometheus.relabel.control_room_filter.receiver",
		"forward_to should route through the control_room_filter receiver")
}

func TestExtractMetricNamesFromExpr(t *testing.T) {
	tests := []struct {
		name     string
		expr     string
		wantSome []string
		wantNot  []string
	}{
		{
			name:     "simple metric",
			expr:     "probe_http_status_code",
			wantSome: []string{"probe_http_status_code"},
		},
		{
			name:     "metric with label matchers",
			expr:     `kube_node_status_condition{condition="Ready",status="true"} == 0`,
			wantSome: []string{"kube_node_status_condition"},
			wantNot:  []string{"condition", "Ready", "status", "true"},
		},
		{
			name:     "function call filtered out",
			expr:     `rate(node_cpu_seconds_total{mode="idle"}[5m])`,
			wantSome: []string{"node_cpu_seconds_total"},
			wantNot:  []string{"rate"},
		},
		{
			name:     "count wrapping up",
			expr:     `count(up{job="prometheus.scrape.kube_state_metrics"})`,
			wantSome: []string{"up"},
			wantNot:  []string{"count", "job"},
		},
		{
			name: "binary expression",
			expr: `kube_deployment_spec_replicas{namespace=~"foo"} != kube_deployment_status_replicas_available{namespace=~"foo"}`,
			wantSome: []string{
				"kube_deployment_spec_replicas",
				"kube_deployment_status_replicas_available",
			},
			wantNot: []string{"namespace"},
		},
		{
			name:     "last_over_time wrapping azure metric",
			expr:     `last_over_time(azure_microsoft_netapp_netappaccounts_capacitypools_volumes_volumeconsumedsizepercentage_average_percent{job="integrations/azure"}[5m])`,
			wantSome: []string{"azure_microsoft_netapp_netappaccounts_capacitypools_volumes_volumeconsumedsizepercentage_average_percent"},
			wantNot:  []string{"last_over_time", "job"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractMetricNamesFromExpr(tt.expr)
			gotSet := make(map[string]bool)
			for _, m := range got {
				gotSet[m] = true
			}
			for _, want := range tt.wantSome {
				assert.True(t, gotSet[want], "expected %q in result %v", want, got)
			}
			for _, notWant := range tt.wantNot {
				assert.False(t, gotSet[notWant], "did not expect %q in result %v", notWant, got)
			}
		})
	}
}
