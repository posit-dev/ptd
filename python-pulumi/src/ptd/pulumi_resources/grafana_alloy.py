import re
import textwrap
import typing
from dataclasses import dataclass

import pulumi
import pulumi_kubernetes as kubernetes
import yaml

import ptd
import ptd.aws_workload
import ptd.azure_workload


@dataclass(frozen=True)
class PTDComponentForAlloy:
    name: str
    module_name: str
    health_check_path: str


components: list[PTDComponentForAlloy] = [
    PTDComponentForAlloy("workbench", "http_2xx_workbench", "health-check"),
    PTDComponentForAlloy("connect", "http_2xx_connect", "__ping__"),
    PTDComponentForAlloy("packageManager", "http_2xx", "__ping__"),
]

T = typing.TypeVar("T")


def _validate_alloy_true_name(true_name: str) -> None:
    """Validate that true_name is safe for interpolation into Alloy River config.

    Alloy River config uses double-quoted strings; characters like `"`, `{`, `}` would
    break the generated config or allow injection. This validation is enforced at
    graph-construction time so failures are caught during `pulumi preview`.
    """
    if not re.match(r"^[a-zA-Z0-9._-]+$", true_name):
        raise ValueError(
            f"workload true_name contains characters unsafe for Alloy River config: "
            f"{true_name!r}. Must match [a-zA-Z0-9._-]+"
        )


