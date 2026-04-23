package steps

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/posit-dev/ptd/lib/types"
	yaml "gopkg.in/yaml.v3"
)

// alloyConfigParams holds all parameters needed to build the Alloy River config.
type alloyConfigParams struct {
	compoundName               string
	trueName                   string // for CloudWatch search tags
	domain                     string // from first/main site
	controlRoomDomain          string
	thirdPartyTelemetryEnabled bool
	release                    string
	region                     string
	clusterName                string // EKS cluster name (aws) or AKS cluster name (azure)
	accountIDOrTenantID        string // AWS account ID or Azure tenant ID
	cloudProvider              string // "aws" or "azure"
	shouldScrapeSystemLogs     bool
	sites                      map[string]types.SiteConfig
	workloadDir                string // for reading site YAML files
	// Azure-specific (empty for AWS)
	subscriptionID           string
	resourceGroupName        string
	clusterResourceGroupName string
	publicSubnetCidr         string
	// tenant_name override (falls back to compoundName)
	tenantName string
}

// ptdComponentForAlloy defines a PTD product component for blackbox health-check targets.
type ptdComponentForAlloy struct {
	name            string
	moduleName      string
	healthCheckPath string
}

var alloyComponents = []ptdComponentForAlloy{
	{"workbench", "http_2xx_workbench", "health-check"},
	{"connect", "http_2xx_connect", "__ping__"},
	{"packageManager", "http_2xx", "__ping__"},
}

