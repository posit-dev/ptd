package steps

import "embed"

// grafanaAssets embeds the Grafana alert rule YAML and dashboard JSON files that
// the cluster and helm steps read at ensure time. They were historically read
// from python-pulumi/src/ptd/grafana_alerts and grafana_dashboards on the OS
// filesystem; embedding them makes the binary self-contained.
//
//go:embed assets/grafana_alerts/*.yaml assets/grafana_dashboards/*.json
var grafanaAssets embed.FS

const (
	grafanaAlertsDir     = "assets/grafana_alerts"
	grafanaDashboardsDir = "assets/grafana_dashboards"
)
