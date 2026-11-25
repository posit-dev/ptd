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

// traefikCRDAssets embeds the traefik.io CustomResourceDefinitions vendored from
// the Traefik Helm chart's crds/ directory. Helm installs crds/ once and never
// upgrades it, so PTD applies these as first-class Pulumi resources instead.
// Refresh them with `just refresh-traefik-crds <chart-version>` whenever
// traefik_version moves. See docs/infrastructure/traefik-crds.md.
//
//go:embed assets/traefik_crds/*.yaml
var traefikCRDAssets embed.FS

const traefikCRDsDir = "assets/traefik_crds"