// buildAlloyConfig generates the Alloy River configuration string.
// Ported from python-pulumi/src/ptd/pulumi_resources/grafana_alloy.py AlloyConfig._define_config_map.
func buildAlloyConfig(params alloyConfigParams) string {
	hasControlRoom := params.controlRoomDomain != ""
	controlRoomURL := ""
	if hasControlRoom {
		controlRoomURL = fmt.Sprintf("https://mimir.%s/api/v1/push", params.controlRoomDomain)
	}
	workloadURL := "http://mimir-gateway.mimir.svc.cluster.local/api/v1/push"
	lokiURL := "http://loki-gateway.loki.svc.cluster.local/loki/api/v1/push"

	tenantName := params.tenantName
	if tenantName == "" {
		tenantName = params.compoundName
	}

	cloudwatchConfig := ""
	if params.cloudProvider == "aws" {
		cloudwatchConfig = buildCloudWatchConfig(params)
	}

	azureMonitorConfig := ""
	if params.cloudProvider == "azure" {
		azureMonitorConfig = buildAzureMonitorConfig(params)
	}

	systemLogsConfig := ""
	if params.shouldScrapeSystemLogs {
		systemLogsConfig = `
			loki.relabel "journal" {
				forward_to = []

				rule {
					source_labels = ["__journal__systemd_unit"]
					target_label  = "unit"
				}
			}

			loki.source.journal "journal" {
				forward_to = [loki.write.local.receiver]
				relabel_rules = loki.relabel.journal.rules
				path = "/var/log/journal"
			}
		`
	}

	blackboxTargets := buildBlackboxTargets(params)
	blackboxConfig := buildBlackboxConfig()

	// Conditional control room remote_write — omitted when no control_room_domain is set.
	controlRoomForwardTo := ""
	controlRoomBlock := ""
	if hasControlRoom {
		controlRoomForwardTo = "prometheus.remote_write.control_room.receiver,"
		controlRoomBlock = fmt.Sprintf(`prometheus.remote_write "control_room" {
    external_labels = {
        tenant_name = "%s",
    }
    endpoint {
        url = "%s"
        basic_auth {
            username = "%s"
            password_file = "/etc/mimir/password"
        }
        headers = {
            "X-Scope-OrgID" = "%s",
        }
        queue_config {
            sample_age_limit = "5m"
            max_shards       = 3
            max_backoff      = "5m"
        }
    }
}
`, tenantName, controlRoomURL, params.compoundName, params.accountIDOrTenantID)
	}

	config := fmt.Sprintf(`
logging {
  level = "info"
  format = "logfmt"
}
// METRICS SCRAPING
discovery.kubernetes "pod_metrics" {
    role = "pod"

    namespaces {
        names = ["posit-team", "posit-team-system", "loki"]
    }
}

discovery.relabel "pod_metrics" {
    targets = discovery.kubernetes.pod_metrics.targets

    rule {
        action       = "replace"
        source_labels = ["__meta_kubernetes_pod_label_posit_team_site"]
        target_label = "ptd_site"
    }
}

prometheus.exporter.unix "nodes" {
    set_collectors= ["cpu", "cpufreq", "diskstats", "filesystem", "loadavg", "meminfo", "mountstats", "netdev", "netstat", "os", "pressure", "uname", "zfs"]
}

prometheus.exporter.blackbox "front_door" {
    config = "%s"

    %s
}

prometheus.scrape "blackbox" {
    targets    = prometheus.exporter.blackbox.front_door.targets
    forward_to = [prometheus.relabel.blackbox.receiver]
    clustering {
        enabled = true
    }
}

// Normalize instance label for blackbox metrics to deduplicate across Alloy pods.
prometheus.relabel "blackbox" {
    forward_to = [prometheus.relabel.default.receiver]

    rule {
        action       = "replace"
        target_label = "instance"
        replacement  = "blackbox"
    }
}

prometheus.scrape "pods" {
    targets    = discovery.relabel.pod_metrics.output
    forward_to = [prometheus.relabel.default.receiver]
    clustering {
        enabled = true
    }
}

prometheus.scrape "kube_state_metrics" {
    targets = [{__address__ = "kube-state-metrics.kube-system.svc:8080"}]
    forward_to = [prometheus.relabel.default.receiver]
    clustering {
        enabled = true
    }
}

// Scrape cAdvisor metrics from kubelet for container resource usage
discovery.kubernetes "nodes" {
    role = "node"
}

discovery.relabel "kubelet" {
    targets = discovery.kubernetes.nodes.targets

    rule {
        target_label = "__address__"
        replacement  = "kubernetes.default.svc:443"
    }

    rule {
        source_labels = ["__meta_kubernetes_node_name"]
        regex         = "(.+)"
        replacement   = "/api/v1/nodes/${1}/proxy/metrics/cadvisor"
        target_label  = "__metrics_path__"
    }
}

prometheus.scrape "kubelet_cadvisor" {
    targets      = discovery.relabel.kubelet.output
    scheme       = "https"
    bearer_token_file = "/var/run/secrets/kubernetes.io/serviceaccount/token"
    tls_config {
        ca_file              = "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"
        insecure_skip_verify = false
    }
    forward_to = [prometheus.relabel.default.receiver]
    clustering {
        enabled = true
    }
}

prometheus.scrape "nodes" {
    targets    = prometheus.exporter.unix.nodes.targets
    forward_to = [prometheus.relabel.default.receiver]
}

%s

%s

prometheus.relabel "default" {
    forward_to = [
        %s
        prometheus.remote_write.workload.receiver,
    ]

    rule {
        action       = "replace"
        target_label = "cluster"
        replacement  = "%s"
    }
}

%s
prometheus.remote_write "workload" {
    external_labels = {
        tenant_name = "%s",
    }
    endpoint {
        url = "%s"
        queue_config {
            sample_age_limit = "5m"
            max_shards       = 3
            max_backoff      = "5m"
        }
    }
}

// LOG SCRAPING

faro.receiver "frontend" {
  server {
    listen_address = "0.0.0.0"
    cors_allowed_origins = [
      "https://%s",
    ]
  }
  extra_log_labels = {
    app = "home",
  }
  output {
    logs = [loki.write.local.receiver]
  }
}

discovery.kubernetes "pod_logs" {
    role = "pod"
    namespaces {
        own_namespace = false
        names = ["posit-team", "posit-team-system"]
    }
}

// Karpenter logs from kube-system namespace
discovery.kubernetes "karpenter_logs" {
    role = "pod"
    namespaces {
        own_namespace = false
        names = ["kube-system"]
    }
    selectors {
        role = "pod"
        label = "app.kubernetes.io/name=karpenter"
    }
}

discovery.relabel "pod_logs" {

    // labels from https://grafana.com/docs/alloy/latest/flow/reference/components/discovery.kubernetes/
    targets = discovery.kubernetes.pod_logs.targets
    rule {
        source_labels = ["__meta_kubernetes_pod_label_app_kubernetes_io_instance"]
        target_label = "app"
    }

    rule {
        source_labels = ["__meta_kubernetes_pod_container_name"]
        target_label = "container"
    }

    rule {
        source_labels = ["__meta_kubernetes_pod_container_id"]
        target_label = "container_id"
    }

    rule {
        source_labels = ["__meta_kubernetes_pod_host_ip"]
        target_label = "host_ip"
    }

    rule {
        source_labels = ["__meta_kubernetes_pod_node_name"]
        target_label = "host_name"
    }

    rule {
        source_labels = ["__meta_kubernetes_pod_uid"]
        target_label = "pod_uid"
    }

    rule {
        source_labels = ["__meta_kubernetes_pod_name"]
        target_label = "pod_name"
    }

    rule {
        source_labels = ["__meta_kubernetes_pod_ip"]
        target_label = "pod_ip"
    }

    rule {
        source_labels = ["__meta_kubernetes_namespace"]
        target_label = "namespace"
    }
    rule {
        action       = "replace"
        target_label = "cluster"
        replacement  = "%s"
    }
}

loki.source.kubernetes "pods" {
    targets    = discovery.relabel.pod_logs.output
    forward_to = [loki.write.local.receiver]
}

// Karpenter log labels
discovery.relabel "karpenter_logs" {
    targets = discovery.kubernetes.karpenter_logs.targets

    rule {
        source_labels = ["__meta_kubernetes_pod_label_app_kubernetes_io_name"]
        target_label = "app"
    }

    rule {
        source_labels = ["__meta_kubernetes_pod_container_name"]
        target_label = "container"
    }

    rule {
        source_labels = ["__meta_kubernetes_pod_name"]
        target_label = "pod_name"
    }

    rule {
        source_labels = ["__meta_kubernetes_pod_node_name"]
        target_label = "host_name"
    }

    rule {
        source_labels = ["__meta_kubernetes_namespace"]
        target_label = "namespace"
    }

    rule {
        action       = "replace"
        target_label = "cluster"
        replacement  = "%s"
    }
}

loki.source.kubernetes "karpenter" {
    targets    = discovery.relabel.karpenter_logs.output
    forward_to = [loki.write.local.receiver]
}

%s

loki.write "local" {
  endpoint {
    url = "%s"

    batch_size = "1MiB"
    batch_wait = "10s"

    max_backoff_retries = 5
    min_backoff_period = "500ms"
    max_backoff_period = "5m"
    retry_on_http_429 = true

    remote_timeout = "30s"
  }

  external_labels = {
    data = "true",
    tenant_name = "%s",
  }
}
`,
		// blackbox config and targets
		blackboxConfig,
		blackboxTargets,
		// cloud-specific scrapers
		cloudwatchConfig,
		azureMonitorConfig,
		// prometheus.relabel "default": conditional control_room receiver + cluster label
		controlRoomForwardTo,
		params.clusterName,
		// conditional prometheus.remote_write "control_room" block (empty when no control room)
		controlRoomBlock,
		// prometheus.remote_write "workload"
		tenantName,
		workloadURL,
		// faro domain
		params.domain,
		// pod_logs cluster label
		params.clusterName,
		// karpenter_logs cluster label
		params.clusterName,
		// system logs config
		systemLogsConfig,
		// loki.write
		lokiURL,
		tenantName,
	)

	return config
}