class AlloyConfig(pulumi.ComponentResource):
    namespace: str
    config_map: kubernetes.core.v1.ConfigMap
    workload: ptd.aws_workload.AWSWorkload | ptd.azure_workload.AzureWorkload
    release: str
    region: str
    should_scrape_system_logs: bool
    journal_path: str
    log_level: str

    def __init__(
        self,
        name: str,
        workload: ptd.aws_workload.AWSWorkload | ptd.azure_workload.AzureWorkload,
        release: str,
        region: str,
        namespace: str,
        provider: pulumi.ProviderResource,
        *,  # Force keyword arguments after this point
        should_scrape_system_logs: bool = False,
        journal_path: str = "/var/log/journal",
        log_level: str = "info",  # Changed from debug to info for production
        opts: pulumi.ResourceOptions = None,
    ):
        super().__init__(
            f"ptd:{self.__class__.__name__}",
            name,
            None,
            opts,
        )

        self.namespace = namespace
        self.workload = workload
        self.release = release
        self.region = region
        self.provider = provider
        # Use the workload's cloud_provider property
        self.cloud_provider = self.workload.cloud_provider.name.lower()

        self.should_scrape_system_logs = should_scrape_system_logs
        self.journal_path = journal_path
        self.log_level = log_level

        self._define_config_map(
            name=name,
            namespace=namespace,
        )

        self.register_outputs({})

    def _load_site_yaml_dict(self, site_name: str) -> dict[str, typing.Any] | None:
        """Load and parse site YAML file. Returns None if file doesn't exist or parsing fails."""
        site_yaml_path = self.workload.site_yaml(site_name)
        if not site_yaml_path.exists():
            return None

        try:
            return yaml.safe_load(site_yaml_path.read_text())
        except yaml.YAMLError as e:
            pulumi.log.warn(f"Failed to parse site YAML for '{site_name}': {e}. Using default configuration values.")
            return None
        except Exception as e:
            pulumi.log.warn(
                f"Unexpected error reading site YAML for '{site_name}': {e}. Using default configuration values."
            )
            return None

    def _get_component_field(
        self, site_dict: dict[str, typing.Any] | None, component: str, field_name: str, default: T
    ) -> T:
        """Extract a field from component spec in parsed site YAML dict, fallback to default."""
        if site_dict is not None:
            component_spec = site_dict.get("spec", {}).get(component, {})
            return component_spec.get(field_name, default)
        return default

    def _is_fqdn_health_check_enabled(self, site_dict: dict[str, typing.Any] | None) -> bool:
        """Check if FQDN health checks are enabled. Defaults to True."""
        if site_dict is not None:
            return site_dict.get("spec", {}).get("enableFqdnHealthChecks", True)
        return True

    def _define_blackbox_targets(self) -> str:
        output = ""

        for site_name, site_config in self.workload.cfg.sites.items():
            # Parse site YAML once for this site
            site_dict = self._load_site_yaml_dict(site_name)
            fqdn_enabled = self._is_fqdn_health_check_enabled(site_dict)

            for component in components:
                # Skip health check if component has 0 replicas
                replicas = self._get_component_field(site_dict, component.name, "replicas", 1)
                if replicas == 0:
                    continue

                # Setup internal cluster service name check
                lower_name = component.name.lower()
                internal_address = (
                    f'"http://{site_name}-{lower_name}.posit-team.svc.cluster.local/{component.health_check_path}"'
                )
                output += textwrap.dedent(f"""
                target {{
                  name = "{site_name}-{lower_name}"
                  address = {internal_address}
                  module = "{component.module_name}"
                  labels = {{
                    "ptd_site" = "{site_name}",
                    "ptd_component" = "{lower_name}",
                    "check_type" = "internal",
                    "health_check_url" = {internal_address},
                  }}
                }}
                """)

                # Setup FQDN service check if enabled
                if fqdn_enabled:
                    domain_prefix = self._get_component_field(site_dict, component.name, "domainPrefix", lower_name)
                    fqdn_address = f'"https://{domain_prefix}.{site_config.domain}/{component.health_check_path}"'
                    output += textwrap.dedent(f"""
                    target {{
                      name = "{site_name}-{lower_name}-fqdn"
                      address = {fqdn_address}
                      module = "{component.module_name}"
                      labels = {{
                        "ptd_site" = "{site_name}",
                        "ptd_component" = "{lower_name}",
                        "check_type" = "fqdn",
                        "health_check_url" = {fqdn_address},
                      }}
                    }}
                    """)

        return textwrap.dedent(output).strip()

    @staticmethod
    def _define_blackbox_config() -> str:
        cfg = """
        {
          modules: {
            http_2xx_workbench: {
              prober: http,
              timeout: 10s,
              http: {
                follow_redirects: false,
                headers: {
                  X-PTD-Health: probe,
                }
              }
            },
            http_2xx_connect: {
              prober: http,
              timeout: 10s,
              http: {
                follow_redirects: false,
                headers: {
                  X-Auth-Token: probe,
                }
              }
            },
            http_2xx: {
              prober: http,
              timeout: 10s,
              http: {
                follow_redirects: false,
              }
            },
        }
        }
        """
        cfg = cfg.replace("\n", " ")
        cfg = cfg.replace("\t", " ")

        many_spaces = re.compile(r"\s+")
        return many_spaces.sub(" ", cfg).strip()

    def _define_cloudwatch_config(self) -> str:
        """Generate CloudWatch exporter configuration for AWS. Returns empty string for non-AWS."""
        if self.cloud_provider != "aws":
            return ""
        _validate_alloy_true_name(self.workload.cfg.true_name)
        _validate_alloy_true_name(self.workload.compound_name)
        return textwrap.dedent(f"""
            prometheus.exporter.cloudwatch "cloudwatch" {{
                sts_region = "{self.region}"

                discovery {{
                    type    = "AWS/FSx"
                    regions = ["{self.region}"]

                    search_tags = {{
                        Name = "{self.workload.compound_name}",
                    }}

                    metric {{
                        name       = "StorageCapacity"
                        statistics = ["Average"]
                        period     = "5m"
                    }}

                    metric {{
                        name       = "UsedStorageCapacity"
                        statistics = ["Average"]
                        period     = "5m"
                    }}
                }}

                discovery {{
                    type    = "AWS/RDS"
                    regions = ["{self.region}"]

                    search_tags = {{
                        Name = "{self.workload.compound_name}",
                    }}

                    metric {{
                        name       = "FreeStorageSpace"
                        statistics = ["Average"]
                        period     = "5m"
                    }}

                    # TODO: Remove ["Sum"] from statistics once all Grafana dashboards have
                    # been updated to query aws_rds_database_connections_average.
                    # Collecting both Sum and Average during migration. Average is the
                    # target metric (aws_rds_database_connections_average); Sum
                    # (aws_rds_database_connections_sum) is kept temporarily for existing
                    # dashboards. NOTE: Keeping Sum doubles the CloudWatch API cost for this metric.
                    metric {{
                        name       = "DatabaseConnections"
                        statistics = ["Average", "Sum"]
                        period     = "5m"
                    }}

                    metric {{
                        name       = "ReadLatency"
                        statistics = ["Average"]
                        period     = "5m"
                    }}

                    metric {{
                        name       = "CPUUtilization"
                        statistics = ["Average"]
                        period     = "5m"
                    }}

                    metric {{
                        name       = "FreeableMemory"
                        statistics = ["Average"]
                        period     = "5m"
                    }}

                    # Collected for dashboard visibility; no alert rules defined
                    metric {{
                        name       = "WriteLatency"
                        statistics = ["Average"]
                        period     = "5m"
                    }}

                    # Collected for dashboard visibility; no alert rules defined
                    metric {{
                        name       = "Deadlocks"
                        statistics = ["Sum"]
                        period     = "5m"
                    }}
                }}

                discovery {{
                    type    = "AWS/EC2"
                    regions = ["{self.region}"]

                    search_tags = {{
                        Name = "{self.workload.compound_name}",
                    }}

                    metric {{
                        name       = "NetworkOut"
                        statistics = ["Average"]
                        period     = "5m"
                    }}

                    metric {{
                        name       = "NetworkPacketsOut"
                        statistics = ["Average"]
                        period     = "5m"
                    }}
                }}

                discovery {{
                    type    = "AWS/NATGateway"
                    regions = ["{self.region}"]

                    # NAT Gateways inherit VPC tags including posit.team/true-name
                    # (see python-pulumi/src/ptd/pulumi_resources/aws_vpc.py:607-616)
                    search_tags = {{
                        "posit.team/true-name" = "{self.workload.cfg.true_name}",
                    }}

                    metric {{
                        name       = "ErrorPortAllocation"
                        statistics = ["Sum"]
                        period     = "5m"
                    }}

                    metric {{
                        name       = "PacketsDropCount"
                        statistics = ["Sum"]
                        period     = "5m"
                    }}
                }}

                discovery {{
                    type    = "AWS/ApplicationELB"
                    regions = ["{self.region}"]

                    # ALBs are tagged at creation time via aws_workload_helm.py.
                    # LBs provisioned before this tag was added won't be discovered
                    # until the cluster is redeployed.
                    # FIXME: To tag existing ALBs without redeploying, use the AWS CLI:
                    #   aws elbv2 add-tags --resource-arns <ALB_ARN> \
                    #     --tags Key=posit.team/true-name,Value=<true_name>
                    search_tags = {{
                        "posit.team/true-name" = "{self.workload.cfg.true_name}",
                    }}

                    metric {{
                        name       = "HTTPCode_Target_5XX_Count"
                        statistics = ["Sum"]
                        period     = "5m"
                    }}

                    metric {{
                        name       = "UnHealthyHostCount"
                        statistics = ["Average"]
                        period     = "5m"
                    }}

                    metric {{
                        name       = "TargetResponseTime"
                        statistics = ["Average"]
                        period     = "5m"
                    }}
                }}

                discovery {{
                    type    = "AWS/NetworkELB"
                    regions = ["{self.region}"]

                    # NLBs are tagged at creation time via traefik.py.
                    # LBs provisioned before this tag was added won't be discovered
                    # until the cluster is redeployed.
                    # FIXME: To tag existing NLBs without redeploying, use the AWS CLI:
                    #   aws elbv2 add-tags --resource-arns <NLB_ARN> \
                    #     --tags Key=posit.team/true-name,Value=<true_name>
                    search_tags = {{
                        "posit.team/true-name" = "{self.workload.cfg.true_name}",
                    }}

                    metric {{
                        name       = "UnHealthyHostCount"
                        statistics = ["Average"]
                        period     = "5m"
                    }}
                }}
            }}

            prometheus.scrape "cloudwatch" {{
                targets    = prometheus.exporter.cloudwatch.cloudwatch.targets
                forward_to = [prometheus.relabel.default.receiver]
                clustering {{
                    enabled = true
                }}
            }}
        """)

    def _define_config_map(
        self,
        name: str,
        namespace: str,
    ):
        control_room_url = f"https://mimir.{self.workload.cfg.control_room_domain}/api/v1/push"
        workload_url = "http://mimir-gateway.mimir.svc.cluster.local/api/v1/push"
        loki_url = "http://loki-gateway.loki.svc.cluster.local/loki/api/v1/push"

        if isinstance(self.workload, ptd.azure_workload.AzureWorkload):
            account_id = self.workload.cfg.tenant_id
            cluster_name = self.workload.cluster_name(self.release)
        else:
            account_id = self.workload.cfg.account_id
            cluster_name = self.workload.eks_cluster_name(self.release)

        # Generate CloudWatch exporter configuration for AWS
        cloudwatch_config = self._define_cloudwatch_config()

        # Generate system log scraping configuration
        system_logs_config = ""
        if self.should_scrape_system_logs:
            system_logs_config = textwrap.dedent(f"""
                loki.relabel "journal" {{
                    forward_to = []

                    rule {{
                        source_labels = ["__journal__systemd_unit"]
                        target_label  = "unit"
                    }}
                }}

                loki.source.journal "journal" {{
                    forward_to = [loki.write.local.receiver]
                    relabel_rules = loki.relabel.journal.rules
                    path = "{self.journal_path}"
                }}
            """)

        alloy_config = textwrap.dedent(
            f"""
                logging {{
                  level = "{self.log_level}"
                  format = "logfmt"
                }}
                // METRICS SCRAPING
                discovery.kubernetes "pod_metrics" {{
                    role = "pod"

                    namespaces {{
                        names = ["posit-team", "posit-team-system", "loki"]
                    }}
                }}

                discovery.relabel "pod_metrics" {{
                    targets = discovery.kubernetes.pod_metrics.targets

                    rule {{
                        action       = "replace"
                        source_labels = ["__meta_kubernetes_pod_label_posit_team_site"]
                        target_label = "ptd_site"
                    }}
                }}

                prometheus.exporter.unix "nodes" {{
                    set_collectors= ["cpu", "cpufreq", "diskstats", "filesystem", "loadavg", "meminfo", "mountstats", "netdev", "netstat", "os", "pressure", "uname", "zfs"]
                }}

                prometheus.exporter.blackbox "front_door" {{
                    config = "{self._define_blackbox_config()}"

                    {textwrap.indent(self._define_blackbox_targets().strip(), " " * 40).strip()}
                }}

                prometheus.scrape "blackbox" {{
                    targets    = prometheus.exporter.blackbox.front_door.targets
                    forward_to = [prometheus.relabel.default.receiver]
                    clustering {{
                        enabled = true
                    }}
                }}

                prometheus.scrape "pods" {{
                    targets    = discovery.relabel.pod_metrics.output
                    forward_to = [prometheus.relabel.default.receiver]
                    clustering {{
                        enabled = true
                    }}
                }}

                prometheus.scrape "kube_state_metrics" {{
                    targets = [{{__address__ = "kube-state-metrics.kube-system.svc:8080"}}]
                    forward_to = [prometheus.relabel.default.receiver]
                    clustering {{
                        enabled = true
                    }}
                }}

                // Scrape cAdvisor metrics from kubelet for container resource usage
                discovery.kubernetes "nodes" {{
                    role = "node"
                }}

                discovery.relabel "kubelet" {{
                    targets = discovery.kubernetes.nodes.targets

                    rule {{
                        target_label = "__address__"
                        replacement  = "kubernetes.default.svc:443"
                    }}

                    rule {{
                        source_labels = ["__meta_kubernetes_node_name"]
                        regex         = "(.+)"
                        replacement   = "/api/v1/nodes/${{1}}/proxy/metrics/cadvisor"
                        target_label  = "__metrics_path__"
                    }}
                }}

                prometheus.scrape "kubelet_cadvisor" {{
                    targets      = discovery.relabel.kubelet.output
                    scheme       = "https"
                    bearer_token_file = "/var/run/secrets/kubernetes.io/serviceaccount/token"
                    tls_config {{
                        ca_file              = "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"
                        insecure_skip_verify = false
                    }}
                    forward_to = [prometheus.relabel.default.receiver]
                    clustering {{
                        enabled = true
                    }}
                }}

                prometheus.scrape "nodes" {{
                    targets    = prometheus.exporter.unix.nodes.targets
                    forward_to = [prometheus.relabel.default.receiver]
                }}

                {cloudwatch_config}

                prometheus.relabel "default" {{
                    forward_to = [
                        prometheus.remote_write.control_room.receiver,
                        prometheus.remote_write.workload.receiver,
                    ]

                    rule {{
                        action       = "replace"
                        target_label = "cluster"
                        replacement  = "{cluster_name}"
                    }}
                }}

                prometheus.remote_write "control_room" {{
                    endpoint {{
                        url = "{control_room_url}"
                        basic_auth {{
                            username = "{self.workload.compound_name}"
                            password_file = "/etc/mimir/password"
                        }}
                        headers = {{
                            "X-Scope-OrgID" = "{account_id}",
                        }}
                    }}
                }}

                prometheus.remote_write "workload" {{
                    endpoint {{
                        url = "{workload_url}"
                    }}
                }}

                // LOG SCRAPING

                faro.receiver "frontend" {{
                  server {{
                    listen_address = "0.0.0.0"
                    cors_allowed_origins = [
                      "https://{self.workload.cfg.domain}",
                    ]
                  }}
                  extra_log_labels = {{
                    app = "home",
                  }}
                  output {{
                    logs = [loki.write.local.receiver]
                //    traces = [otelcol.exporter.otlp.tempo_local.input]
                  }}
                }}

                discovery.kubernetes "pod_logs" {{
                    role = "pod"
                    // Only discover pods from posit-team and posit-team-system namespaces
                    // This significantly reduces log volume and S3 costs
                    namespaces {{
                        own_namespace = false
                        names = ["posit-team", "posit-team-system"]
                    }}
                }}

                // Karpenter logs from kube-system namespace
                discovery.kubernetes "karpenter_logs" {{
                    role = "pod"
                    namespaces {{
                        own_namespace = false
                        names = ["kube-system"]
                    }}
                    selectors {{
                        role = "pod"
                        label = "app.kubernetes.io/name=karpenter"
                    }}
                }}

                discovery.relabel "pod_logs" {{

                    // labels from https://grafana.com/docs/alloy/latest/flow/reference/components/discovery.kubernetes/
                    targets = discovery.kubernetes.pod_logs.targets
                    rule {{
                        source_labels = ["__meta_kubernetes_pod_label_app_kubernetes_io_instance"]
                        target_label = "app"
                    }}

                    rule {{
                        source_labels = ["__meta_kubernetes_pod_container_name"]
                        target_label = "container"
                    }}

                    rule {{
                        source_labels = ["__meta_kubernetes_pod_container_id"]
                        target_label = "container_id"
                    }}

                    rule {{
                        source_labels = ["__meta_kubernetes_pod_host_ip"]
                        target_label = "host_ip"
                    }}

                    rule {{
                        source_labels = ["__meta_kubernetes_pod_node_name"]
                        target_label = "host_name"
                    }}

                    rule {{
                        source_labels = ["__meta_kubernetes_pod_uid"]
                        target_label = "pod_uid"
                    }}

                    rule {{
                        source_labels = ["__meta_kubernetes_pod_name"]
                        target_label = "pod_name"
                    }}

                    rule {{
                        source_labels = ["__meta_kubernetes_pod_ip"]
                        target_label = "pod_ip"
                    }}

                    rule {{
                        source_labels = ["__meta_kubernetes_namespace"]
                        target_label = "namespace"
                    }}
                    rule {{
                        action       = "replace"
                        target_label = "cluster"
                        replacement  = "{cluster_name}"
                    }}
                }}

                loki.source.kubernetes "pods" {{
                    targets    = discovery.relabel.pod_logs.output
                    forward_to = [loki.write.local.receiver]
                }}

                // Karpenter log labels
                discovery.relabel "karpenter_logs" {{
                    targets = discovery.kubernetes.karpenter_logs.targets

                    rule {{
                        source_labels = ["__meta_kubernetes_pod_label_app_kubernetes_io_name"]
                        target_label = "app"
                    }}

                    rule {{
                        source_labels = ["__meta_kubernetes_pod_container_name"]
                        target_label = "container"
                    }}

                    rule {{
                        source_labels = ["__meta_kubernetes_pod_name"]
                        target_label = "pod_name"
                    }}

                    rule {{
                        source_labels = ["__meta_kubernetes_pod_node_name"]
                        target_label = "host_name"
                    }}

                    rule {{
                        source_labels = ["__meta_kubernetes_namespace"]
                        target_label = "namespace"
                    }}

                    rule {{
                        action       = "replace"
                        target_label = "cluster"
                        replacement  = "{cluster_name}"
                    }}
                }}

                loki.source.kubernetes "karpenter" {{
                    targets    = discovery.relabel.karpenter_logs.output
                    forward_to = [loki.write.local.receiver]
                }}

                {system_logs_config}

                loki.write "local" {{
                  endpoint {{
                    url = "{loki_url}"

                    // Batch configuration to optimize writes
                    batch_size = "1MiB"  // 1MiB - optimal batch size
                    batch_wait = "10s"    // Wait up to 10s before sending partial batch

                    // Retry and backoff configuration to prevent runaway retries
                    max_backoff_retries = 5              // Limit retry attempts
                    min_backoff_period = "500ms"        // Minimum backoff time
                    max_backoff_period = "5m"          // Maximum backoff time
                    retry_on_http_429 = true     // Retry on rate limiting

                    // Timeout configuration
                    remote_timeout = "30s"              // Request timeout
                  }}

                  external_labels = {{
                    data = "true",
                  }}
                }}
            """
        )

        # Create the ConfigMap resource
        self.config_map = kubernetes.core.v1.ConfigMap(
            f"{name}-configmap",
            metadata={
                "name": name,
                "namespace": namespace,
            },
            data={"config.alloy": alloy_config},
            opts=pulumi.ResourceOptions(parent=self, provider=self.provider),
        )
