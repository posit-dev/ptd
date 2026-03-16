import pulumi
import pulumi_tls as tls
from pulumi_azure_native import compute, network


class AzureBastion(pulumi.ComponentResource):
    tags: dict[str, str]
    name: str
    vnet_name: str | pulumi.Output[str]
    resource_group_name: str | pulumi.Output[str]
    location: str | pulumi.Output[str]

    bastion_host: network.BastionHost
    jumpbox_ssh_key: tls.PrivateKey
    jumpbox_host: compute.VirtualMachine
    public_ip: network.PublicIPAddress
    bastion_subnet: network.Subnet
    jumpbox_subnet: network.Subnet

    def __init__(
        self,
        name: str,
        bastion_subnet: network.Subnet | pulumi.Output[network.Subnet],
        jumpbox_subnet: network.Subnet | pulumi.Output[network.Subnet],
        resource_group_name: str | pulumi.Output[str],
        location: str | pulumi.Output[str],
        tags: dict[str, str],
        vm_size: str | pulumi.Output[str],
        image_version: str,
        *args,
        **kwargs,
    ):
        kwargs["opts"] = pulumi.ResourceOptions.merge(
            kwargs.get("opts"),
            pulumi.ResourceOptions(aliases=[pulumi.Alias(name=name, type_="ptd:Bastion")]),
        )
        super().__init__(
            f"ptd:{self.__class__.__name__}",
            f"{name}",
            *args,
            **kwargs,
        )

        self.vm_size = vm_size

        # generate a key pair for the jumpbox
        self.jumpbox_ssh_key = tls.PrivateKey(
            "ssh-key",
            algorithm="ED25519",
        )

        # Create a Public IP for Bastion
        self.public_ip = network.PublicIPAddress(
            f"{name}-pip",
            resource_group_name=resource_group_name,
            public_ip_allocation_method="Static",
            sku=network.PublicIPAddressSkuArgs(name="Standard"),
            tags=tags | {"Name": f"{name}-pip"},
            opts=pulumi.ResourceOptions(parent=self),
        )

        # Create the Bastion Host
        self.bastion_host = network.BastionHost(
            f"{name}-host",
            resource_group_name=resource_group_name,
            location=location,
            ip_configurations=[
                network.BastionHostIPConfigurationArgs(
                    name="bastionIpConfig",
                    public_ip_address=network.SubResourceArgs(id=self.public_ip.id),
                    subnet=network.SubResourceArgs(id=bastion_subnet.id),
                )
            ],
            enable_tunneling=True,
            sku=network.SkuArgs(name="Standard"),
            tags=tags | {"Name": f"{name}-bastion-host"},
            opts=pulumi.ResourceOptions(parent=self),
        )

        jumpbox_nic = network.NetworkInterface(
            f"{name}-jumpbox-nic",
            resource_group_name=resource_group_name,
            location=location,
            ip_configurations=[
                network.NetworkInterfaceIPConfigurationArgs(
                    name="internal",
                    subnet=network.SubnetArgs(id=jumpbox_subnet.id),
                )
            ],
        )

        # network.IP

        self.jumpbox_host = compute.VirtualMachine(
            f"{name}-jumpbox",
            resource_group_name=resource_group_name,
            location=location,
            hardware_profile=compute.HardwareProfileArgs(
                vm_size=self.vm_size,
            ),
            storage_profile=compute.StorageProfileArgs(
                image_reference=compute.ImageReferenceArgs(
                    publisher="Canonical",
                    offer="0001-com-ubuntu-server-jammy",
                    sku="22_04-lts-gen2",
                    version=image_version,
                ),
                os_disk=compute.OSDiskArgs(
                    name=f"{name}-jumpbox-osdisk",
                    caching=compute.CachingTypes("ReadWrite"),
                    create_option="FromImage",
                    delete_option="Delete",
                ),
            ),
            network_profile=compute.NetworkProfileArgs(
                network_interfaces=[
                    compute.NetworkInterfaceReferenceArgs(
                        id=jumpbox_nic.id,
                        primary=True,
                    )
                ],
            ),
            os_profile=compute.OSProfileArgs(
                admin_username="ptd-admin",
                computer_name=f"{name}-jumpbox",
                linux_configuration=compute.LinuxConfigurationArgs(
                    disable_password_authentication=True,
                    patch_settings=compute.LinuxPatchSettingsArgs(
                        patch_mode="AutomaticByPlatform",
                        assessment_mode="AutomaticByPlatform",
                    ),
                    ssh=compute.SshConfigurationArgs(
                        public_keys=[
                            compute.SshPublicKeyArgs(
                                path="/home/ptd-admin/.ssh/authorized_keys",
                                key_data=self.jumpbox_ssh_key.public_key_openssh,
                            )
                        ],
                    ),
                ),
            ),
            tags=tags | {"Name": f"{name}-jumpbox", "ImageVersion": image_version},
            opts=pulumi.ResourceOptions(
                parent=self,
                protect=False,  # allow the VM to be recreated on image version updates
                replace_on_changes=["storageProfile.imageReference.version"],
                delete_before_replace=True,
            ),
        )