// buildBlackboxConfig returns the inline blackbox module config string (single-line).
func buildBlackboxConfig() string {
	cfg := `{ modules: { http_2xx_workbench: { prober: http, timeout: 10s, http: { follow_redirects: false, headers: { X-PTD-Health: probe, } } }, http_2xx_connect: { prober: http, timeout: 10s, http: { follow_redirects: false, headers: { X-Auth-Token: probe, } } }, http_2xx: { prober: http, timeout: 10s, http: { follow_redirects: false, } }, } }`
	return cfg
}

// siteYAMLDict parses a site YAML file from the workload directory.
type siteYAMLDict map[string]interface{}

func loadSiteYAML(workloadDir, siteName string) siteYAMLDict {
	p := filepath.Join(workloadDir, fmt.Sprintf("site_%s", siteName), "site.yaml")
	data, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	var d map[string]interface{}
	if err := yaml.Unmarshal(data, &d); err != nil {
		return nil
	}
	return d
}

func getSiteComponentField(d siteYAMLDict, component, field string, defaultVal interface{}) interface{} {
	if d == nil {
		return defaultVal
	}
	spec, ok := d["spec"].(map[string]interface{})
	if !ok {
		return defaultVal
	}
	comp, ok := spec[component].(map[string]interface{})
	if !ok {
		return defaultVal
	}
	if v, ok := comp[field]; ok {
		return v
	}
	return defaultVal
}

