import pulumi
import pulumi_kubernetes as k8s


class TigeraOperator(pulumi.ComponentResource):
    namespace: k8s.core.v1.Namespace
    helm_release: k8s.helm.v3.Release

    def __init__(
        self,
        name: str,
        release: str,
        *args,
        version: str = "3.31.4",
        third_party_telemetry_enabled: bool = True,
        **kwargs,
    ):
        super().__init__(
            f"ptd:{self.__class__.__name__}",
            f"{name}-{release}-tigera-operator",
            *args,
            **kwargs,
        )

        self.name = name
        self.release = release
        self.version = version
        self.third_party_telemetry_enabled = third_party_telemetry_enabled

        self._define_namespace()

        self._adopt_felix_configuration()

        self._define_helm_release()

        self._patch_installation_cni()

        self.register_outputs(
            {
                "namespace": self.namespace,
                "helm_release": self.helm_release,
            }
        )

    def _define_namespace(self):
        self.namespace = k8s.core.v1.Namespace(
            f"{self.name}-{self.release}-tigera-ns",
            metadata=k8s.meta.v1.ObjectMetaArgs(
                name="tigera-operator",
            ),
            opts=pulumi.ResourceOptions(parent=self),
        )

    def _adopt_felix_configuration(self):
        """Add Helm ownership labels/annotations to the existing default FelixConfiguration.

        The Tigera Operator creates a default FelixConfiguration on install. When we enable
        defaultFelixConfiguration in the Helm values, Helm needs ownership metadata on the
        existing resource to adopt it.
        """
        self._felix_patch = k8s.apiextensions.CustomResourcePatch(
            f"{self.name}-{self.release}-felix-helm-adopt",
            api_version="crd.projectcalico.org/v1",
            kind="FelixConfiguration",
            metadata=k8s.meta.v1.ObjectMetaPatchArgs(
                name="default",
                labels={"app.kubernetes.io/managed-by": "Helm"},
                annotations={
                    "meta.helm.sh/release-name": "tigera-operator",
                    "meta.helm.sh/release-namespace": "tigera-operator",
                },
            ),
            opts=pulumi.ResourceOptions(parent=self, depends_on=self.namespace),
        )

    def _patch_installation_cni(self):
        """Force cni.type=Calico on the Installation CR.

        The Tigera operator auto-detects EKS and defaults cni.type to AmazonVPC
        when the field is empty. Due to a race condition during install/upgrade,
        the operator can fill this default before Helm writes the user's value.
        Once set, Helm's 3-way merge won't revert it. This patch ensures Calico
        CNI is always set regardless of operator defaulting behavior.
        """
        self._installation_patch = k8s.apiextensions.CustomResourcePatch(
            f"{self.name}-{self.release}-installation-cni-patch",
            api_version="operator.tigera.io/v1",
            kind="Installation",
            metadata=k8s.meta.v1.ObjectMetaPatchArgs(
                name="default",
            ),
            spec={
                "cni": {
                    "type": "Calico",
                    "ipam": {"type": "Calico"},
                },
            },
            opts=pulumi.ResourceOptions(parent=self, depends_on=[self.helm_release]),
        )

    def _define_helm_release(self):
        helm_depends = [self.namespace, self._felix_patch]

        self.helm_release = k8s.helm.v3.Release(
            f"{self.name}-{self.release}-tigera-operator",
            chart="tigera-operator",
            version=self.version,
            namespace="tigera-operator",
            name="tigera-operator",
            repository_opts=k8s.helm.v3.RepositoryOptsArgs(
                repo="https://docs.tigera.io/calico/charts",
            ),
            atomic=False,
            values={
                "installation": {
                    "enabled": True,
                    "registry": "quay.io",
                    "calicoNetwork": {
                        "bgp": "Enabled",
                        "hostPorts": "Enabled",
                        "ipPools": [
                            {
                                "blockSize": 26,
                                "cidr": "172.16.0.0/16",
                                "encapsulation": "VXLAN",
                                "natOutgoing": "Enabled",
                                "nodeSelector": "all()",
                            }
                        ],
                        "linuxDataplane": "Iptables",
                        "multiInterfaceMode": "None",
                        "nodeAddressAutodetectionV4": {"firstFound": True},
                    },
                    "cni": {
                        "ipam": {"type": "Calico"},
                        "type": "Calico",
                    },
                },
                "goldmane": {"enabled": False},
                "whisker": {"enabled": False},
                "defaultFelixConfiguration": {
                    "enabled": True,
                    **({"usageReportingEnabled": False} if not self.third_party_telemetry_enabled else {}),
                },
            },
            opts=pulumi.ResourceOptions(parent=self, depends_on=helm_depends),
        )
