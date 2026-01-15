import json
import typing

import pulumi
import pulumi_azure_native as pulumi_az
import pulumi_kubernetes as kubernetes

import ptd
import ptd.azure_workload
import ptd.pulumi_resources.custom_k8s_resources
import ptd.pulumi_resources.helm_controller
import ptd.pulumi_resources.traefik_forward_auth_azure as tfa
from ptd.azure_roles import (
    ACR_PULL_ROLE_DEFINITION_ID,
    NETWORK_CONTRIBUTOR_ROLE_DEFINITION_ID,
    READER_ROLE_DEFINITION_ID,
)
from ptd.pulumi_resources import (
    azure_files_csi,
    azure_traefik,
    cert_manager,
    team_operator,
    trident_operator,
)


class AzureWorkloadClusters(pulumi.ComponentResource):
    kubeconfigs: dict[str, str]
    kube_providers: dict[str, kubernetes.Provider]

    managed_clusters: dict[str, dict[str, typing.Any]]
    managed_clusters_by_release: dict[str, dict[str, typing.Any]]
    cert_managers: dict[str, cert_manager.CertManager]
    helm_controllers: dict[str, ptd.pulumi_resources.helm_controller.HelmController]
    team_operators: dict[str, team_operator.TeamOperator]
    team_operator_identity: pulumi_az.managedidentity.UserAssignedIdentity
    traefiks: dict[str, azure_traefik.AzureTraefik]
    traefik_forward_auths: dict[str, tfa.TraefikForwardAuthAzure]
    trident_operators: dict[str, trident_operator.TridentOperator]
    azure_files_csi_classes: dict[str, azure_files_csi.AzureFilesCSI]

    @classmethod
    def autoload(cls) -> "AzureWorkloadClusters":
        return cls(workload=ptd.azure_workload.AzureWorkload(pulumi.get_stack()))

    def __init__(self, workload: ptd.azure_workload.AzureWorkload, *args, **kwargs):
        super().__init__(
            f"ptd:{self.__class__.__name__}",
            workload.compound_name,
            *args,
            **kwargs,
        )

        self.workload = workload

        self.required_tags = self.workload.required_tags | {
            ptd.azure_tag_key_format(str(ptd.TagKeys.POSIT_TEAM_MANAGED_BY)): __name__,
        }

        clusters = self.workload.managed_clusters()
        self.managed_clusters = {(cluster["name"]): cluster for cluster in clusters}

        self.managed_clusters_by_release = {
            (cluster["name"].removeprefix(f"{self.workload.compound_name}-")): cluster for cluster in clusters
        }

        self.kubeconfigs = {
            release: json.dumps(self.workload.cluster_kubeconfig(release))
            for release in self.managed_clusters_by_release
        }

        self.kube_providers = {
            release: kubernetes.Provider(
                self.workload.cluster_name(release),
                kubeconfig=self.kubeconfigs[release],
            )
            for release in self.managed_clusters_by_release
        }

        self._define_cluster_role_assignments()
        self._define_kubelet_role_assignments()
        self._define_cert_manager()
        self._apply_custom_k8s_resources()
        self._define_team_operator()
        # self._define_trident_operator()
        self._define_traefik()
        self._define_traefik_forward_auths()
        self._define_bastion_access()
        self._define_coredns()
        self._define_helm_controllers()
        self._define_azure_files_csi()

        self.register_outputs({})

    def _define_team_operator(self):
        self.team_operators = {}
        for release in self.managed_clusters_by_release:
            self.team_operators[release] = team_operator.TeamOperator(
                workload=self.workload,
                release=release,
                opts=pulumi.ResourceOptions(
                    parent=self,
                    providers=[self.kube_providers[release]],
                ),
            )

    def _apply_custom_k8s_resources(self):
        """Apply custom Kubernetes resources from the custom_k8s_resources/ directory."""
        ptd.pulumi_resources.custom_k8s_resources.apply_custom_k8s_resources(
            workload=self.workload,
            managed_clusters_by_release=self.managed_clusters_by_release,
            kube_providers=self.kube_providers,
            parent=self,
        )

    def _define_trident_operator(self):
        self.trident_operators = {}
        for release in self.managed_clusters_by_release:
            self.trident_operators[release] = trident_operator.TridentOperator(
                workload=self.workload,
                release=release,
                opts=pulumi.ResourceOptions(
                    parent=self,
                    providers=[self.kube_providers[release]],
                ),
            )

    def _define_traefik(self):
        self.traefiks = {}
        for release in self.managed_clusters_by_release:
            self.traefiks[release] = azure_traefik.AzureTraefik(
                workload=self.workload,
                release=release,
                domains=self.workload.cfg.domains,
                opts=pulumi.ResourceOptions(
                    parent=self,
                    providers=[self.kube_providers[release]],
                ),
            )

    def _define_traefik_forward_auths(self):
        self.traefik_forward_auths = {}
        for release in self.managed_clusters_by_release:
            comps = self.workload.cfg.clusters[release].components
            if comps is not None and comps.traefik_forward_auth_version is not None:
                self.traefik_forward_auths[release] = tfa.TraefikForwardAuthAzure(
                    workload=self.workload,
                    release=release,
                    chart_version=comps.traefik_forward_auth_version,
                    opts=pulumi.ResourceOptions(parent=self, providers=[self.kube_providers[release]]),
                )

    def _define_cert_manager(self):
        self.cert_managers = {}
        domains = [self.workload.cfg.root_domain] if self.workload.cfg.root_domain else self.workload.cfg.domains

        for release in self.managed_clusters_by_release:
            if self.workload.cfg.clusters[release].use_lets_encrypt:
                self.cert_managers[release] = cert_manager.CertManager(
                    workload=self.workload,
                    release=release,
                    domains=domains,
                    opts=pulumi.ResourceOptions(
                        parent=self,
                        providers=[self.kube_providers[release]],
                    ),
                )

    def _define_cluster_role_assignments(self):
        for release, cluster in self.managed_clusters_by_release.items():
            principal_id = cluster.get("identity", {}).get("principalId")
            resource_group = cluster.get("resourceGroup")
            subscription_id = self.workload.cfg.subscription_id
            if principal_id and resource_group and subscription_id:
                scope = f"/subscriptions/{subscription_id}/resourceGroups/{resource_group}"
                role_definition_string = (
                    f"/subscriptions/{subscription_id}/providers/Microsoft.Authorization/roleDefinitions/"
                )
                pulumi_az.authorization.RoleAssignment(
                    f"{release}-aks-reader",
                    principal_id=principal_id,
                    scope=scope,
                    principal_type="ServicePrincipal",
                    role_definition_id=f"{role_definition_string}{READER_ROLE_DEFINITION_ID}",
                    opts=pulumi.ResourceOptions(parent=self),
                )
                pulumi_az.authorization.RoleAssignment(
                    f"{release}-aks-network-contributor",
                    principal_id=principal_id,
                    scope=scope,
                    principal_type="ServicePrincipal",
                    role_definition_id=f"{role_definition_string}{NETWORK_CONTRIBUTOR_ROLE_DEFINITION_ID}",
                    opts=pulumi.ResourceOptions(parent=self),
                )

    def _define_kubelet_role_assignments(self):
        for release, cluster in self.managed_clusters_by_release.items():
            principal_id = cluster.get("identityProfile", {}).get("kubeletidentity", {}).get("objectId")
            resource_group = cluster.get("resourceGroup")
            subscription_id = self.workload.cfg.subscription_id
            if principal_id and resource_group and subscription_id:
                scope = f"/subscriptions/{subscription_id}/resourceGroups/{resource_group}"
                role_definition_string = (
                    f"/subscriptions/{subscription_id}/providers/Microsoft.Authorization/roleDefinitions/"
                )
                pulumi_az.authorization.RoleAssignment(
                    f"{release}-acrpull",
                    principal_id=principal_id,
                    scope=scope,
                    principal_type="ServicePrincipal",
                    role_definition_id=f"{role_definition_string}{ACR_PULL_ROLE_DEFINITION_ID}",
                    opts=pulumi.ResourceOptions(parent=self),
                )

    def _define_bastion_access(self):
        """
        Creates Network Security Groups to allow communication between Bastion Host and AKS clusters.
        Allows ingress from Bastion to AKS nodes and egress from AKS to Bastion.
        """
        self.bastion_aks_nsgs = {}

        for release, cluster in self.managed_clusters_by_release.items():
            resource_group = cluster.get("resourceGroup")
            location = cluster.get("location", self.workload.cfg.region)

            if not resource_group:
                continue

            # Get the VNet information for this cluster
            vnet_subnet_id = cluster.get("agentPoolProfiles", [{}])[0].get("vnetSubnetId")
            if not vnet_subnet_id:
                continue

            # Parse VNet info from subnet ID
            vnet_parts = vnet_subnet_id.split("/")
            vnet_resource_group = vnet_parts[4]
            vnet_name = vnet_parts[8]
            aks_subnet_name = vnet_parts[10]  # Get the AKS subnet name

            # Get both Bastion and AKS subnet CIDRs
            try:
                bastion_subnet = pulumi_az.network.get_subnet(
                    resource_group_name=vnet_resource_group,
                    virtual_network_name=vnet_name,
                    subnet_name="AzureBastionSubnet",
                )
                bastion_subnet_cidr = bastion_subnet.address_prefix

                # Get the AKS subnet CIDR
                aks_subnet = pulumi_az.network.get_subnet(
                    resource_group_name=vnet_resource_group, virtual_network_name=vnet_name, subnet_name=aks_subnet_name
                )
                aks_subnet_cidr = aks_subnet.address_prefix

            except Exception as e:
                pulumi.log.warn(f"Could not get subnet info for cluster {release}: {e}")
                continue

            # Create NSG for AKS-Bastion communication
            nsg_name = f"{self.workload.cluster_name(release)}-bastion-access"

            self.bastion_aks_nsgs[release] = pulumi_az.network.NetworkSecurityGroup(
                f"{release}-bastion-aks-nsg",
                resource_group_name=resource_group,
                location=location,
                network_security_group_name=nsg_name,
                security_rules=[
                    # Allow all traffic from Bastion subnet to AKS cluster ONLY
                    pulumi_az.network.SecurityRuleArgs(
                        name="AllowBastionToAKS",
                        priority=1000,
                        direction="Inbound",
                        access="Allow",
                        protocol="*",
                        source_port_range="*",
                        destination_port_range="*",
                        source_address_prefix=bastion_subnet_cidr,
                        destination_address_prefix=aks_subnet_cidr,  # Specific AKS subnet only
                        description="Allow all traffic from Bastion to AKS cluster subnet only",
                    ),
                    # Allow all traffic from AKS cluster to Bastion subnet ONLY
                    pulumi_az.network.SecurityRuleArgs(
                        name="AllowAKSToBastion",
                        priority=1010,
                        direction="Outbound",
                        access="Allow",
                        protocol="*",
                        source_port_range="*",
                        destination_port_range="*",
                        source_address_prefix=aks_subnet_cidr,  # Specific AKS subnet only
                        destination_address_prefix=bastion_subnet_cidr,
                        description="Allow all traffic from AKS cluster subnet to Bastion",
                    ),
                ],
                tags=self.required_tags
                | {
                    "Name": nsg_name,
                    "Purpose": "AKS-Bastion-Access",
                    "Release": release,
                    "BastionSubnetCIDR": bastion_subnet_cidr,
                    "AKSSubnetCIDR": aks_subnet_cidr,
                },
                opts=pulumi.ResourceOptions(parent=self),
            )

    def _define_coredns(self):
        """
        Modifies the existing coredns-custom ConfigMap to forward DNS queries to custom DNS servers if specified in dns_forward_domains.
        """
        for release in self.managed_clusters_by_release:
            forward_domains = self.workload.cfg.network.dns_forward_domains

            if not forward_domains:
                continue

            dns_server_blocks = {}
            for domain_obj in forward_domains:
                host = domain_obj["host"]
                ip = domain_obj["ip"]

                dns_server_blocks[f"dns-forward-{host.replace('.', '-')}.server"] = (
                    f"{host}:53 {{\n  errors\n  cache 30\n  forward . {ip}\n}}\n"
                )

            kubernetes.core.v1.ConfigMapPatch(
                f"{release}-coredns-forward",
                metadata={
                    "name": "coredns-custom",
                    "namespace": "kube-system",
                },
                data=dns_server_blocks,
                opts=pulumi.ResourceOptions(
                    parent=self,
                    provider=self.kube_providers[release],
                    # Patch, do not replace or modify existing data
                    replace_on_changes=[],
                ),
            )

    def _define_helm_controllers(self):
        self.helm_controllers = {}
        for release in self.managed_clusters_by_release:
            self.helm_controllers[release] = ptd.pulumi_resources.helm_controller.HelmController(
                workload=self.workload,
                release=release,
                opts=pulumi.ResourceOptions(parent=self, providers=[self.kube_providers[release]]),
            )

    def _define_azure_files_csi(self):
        """Creates StorageClasses for Azure Files CSI driver in each cluster"""
        self.azure_files_csi_classes = {}
        storage_account_name = self.workload.azure_files_storage_account_name

        for release in self.managed_clusters_by_release:
            self.azure_files_csi_classes[release] = azure_files_csi.AzureFilesCSI(
                workload=self.workload,
                release=release,
                storage_account_name=storage_account_name,
                opts=pulumi.ResourceOptions(
                    parent=self,
                    providers=[self.kube_providers[release]],
                ),
            )