func isFQDNHealthCheckEnabled(d siteYAMLDict) bool {
	if d == nil {
		return true
	}
	spec, ok := d["spec"].(map[string]interface{})
	if !ok {
		return true
	}
	if v, ok := spec["enableFqdnHealthChecks"]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return true
}

// buildBlackboxTargets generates the River target blocks for blackbox health checks.
func buildBlackboxTargets(params alloyConfigParams) string {
	tenantName := params.tenantName
	if tenantName == "" {
		tenantName = params.compoundName
	}

	var sb strings.Builder
	siteNames := make([]string, 0, len(params.sites))
	for k := range params.sites {
		siteNames = append(siteNames, k)
	}
	sort.Strings(siteNames)

	for _, siteName := range siteNames {
		siteConfig := params.sites[siteName]
		d := loadSiteYAML(params.workloadDir, siteName)
		fqdnEnabled := isFQDNHealthCheckEnabled(d)

		for _, comp := range alloyComponents {
			replicas := 1
			if r := getSiteComponentField(d, comp.name, "replicas", 1); r != nil {
				if ri, ok := r.(int); ok {
					replicas = ri
				}
			}
			if replicas == 0 {
				continue
			}

			lowerName := strings.ToLower(comp.name)
			internalAddress := fmt.Sprintf(`"http://%s-%s.posit-team.svc.cluster.local/%s"`, siteName, lowerName, comp.healthCheckPath)

			sb.WriteString(fmt.Sprintf(`
target {
  name = "%s-%s"
  address = %s
  module = "%s"
  labels = {
    "tenant_name" = "%s",
    "ptd_site" = "%s",
    "ptd_component" = "%s",
    "check_type" = "internal",
    "health_check_url" = %s,
  }
}
`, siteName, lowerName, internalAddress, comp.moduleName, tenantName, siteName, lowerName, internalAddress))

			if fqdnEnabled {
				domainPrefix := lowerName
				if dp := getSiteComponentField(d, comp.name, "domainPrefix", lowerName); dp != nil {
					if dps, ok := dp.(string); ok {
						domainPrefix = dps
					}
				}
				domain := siteConfig.Spec.Domain
				fqdnAddress := fmt.Sprintf(`"https://%s.%s/%s"`, domainPrefix, domain, comp.healthCheckPath)

				sb.WriteString(fmt.Sprintf(`
target {
  name = "%s-%s-fqdn"
  address = %s
  module = "%s"
  labels = {
    "tenant_name" = "%s",
    "ptd_site" = "%s",
    "ptd_component" = "%s",
    "check_type" = "fqdn",
    "health_check_url" = %s,
  }
}
`, siteName, lowerName, fqdnAddress, comp.moduleName, tenantName, siteName, lowerName, fqdnAddress))
			}
		}
	}
	return sb.String()
}

