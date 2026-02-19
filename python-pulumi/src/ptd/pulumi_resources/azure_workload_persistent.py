import json

import pulumi
import pulumi_azure_native as pulumi_az
import pulumi_azure_native.dbforpostgresql as pulumi_az_pg
import pulumi_random
from pulumi_azure_native import containerregistry, dns, netapp, network, privatedns, storage

import ptd
import ptd.azure_workload
from ptd.pulumi_resources.azure_bastion import AzureBastion

DB_ADMIN_USERNAME = "ptd_admin"


class AzureWorkloadPersistent(pulumi.ComponentResource):
    workload: ptd.azure_workload.AzureWorkload
    required_tags: dict[str, str]
    vnet: network.VirtualNetwork
    blobs: storage.StorageAccount
    capacity_pool: netapp.CapacityPool
    files_volume: netapp.CapacityPoolVolume
    acr_registry: containerregistry.Registry
    postgres: pulumi_az_pg.Server
    grafana_postgres: pulumi_az_pg.Server
    public_ip: network.PublicIPAddress
    public_subnet: network.Subnet
    private_subnet: network.Subnet
    db_subnet: network.Subnet
    netapp_subnet: network.Subnet
    app_gateway_subnet: network.Subnet
    chronicle_container: storage.BlobContainer
    loki_container: storage.BlobContainer
    ppm_container: storage.BlobContainer
    files_storage_account: storage.StorageAccount
    dns_zones: list[dns.Zone]
    bastion: AzureBastion
    mimir_password: pulumi_random.RandomPassword

    @classmethod
    def autoload(cls) -> "AzureWorkloadPersistent":
        return cls(workload=ptd.azure_workload.AzureWorkload(pulumi.get_stack()))

    def __init__(
        self,
        workload: ptd.azure_workload.AzureWorkload,
        *args,
        **kwargs,
    ):
        super().__init__(
            f"ptd:{self.__class__.__name__}",
            workload.compound_name,
            *args,
            **kwargs,
        )

        outputs = {}
        self.workload = workload
        self.vnet_rsg_name = (
            self.workload.cfg.network.vnet_rsg_name
            if getattr(self.workload.cfg.network, "vnet_rsg_name", None)
            else self.workload.resource_group_name
        )
        self.vnet_name = (
            self.workload.cfg.network.provisioned_vnet_name
            if getattr(self.workload.cfg.network, "provisioned_vnet_name", None)
            else self.workload.vnet_name
        )
        self.required_tags = self.workload.required_tags | {
            ptd.azure_tag_key_format(str(ptd.TagKeys.POSIT_TEAM_MANAGED_BY)): __name__,
        }

        self._define_vnet()
        self._define_file_storage()
        self._define_postgres()
        self._define_container_registry()

        self._define_chronicle_container()
        self._define_loki_container()

        self._define_files_storage_account()

        self._define_dns_zones()

        self._define_bastion()

        self._define_mimir_password()

        outputs = outputs | {
            "db_domain": self.postgres.fully_qualified_domain_name,
            "db_url": self.postgres.fully_qualified_domain_name.apply(
                lambda n: f"postgres://{n}/postgres?sslmode=require"
            ),
            "acr_name": self.acr_registry.name,
            "app_gateway_subnet_id": self.app_gateway_subnet.id,
            "bastion_name": self.bastion.bastion_host.name,
            "bastion_jumpbox_id": self.bastion.jumpbox_host.id,
            "mimir_password": self.mimir_password.result,
            "private_subnet_name": self.private_subnet.name,
            "private_subnet_cidr": self.private_subnet.address_prefix,
            "nat_gw_ip": getattr(self, "public_ip", None) and self.public_ip.ip_address,
            "vnet_name": self.vnet.name if self.vnet else self.workload.cfg.network.provisioned_vnet_name,
            "vnet_cidr": self.vnet.address_space["address_prefixes"][0]
            if self.vnet
            else self.workload.cfg.network.vnet_cidr,
        }

        for key, value in outputs.items():
            pulumi.export(key, value)

        self.register_outputs(outputs)

    def _define_vnet(self):
        self.vnet = None
        # If the customer has provided a vnet name, we use the already provisioned vnet.
        # Either provisioned_vnet_name or vnet_cidr must be set in the network config.
        if self.workload.cfg.network.provisioned_vnet_name:
            vnet_info = network.get_virtual_network(
                resource_group_name=self.vnet_rsg_name,
                virtual_network_name=self.vnet_name,
            )
            self.vnet_id = vnet_info.id
        elif self.workload.cfg.network.vnet_cidr:
            self.vnet = network.VirtualNetwork(
                self.vnet_name,
                virtual_network_name=self.vnet_name,
                resource_group_name=self.vnet_rsg_name,
                address_space=network.AddressSpaceArgs(address_prefixes=[self.workload.cfg.network.vnet_cidr]),
                location=self.workload.cfg.region,
                opts=pulumi.ResourceOptions(
                    protect=self.workload.cfg.protect_persistent_resources,
                ),
                tags=self.required_tags,
            )
            self.vnet_id = self.vnet.id

        if self.workload.cfg.network.public_subnet_cidr:
            self.public_ip = network.PublicIPAddress(
                f"pip-ptd-{self.workload.compound_name}",
                resource_group_name=self.vnet_rsg_name,
                public_ip_allocation_method=network.IPAllocationMethod.STATIC,
                sku=network.PublicIPAddressSkuArgs(name=network.PublicIPAddressSkuName.STANDARD),
            )

            self.nat_gw = network.NatGateway(
                f"ng-ptd-{self.workload.compound_name}",
                resource_group_name=self.vnet_rsg_name,
                sku=network.NatGatewaySkuArgs(name=network.NatGatewaySkuName.STANDARD),
                public_ip_addresses=[network.PublicIPAddressArgs(id=self.public_ip.id)],
            )

            self.public_subnet = network.Subnet(
                f"snet-ptd-{self.workload.compound_name}-public",
                resource_group_name=self.vnet_rsg_name,
                virtual_network_name=self.vnet_name,
                address_prefix=self.workload.cfg.network.public_subnet_cidr,
                opts=pulumi.ResourceOptions(
                    parent=self.vnet if self.vnet else None,
                    protect=self.workload.cfg.protect_persistent_resources,
                ),
            )

        # private network security group and subnet
        # we don't set rules on the security group as the default rules allowing inbound from vnet and load balancer are sufficient
        private_nsg = network.NetworkSecurityGroup(
            resource_name=f"nsg-ptd-{self.workload.compound_name}-private",
            network_security_group_name=f"nsg-ptd-{self.workload.compound_name}-private",
            resource_group_name=self.vnet_rsg_name,
            location=self.workload.cfg.region,
            opts=pulumi.ResourceOptions(
                parent=self,
            ),
        )

        self.private_subnet = network.Subnet(
            f"snet-ptd-{self.workload.compound_name}-private",
            resource_group_name=self.vnet_rsg_name,
            virtual_network_name=self.vnet_name,
            address_prefix=self.workload.cfg.network.private_subnet_cidr,
            **(
                {"nat_gateway": network.SubResourceArgs(id=self.nat_gw.id)}
                if self.workload.cfg.network.public_subnet_cidr
                else {}
            ),
            service_endpoints=[
                network.ServiceEndpointPropertiesFormatArgs(
                    locations=[self.workload.cfg.region],
                    service="Microsoft.SQL",
                ),
                network.ServiceEndpointPropertiesFormatArgs(
                    locations=[self.workload.cfg.region],
                    service="Microsoft.Storage",
                ),
            ],
            network_security_group=network.SubResourceArgs(id=private_nsg.id),
            opts=pulumi.ResourceOptions(
                parent=self.vnet if hasattr(self, "vnet") and self.vnet else None,
                protect=self.workload.cfg.protect_persistent_resources,
            ),
        )

        # DB network security group and subnet
        db_nsg = network.NetworkSecurityGroup(
            resource_name=f"nsg-ptd-{self.workload.compound_name}-db",
            network_security_group_name=f"nsg-ptd-{self.workload.compound_name}-db",
            resource_group_name=self.vnet_rsg_name,
            location=self.workload.cfg.region,
            security_rules=[
                network.SecurityRuleArgs(
                    name="InboundPostgres",
                    priority=1000,
                    direction=network.SecurityRuleDirection.INBOUND,
                    access=network.SecurityRuleAccess.ALLOW,
                    protocol=network.SecurityRuleProtocol.TCP,
                    source_port_range="*",
                    destination_port_range="5432",
                    source_address_prefix="VirtualNetwork",
                    destination_address_prefix="VirtualNetwork",
                ),
                network.SecurityRuleArgs(
                    name="InboundDenyAll",
                    priority=4000,
                    direction=network.SecurityRuleDirection.INBOUND,
                    access=network.SecurityRuleAccess.DENY,
                    protocol=network.SecurityRuleProtocol.ASTERISK,
                    source_port_range="*",
                    destination_port_range="*",
                    source_address_prefix="*",
                    destination_address_prefix="*",
                ),
            ],
            opts=pulumi.ResourceOptions(
                parent=self,
            ),
        )

        self.db_subnet = network.Subnet(
            f"snet-ptd-{self.workload.compound_name}-db",
            resource_group_name=self.vnet_rsg_name,
            virtual_network_name=self.vnet_name,
            address_prefix=self.workload.cfg.network.db_subnet_cidr,
            delegations=[
                network.DelegationArgs(
                    name="postgresql",
                    service_name="Microsoft.DBforPostgreSQL/flexibleServers",
                )
            ],
            network_security_group=network.SubResourceArgs(id=db_nsg.id),
            opts=pulumi.ResourceOptions(
                parent=self.vnet if self.vnet else None,
                protect=self.workload.cfg.protect_persistent_resources,
            ),
        )

        # NetApp network security group and subnet
        netapp_nsg = network.NetworkSecurityGroup(
            resource_name=f"nsg-ptd-{self.workload.compound_name}-netapp",
            network_security_group_name=f"nsg-ptd-{self.workload.compound_name}-netapp",
            resource_group_name=self.vnet_rsg_name,
            location=self.workload.cfg.region,
            security_rules=[
                network.SecurityRuleArgs(
                    name="InboundVnet",
                    priority=1000,
                    direction=network.SecurityRuleDirection.INBOUND,
                    access=network.SecurityRuleAccess.ALLOW,
                    protocol=network.SecurityRuleProtocol.TCP,
                    source_port_range="*",
                    destination_port_range="*",  # restrict to known Netapp ports after other Netapp shenanigans are resolved
                    source_address_prefix="VirtualNetwork",
                    destination_address_prefix="VirtualNetwork",
                ),
                network.SecurityRuleArgs(
                    name="InboundDenyAll",
                    priority=4000,
                    direction=network.SecurityRuleDirection.INBOUND,
                    access=network.SecurityRuleAccess.DENY,
                    protocol=network.SecurityRuleProtocol.ASTERISK,
                    source_port_range="*",
                    destination_port_range="*",
                    source_address_prefix="*",
                    destination_address_prefix="*",
                ),
            ],
            opts=pulumi.ResourceOptions(
                parent=self,
            ),
        )

        self.netapp_subnet = network.Subnet(
            self.workload.netapp_subnet_name,
            subnet_name=self.workload.netapp_subnet_name,
            resource_group_name=self.vnet_rsg_name,
            virtual_network_name=self.vnet_name,
            address_prefix=self.workload.cfg.network.netapp_subnet_cidr,
            delegations=[
                network.DelegationArgs(
                    service_name="Microsoft.NetApp/volumes",
                    name=f"{self.workload.compound_name}-netapp-delegation",
                    type="Microsoft.Network/virtualNetworks/subnets/delegations",
                    actions=[
                        "Microsoft.Network/networkinterfaces/*",
                        "Microsoft.Network/virtualNetworks/subnets/join/action",
                    ],
                )
            ],
            network_security_group=network.SubResourceArgs(id=netapp_nsg.id),
            opts=pulumi.ResourceOptions(
                parent=self.vnet if self.vnet else None,
                protect=self.workload.cfg.protect_persistent_resources,
            ),
        )

        # Application Gateway network security group and subnet
        app_gateway_nsg = network.NetworkSecurityGroup(
            resource_name=f"nsg-ptd-{self.workload.compound_name}-app-gateway",
            network_security_group_name=f"nsg-ptd-{self.workload.compound_name}-app-gateway",
            resource_group_name=self.vnet_rsg_name,
            location=self.workload.cfg.region,
            security_rules=[
                network.SecurityRuleArgs(
                    name="InboundVnet",
                    priority=1000,
                    direction=network.SecurityRuleDirection.INBOUND,
                    access=network.SecurityRuleAccess.ALLOW,
                    protocol=network.SecurityRuleProtocol.TCP,
                    source_port_range="*",
                    destination_port_range="*",
                    source_address_prefix="VirtualNetwork",
                    destination_address_prefix="VirtualNetwork",
                ),
                network.SecurityRuleArgs(
                    name="InboundDenyAll",
                    priority=4000,
                    direction=network.SecurityRuleDirection.INBOUND,
                    access=network.SecurityRuleAccess.DENY,
                    protocol=network.SecurityRuleProtocol.ASTERISK,
                    source_port_range="*",
                    destination_port_range="*",
                    source_address_prefix="*",
                    destination_address_prefix="*",
                ),
            ],
            opts=pulumi.ResourceOptions(
                parent=self,
            ),
        )

        self.app_gateway_subnet = network.Subnet(
            self.workload.app_gateway_subnet_name,
            subnet_name=self.workload.app_gateway_subnet_name,
            resource_group_name=self.vnet_rsg_name,
            virtual_network_name=self.vnet_name,
            address_prefix=self.workload.cfg.network.app_gateway_subnet_cidr,
            network_security_group=network.SubResourceArgs(id=app_gateway_nsg.id),
            opts=pulumi.ResourceOptions(
                parent=self.vnet if self.vnet else None,
                protect=self.workload.cfg.protect_persistent_resources,
            ),
        )

        # Bastion network security group and subnet
        # See: https://learn.microsoft.com/en-us/azure/bastion/bastion-nsg
        bastion_nsg = network.NetworkSecurityGroup(
            resource_name=f"nsg-ptd-{self.workload.compound_name}-bastion",
            network_security_group_name=f"nsg-ptd-{self.workload.compound_name}-bastion",
            resource_group_name=self.vnet_rsg_name,
            location=self.workload.cfg.region,
            security_rules=[
                # Inbound rules
                network.SecurityRuleArgs(
                    name="AllowHttpsInboundFromInternet",
                    priority=100,
                    direction=network.SecurityRuleDirection.INBOUND,
                    access=network.SecurityRuleAccess.ALLOW,
                    protocol=network.SecurityRuleProtocol.TCP,
                    source_port_range="*",
                    destination_port_range="443",
                    source_address_prefix="Internet",
                    destination_address_prefix="*",
                ),
                network.SecurityRuleArgs(
                    name="AllowGatewayManagerInbound",
                    priority=110,
                    direction=network.SecurityRuleDirection.INBOUND,
                    access=network.SecurityRuleAccess.ALLOW,
                    protocol=network.SecurityRuleProtocol.TCP,
                    source_port_range="*",
                    destination_port_range="443",
                    source_address_prefix="GatewayManager",
                    destination_address_prefix="*",
                ),
                network.SecurityRuleArgs(
                    name="AllowVnetInbound",
                    priority=120,
                    direction=network.SecurityRuleDirection.INBOUND,
                    access=network.SecurityRuleAccess.ALLOW,
                    protocol=network.SecurityRuleProtocol.TCP,
                    source_port_range="*",
                    destination_port_ranges=["8080", "5701"],
                    source_address_prefix="VirtualNetwork",
                    destination_address_prefix="VirtualNetwork",
                ),
                network.SecurityRuleArgs(
                    name="AllowAzureLoadBalancerInbound",
                    priority=130,
                    direction=network.SecurityRuleDirection.INBOUND,
                    access=network.SecurityRuleAccess.ALLOW,
                    protocol=network.SecurityRuleProtocol.TCP,
                    source_port_range="*",
                    destination_port_range="443",
                    source_address_prefix="AzureLoadBalancer",
                    destination_address_prefix="*",
                ),
                network.SecurityRuleArgs(
                    name="DenyAllInbound",
                    priority=4000,
                    direction=network.SecurityRuleDirection.INBOUND,
                    access=network.SecurityRuleAccess.DENY,
                    protocol=network.SecurityRuleProtocol.ASTERISK,
                    source_port_range="*",
                    destination_port_range="*",
                    source_address_prefix="*",
                    destination_address_prefix="*",
                ),
                # Outbound rules
                network.SecurityRuleArgs(
                    name="AllowSshRdpOutbound",
                    priority=100,
                    direction=network.SecurityRuleDirection.OUTBOUND,
                    access=network.SecurityRuleAccess.ALLOW,
                    protocol=network.SecurityRuleProtocol.TCP,
                    source_port_range="*",
                    destination_port_ranges=["22", "3389"],
                    source_address_prefix="*",
                    destination_address_prefix="VirtualNetwork",
                ),
                network.SecurityRuleArgs(
                    name="AllowVnetOutbound",
                    priority=110,
                    direction=network.SecurityRuleDirection.OUTBOUND,
                    access=network.SecurityRuleAccess.ALLOW,
                    protocol=network.SecurityRuleProtocol.TCP,
                    source_port_range="*",
                    destination_port_ranges=["8080", "5701"],
                    source_address_prefix="VirtualNetwork",
                    destination_address_prefix="VirtualNetwork",
                ),
                network.SecurityRuleArgs(
                    name="AllowAzureCloudOutbound",
                    priority=120,
                    direction=network.SecurityRuleDirection.OUTBOUND,
                    access=network.SecurityRuleAccess.ALLOW,
                    protocol=network.SecurityRuleProtocol.TCP,
                    source_port_range="*",
                    destination_port_range="443",
                    source_address_prefix="*",
                    destination_address_prefix="AzureCloud",
                ),
                network.SecurityRuleArgs(
                    name="AllowInternetOutbound",
                    priority=130,
                    direction=network.SecurityRuleDirection.OUTBOUND,
                    access=network.SecurityRuleAccess.ALLOW,
                    protocol=network.SecurityRuleProtocol.TCP,
                    source_port_range="*",
                    destination_port_range="80",
                    source_address_prefix="*",
                    destination_address_prefix="Internet",
                ),
                network.SecurityRuleArgs(
                    name="DenyAllOutbound",
                    priority=4000,
                    direction=network.SecurityRuleDirection.OUTBOUND,
                    access=network.SecurityRuleAccess.DENY,
                    protocol=network.SecurityRuleProtocol.ASTERISK,
                    source_port_range="*",
                    destination_port_range="*",
                    source_address_prefix="*",
                    destination_address_prefix="*",
                ),
            ],
            opts=pulumi.ResourceOptions(
                parent=self,
            ),
        )

        self.bastion_subnet = network.Subnet(
            "AzureBastionSubnet",
            resource_group_name=self.vnet_rsg_name,
            virtual_network_name=self.vnet_name,
            address_prefix=self.workload.cfg.network.bastion_subnet_cidr,
            network_security_group=network.SubResourceArgs(id=bastion_nsg.id),
            opts=pulumi.ResourceOptions(
                parent=self.vnet if self.vnet else None,
                protect=self.workload.cfg.protect_persistent_resources,
            ),
        )

    def _define_file_storage(self):
        netapp_account = netapp.Account(
            self.workload.netapp_account_name,
            account_name=self.workload.netapp_account_name,
            resource_group_name=self.workload.resource_group_name,
            location=self.workload.cfg.region,
            opts=pulumi.ResourceOptions(
                protect=self.workload.cfg.protect_persistent_resources,
            ),
        )

        self.capacity_pool = netapp.CapacityPool(
            self.workload.netapp_pool_name,
            pool_name=self.workload.netapp_pool_name,
            resource_group_name=self.workload.resource_group_name,
            account_name=netapp_account.name,
            location=self.workload.cfg.region,
            service_level=netapp.ServiceLevel.PREMIUM,
            size=1099511627776,  # 1 TiB (min requirement)
            opts=pulumi.ResourceOptions(
                protect=self.workload.cfg.protect_persistent_resources,
            ),
        )

    def _define_postgres(self):
        self.postgres = self._define_database_resources(
            name=f"{self.workload.compound_name}", db_instance_type="Standard_B2s"
        )
        self.grafana_postgres = self._define_database_resources(
            name=f"{self.workload.compound_name}-grafana", db_instance_type="Standard_B1ms"
        )

    def _define_database_resources(self, name: str, db_instance_type: str) -> pulumi_az_pg.Server:
        pw = pulumi_random.RandomPassword(
            f"{name}-db-pw",
            special=True,
            override_special="-_",
            length=36,
            opts=pulumi.ResourceOptions(parent=self),
        )

        dns = privatedns.PrivateZone(
            f"{name}-private-dns-zone",
            location="Global",
            resource_group_name=self.workload.resource_group_name,
            private_zone_name=f"{name}.ptd.postgres.database.azure.com",
            opts=pulumi.ResourceOptions(
                protect=self.workload.cfg.protect_persistent_resources,
            ),
            tags=self.required_tags,
        )

        privatedns.VirtualNetworkLink(
            f"{name}-dns-vnet-link",
            resource_group_name=self.workload.resource_group_name,
            virtual_network_link_name=f"{name}-dns-vnet-link",
            location="global",
            private_zone_name=dns.name,
            registration_enabled=False,
            virtual_network=network.SubResourceArgs(id=self.vnet_id),
            tags=self.required_tags,
        )

        server = pulumi_az_pg.Server(
            f"psql-ptd-{name}",
            administrator_login=DB_ADMIN_USERNAME,
            administrator_login_password=pw.result,
            location=self.workload.cfg.region,
            data_encryption=pulumi_az_pg.DataEncryptionArgs(
                type=pulumi_az_pg.ArmServerKeyType.SYSTEM_MANAGED,
            ),
            resource_group_name=self.workload.resource_group_name,
            server_name=f"psql-ptd-{name}",
            network=pulumi_az_pg.NetworkArgs(
                delegated_subnet_resource_id=self.db_subnet.id,
                private_dns_zone_arm_resource_id=dns.id,
            ),
            sku=pulumi_az_pg.SkuArgs(
                name=db_instance_type,
                tier=pulumi_az_pg.SkuTier.BURSTABLE,
            ),
            storage=pulumi_az_pg.StorageArgs(
                auto_grow=pulumi_az_pg.StorageAutoGrow.ENABLED,
                tier=pulumi_az_pg.AzureManagedDiskPerformanceTiers.P10,  # 500 iops
                storage_size_gb=128,
                type=pulumi_az_pg.StorageType.PREMIUM_LRS,
            ),
            version=pulumi_az_pg.ServerVersion.SERVER_VERSION_14,
            opts=pulumi.ResourceOptions(
                protect=self.workload.cfg.protect_persistent_resources,
            ),
            tags=self.required_tags,
        )

        secret_val = pulumi.Output.all(pw=pw.result, fqdn=server.fully_qualified_domain_name).apply(
            lambda outputs: json.dumps(
                {
                    "fqdn": outputs["fqdn"],
                    "username": DB_ADMIN_USERNAME,
                    "password": outputs["pw"],
                }
            )
        )

        pulumi_az.keyvault.Secret(
            f"{name}-postgres-admin-secret",
            secret_name=f"{name}-postgres-admin-secret",
            resource_group_name=self.workload.resource_group_name,
            properties=pulumi_az.keyvault.SecretPropertiesArgs(value=secret_val),
            vault_name=self.workload.key_vault_name,
            opts=pulumi.ResourceOptions(
                protect=self.workload.cfg.protect_persistent_resources,
            ),
            tags=self.required_tags,
        )

        return server

    def _define_chronicle_container(self):
        self.chronicle_container = self._create_blob_container("chronicle")

    def _define_loki_container(self):
        self.loki_container = self._create_blob_container("loki")

    def _define_files_storage_account(self):
        storage_account_name = self.workload.azure_files_storage_account_name

        # Create a Private DNS Zone for Azure Files
        private_dns_zone = privatedns.PrivateZone(
            f"{self.workload.compound_name}-files-dns-zone",
            location="Global",
            resource_group_name=self.workload.resource_group_name,
            private_zone_name="privatelink.file.core.windows.net",
            tags=self.required_tags,
            opts=pulumi.ResourceOptions(
                protect=self.workload.cfg.protect_persistent_resources,
            ),
        )

        # Link the Private DNS Zone to the VNet
        vnet_dns_link = privatedns.VirtualNetworkLink(
            f"{self.workload.compound_name}-files-dns-link",
            resource_group_name=self.workload.resource_group_name,
            virtual_network_link_name=f"{self.workload.compound_name}-files-dns-link",
            location="global",
            private_zone_name=private_dns_zone.name,
            registration_enabled=False,
            resolution_policy="NxDomainRedirect",  # Enable DNS fallback for NXDOMAIN responses
            virtual_network=network.SubResourceArgs(id=self.vnet_id),
            tags=self.required_tags,
            opts=pulumi.ResourceOptions(
                protect=self.workload.cfg.protect_persistent_resources,
            ),
        )

        # Create the storage account
        self.files_storage_account = storage.StorageAccount(
            resource_name=f"{self.workload.compound_name}-files-storage",
            account_name=storage_account_name,
            resource_group_name=self.workload.resource_group_name,
            location=self.workload.cfg.region,
            kind=storage.Kind.FILE_STORAGE,
            enable_https_traffic_only=False,  # required for NFS with Azure Files, see network_rules for other security measures in place
            sku=storage.SkuArgs(
                name=storage.SkuName.PREMIUM_LRS,
            ),
            minimum_tls_version=storage.MinimumTlsVersion.TLS1_2,
            network_rule_set=storage.NetworkRuleSetArgs(
                # deny public access
                default_action=storage.DefaultAction.DENY,
                # Allow access from the VNet
                virtual_network_rules=[
                    storage.VirtualNetworkRuleArgs(
                        virtual_network_resource_id=self.private_subnet.id,
                        action=storage.Action.ALLOW,
                    ),
                ],
                # Allow Azure services to access the storage account
                bypass=storage.Bypass.AZURE_SERVICES,
            ),
            tags=self.required_tags,
            opts=pulumi.ResourceOptions(
                protect=self.workload.cfg.protect_persistent_resources,
                depends_on=[vnet_dns_link],
            ),
        )

        # Create a Private Endpoint for the Azure Files storage account in the private subnet
        private_endpoint = network.PrivateEndpoint(
            resource_name=f"{self.workload.compound_name}-files-pe",
            private_endpoint_name=f"{self.workload.compound_name}-files-pe",
            resource_group_name=self.workload.resource_group_name,
            location=self.workload.cfg.region,
            subnet=network.SubResourceArgs(id=self.private_subnet.id),
            private_link_service_connections=[
                network.PrivateLinkServiceConnectionArgs(
                    name=f"{self.workload.compound_name}-files-plsc",
                    private_link_service_id=self.files_storage_account.id,
                    group_ids=["file"],
                )
            ],
            tags=self.required_tags,
            opts=pulumi.ResourceOptions(
                protect=self.workload.cfg.protect_persistent_resources,
                depends_on=[self.files_storage_account],
            ),
        )

        # Create a Private DNS Zone Group for the Private Endpoint
        network.PrivateDnsZoneGroup(
            resource_name=f"{self.workload.compound_name}-files-dns-zone-group",
            private_dns_zone_group_name="default",
            private_endpoint_name=private_endpoint.name,
            resource_group_name=self.workload.resource_group_name,
            private_dns_zone_configs=[
                network.PrivateDnsZoneConfigArgs(
                    name="privatelink-file-core-windows-net",
                    private_dns_zone_id=private_dns_zone.id,
                )
            ],
            opts=pulumi.ResourceOptions(
                protect=self.workload.cfg.protect_persistent_resources,
                depends_on=[private_endpoint],
            ),
        )

        return self.files_storage_account

    def _create_blob_container(self, container_name) -> storage.BlobContainer:
        # in the 'clusters' step we create any required service principals
        # with permisisons to read site-specific paths within this shared container

        return storage.BlobContainer(
            f"{self.workload.compound_name}-{container_name}-container",
            account_name=self.workload.storage_account_name,
            container_name=container_name,
            resource_group_name=self.workload.resource_group_name,
            opts=pulumi.ResourceOptions(
                protect=self.workload.cfg.protect_persistent_resources,
            ),
        )

    def _define_container_registry(self):
        self.acr_registry = containerregistry.Registry(
            self.workload.acr_registry,
            registry_name=self.workload.acr_registry,
            admin_user_enabled=False,
            location=self.workload.cfg.region,
            resource_group_name=self.workload.resource_group_name,
            sku=containerregistry.SkuArgs(
                name=containerregistry.SkuName.STANDARD,
            ),
            opts=pulumi.ResourceOptions(
                protect=self.workload.cfg.protect_persistent_resources,
            ),
            tags=self.required_tags,
        )

    def _define_dns_zones(self):
        self.dns_zones = []
        if self.workload.cfg.root_domain:
            # Create a single zone for the root domain
            self.dns_zones.append(
                dns.Zone(
                    f"{self.workload.compound_name}-dns-zone",
                    location="Global",
                    resource_group_name=self.workload.resource_group_name,
                    zone_name=self.workload.cfg.root_domain,
                    tags=self.required_tags,
                )
            )
        else:
            # Create a zone for each site domain
            for site_name, site in sorted(self.workload.cfg.sites.items()):
                self.dns_zones.append(
                    dns.Zone(
                        f"{self.workload.compound_name}-{site_name}-dns-zone",
                        location="Global",
                        resource_group_name=self.workload.resource_group_name,
                        zone_name=site.domain,
                        tags=self.required_tags,
                    )
                )

    def _define_bastion(self):
        self.bastion = AzureBastion(
            name=f"bas-ptd-{self.workload.compound_name}-bastion",
            bastion_subnet=self.bastion_subnet,
            jumpbox_subnet=self.private_subnet,
            resource_group_name=self.vnet_rsg_name,
            location=self.workload.cfg.region,
            tags=self.required_tags,
            vm_size=self.workload.cfg.bastion_instance_type,
            opts=pulumi.ResourceOptions(
                protect=self.workload.cfg.protect_persistent_resources,
            ),
        )

    def _define_mimir_password(self):
        self.mimir_password = pulumi_random.RandomPassword(
            f"{self.workload.compound_name}-mimir-auth",
            special=True,
            override_special="-/_",
            length=36,
            opts=pulumi.ResourceOptions(parent=self, protect=False),
        )

        pulumi_az.keyvault.Secret(
            f"{self.workload.compound_name}-mimir-auth",
            secret_name=f"{self.workload.compound_name}-mimir-auth",
            resource_group_name=self.workload.resource_group_name,
            properties=pulumi_az.keyvault.SecretPropertiesArgs(value=self.mimir_password.result),
            vault_name=self.workload.key_vault_name,
            opts=pulumi.ResourceOptions(
                protect=self.workload.cfg.protect_persistent_resources,
            ),
            tags=self.required_tags,
        )
