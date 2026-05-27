package steps

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// findRepoRoot walks up from the test file's directory until it finds a directory
// containing python-pulumi/ (which is unique to the ptd repo root).
func findRepoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller failed")

	dir := filepath.Dir(thisFile)
	for {
		if _, err := os.Stat(filepath.Join(dir, "python-pulumi")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find repo root (no python-pulumi/ ancestor directory found)")
		}
		dir = parent
	}
}

func TestBuildControlRoomMetricFilter(t *testing.T) {
	repoRoot := findRepoRoot(t)

	result, err := BuildControlRoomMetricFilter(repoRoot)
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
	for _, m := range knownMetrics {
		// Each metric should appear as a full token (not a substring of another metric).
		parts := strings.Split(result, "|")
		found := false
		for _, p := range parts {
			if p == m {
				found = true
				break
			}
		}
		assert.True(t, found, "expected metric %q to be present in filter result", m)
	}

	// Verify that known PromQL function names are NOT present as top-level tokens.
	forbiddenTokens := []string{"rate", "sum", "by", "count", "avg", "last_over_time", "offset"}
	parts := strings.Split(result, "|")
	partSet := make(map[string]bool, len(parts))
	for _, p := range parts {
		partSet[p] = true
	}
	for _, f := range forbiddenTokens {
		assert.False(t, partSet[f], "PromQL keyword/function %q should not appear as a metric name in the filter", f)
	}
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