// buildCloudWatchConfig generates the CloudWatch exporter River config block (AWS only).
func buildCloudWatchConfig(params alloyConfigParams) string {
	return fmt.Sprintf(`
prometheus.exporter.cloudwatch "cloudwatch" {
    sts_region = "%s"

    discovery {
        type    = "AWS/FSx"
        regions = ["%s"]

        search_tags = {
            Name = "%s",
        }

        metric {
            name       = "StorageCapacity"
            statistics = ["Average"]
            period     = "5m"
        }

        metric {
            name       = "UsedStorageCapacity"
            statistics = ["Average"]
            period     = "5m"
        }
    }

    discovery {
        type    = "AWS/RDS"
        regions = ["%s"]

        search_tags = {
            Name = "%s",
        }

        metric {
            name       = "FreeStorageSpace"
            statistics = ["Average"]
            period     = "5m"
        }

        metric {
            name       = "DatabaseConnections"
            statistics = ["Average", "Sum"]
            period     = "5m"
        }

        metric {
            name       = "ReadLatency"
            statistics = ["Average"]
            period     = "5m"
        }

        metric {
            name       = "CPUUtilization"
            statistics = ["Average"]
            period     = "5m"
        }

        metric {
            name       = "FreeableMemory"
            statistics = ["Average"]
            period     = "5m"
        }

        metric {
            name       = "WriteLatency"
            statistics = ["Average"]
            period     = "5m"
        }

        metric {
            name       = "Deadlocks"
            statistics = ["Sum"]
            period     = "5m"
        }
    }

    discovery {
        type    = "AWS/EC2"
        regions = ["%s"]

        search_tags = {
            Name = "%s",
        }

        metric {
            name       = "NetworkOut"
            statistics = ["Average"]
            period     = "5m"
        }

        metric {
            name       = "NetworkPacketsOut"
            statistics = ["Average"]
            period     = "5m"
        }
    }

    discovery {
        type    = "AWS/NATGateway"
        regions = ["%s"]

        search_tags = {
            "posit.team/true-name" = "%s",
        }

        metric {
            name       = "ErrorPortAllocation"
            statistics = ["Sum"]
            period     = "5m"
        }

        metric {
            name       = "PacketsDropCount"
            statistics = ["Sum"]
            period     = "5m"
        }
    }

    discovery {
        type    = "AWS/ApplicationELB"
        regions = ["%s"]

        search_tags = {
            "posit.team/true-name" = "%s",
        }

        metric {
            name       = "HTTPCode_Target_5XX_Count"
            statistics = ["Sum"]
            period     = "5m"
        }

        metric {
            name       = "UnHealthyHostCount"
            statistics = ["Average"]
            period     = "5m"
        }

        metric {
            name       = "TargetResponseTime"
            statistics = ["Average"]
            period     = "5m"
        }
    }

    discovery {
        type    = "AWS/NetworkELB"
        regions = ["%s"]

        search_tags = {
            "posit.team/true-name" = "%s",
        }

        metric {
            name       = "UnHealthyHostCount"
            statistics = ["Average"]
            period     = "5m"
        }
    }
}

prometheus.scrape "cloudwatch" {
    targets    = prometheus.exporter.cloudwatch.cloudwatch.targets
    forward_to = [prometheus.relabel.default.receiver]
    clustering {
        enabled = true
    }
}
`,
		params.region,
		params.region, params.compoundName,
		params.region, params.compoundName,
		params.region, params.compoundName,
		params.region, params.trueName,
		params.region, params.trueName,
		params.region, params.trueName,
	)
}

