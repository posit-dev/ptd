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

        self._define_namespace()
        self._define_helm_release()

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

    def _define_helm_release(self):
        self.helm_release = k8s.helm.v3.Release(
            f"{self.name}-{self.release}-tigera-operator",
            chart="tigera-operator",
            version="3.26.1",
            namespace="tigera-operator",
            name="tigera-operator",
            repository_opts=k8s.helm.v3.RepositoryOptsArgs(
                repo="https://docs.projectcalico.org/charts",
            ),
            atomic=False,
            values={
                "resources": {
                    "requests": {
                        "cpu": "100m",
                        "memory": "128Mi",
                        "ephemeral-storage": "1Gi",
                    },
                    "limits": {
                        "memory": "128Mi",
                        "ephemeral-storage": "2Gi",
                    },
                },
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
                    "nonPrivileged": "Enabled",
                },
            },
            opts=pulumi.ResourceOptions(parent=self, depends_on=self.namespace),
        )