// buildAzureMonitorConfig generates the Azure Monitor exporter River config block (Azure only).
func buildAzureMonitorConfig(params alloyConfigParams) string {
	config := fmt.Sprintf(`
prometheus.exporter.azure "postgres" {
    subscriptions    = ["%s"]
    resource_type    = "Microsoft.DBforPostgreSQL/flexibleServers"
    resource_graph_query_filter = "where resourceGroup == '%s'"
    metrics          = ["cpu_percent", "memory_percent", "storage_percent", "active_connections", "connections_failed", "deadlocks"]
    included_dimensions = ["*"]
}

prometheus.scrape "azure_postgres" {
    targets    = prometheus.exporter.azure.postgres.targets
    forward_to = [prometheus.relabel.default.receiver]
    clustering {
        enabled = true
    }
}

prometheus.exporter.azure "netapp" {
    subscriptions    = ["%s"]
    resource_type    = "Microsoft.NetApp/netAppAccounts/capacityPools/volumes"
    resource_graph_query_filter = "where resourceGroup == '%s'"
    metrics          = ["VolumeConsumedSizePercentage", "VolumeLogicalSize", "AverageReadLatency", "AverageWriteLatency", "ReadIops", "WriteIops"]
}

prometheus.scrape "azure_netapp" {
    targets    = prometheus.exporter.azure.netapp.targets
    forward_to = [prometheus.relabel.default.receiver]
    clustering {
        enabled = true
    }
}

prometheus.exporter.azure "loadbalancer" {
    subscriptions    = ["%s"]
    resource_type    = "Microsoft.Network/loadBalancers"
    resource_graph_query_filter = "where resourceGroup == '%s'"
    metrics          = ["DipAvailability", "VipAvailability", "UsedSnatPorts", "AllocatedSnatPorts", "SnatConnectionCount"]
}

prometheus.scrape "azure_loadbalancer" {
    targets    = prometheus.exporter.azure.loadbalancer.targets
    forward_to = [prometheus.relabel.default.receiver]
    clustering {
        enabled = true
    }
}

prometheus.exporter.azure "storage" {
    subscriptions    = ["%s"]
    resource_type    = "Microsoft.Storage/storageAccounts"
    resource_graph_query_filter = "where resourceGroup == '%s'"
    metrics          = ["Availability", "SuccessE2ELatency", "UsedCapacity", "Transactions"]
}

prometheus.scrape "azure_storage" {
    targets    = prometheus.exporter.azure.storage.targets
    forward_to = [prometheus.relabel.default.receiver]
    clustering {
        enabled = true
    }
}
`,
		params.subscriptionID, params.resourceGroupName,
		params.subscriptionID, params.resourceGroupName,
		params.subscriptionID, params.clusterResourceGroupName,
		params.subscriptionID, params.resourceGroupName,
	)

	if params.publicSubnetCidr != "" {
		config += fmt.Sprintf(`
prometheus.exporter.azure "natgateway" {
    subscriptions    = ["%s"]
    resource_type    = "Microsoft.Network/natGateways"
    resource_graph_query_filter = "where resourceGroup == '%s'"
    metrics          = ["PacketCount", "ByteCount", "DroppedPackets", "TotalConnectionCount", "SNATConnectionCount"]
}

prometheus.scrape "azure_natgateway" {
    targets    = prometheus.exporter.azure.natgateway.targets
    forward_to = [prometheus.relabel.default.receiver]
    clustering {
        enabled = true
    }
}
`, params.subscriptionID, params.resourceGroupName)
	}

	return config
}

// formatLBTags builds the ALB annotation tag string (posit.team/true-name=X,posit.team/environment=Y,Name=Z).
// Values must not contain commas, equals signs, or whitespace.
func formatLBTags(tags map[string]string) string {
	// Sort keys for determinism
	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(tags))
	for _, k := range keys {
		v := tags[k]
		parts = append(parts, k+"="+v)
	}
	return strings.Join(parts, ",")
}

// validateAlloyTrueName validates that a trueName string is safe for Alloy River config interpolation.
var alloyTrueNameRe = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

func validateAlloyTrueName(trueName string) error {
	if !alloyTrueNameRe.MatchString(trueName) {
		return fmt.Errorf("workload true_name contains characters unsafe for Alloy River config: %q. Must match [a-zA-Z0-9._-]+", trueName)
	}
	return nil
}
