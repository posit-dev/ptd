import pulumi
import pulumi_kubernetes as kubernetes

import ptd.aws_workload


class ExternalDNS(pulumi.ComponentResource):
    workload: ptd.aws_workload.AWSWorkload
    release: str

    helm_release: kubernetes.helm.v3.Release

    def __init__(self, workload: ptd.aws_workload.AWSWorkload, release: str, *args, **kwargs):
        super().__init__(
            f"ptd:{self.__class__.__name__}",
            f"{workload.compound_name}-{release}-external-dns",
            *args,
            **kwargs,
        )

        self.workload = workload
        self.release = release

        self._define_helm_release()

        self.register_outputs({})

    def _define_helm_release(self) -> None:
        cfg_components = self.workload.cfg.clusters[self.release].components
        if cfg_components is None:
            msg = f"{self.workload.compound_name} cluster {self.release} components are None"

            pulumi.error(msg)

            raise ValueError(msg)

        version = cfg_components.external_dns_version

        if version is None:
            msg = f"{self.workload.compound_name} cluster {self.release} did not specify an External DNS version"

            pulumi.error(msg)

            raise ValueError(msg)

        version_tuple: tuple[int, ...] = tuple([int(s) for s in version.split(".")])

        self.helm_release = kubernetes.helm.v3.Release(
            f"{self.workload.compound_name}-{self.release}-external-dns",
            chart="external-dns",
            version=version,
            namespace=ptd.KUBE_SYSTEM_NAMESPACE,
            name="external-dns",
            repository_opts=kubernetes.helm.v3.RepositoryOptsArgs(
                repo="https://kubernetes-sigs.github.io/external-dns/",
            ),
            atomic=True,
            values={
                "provider": "aws",
                "serviceAccount": {
                    "create": True,
                    "name": str(ptd.Roles.EXTERNAL_DNS),
                    "annotations": {
                        "eks.amazonaws.com/role-arn": f"arn:aws:iam::{self.workload.cfg.account_id}"
                        f":role/{self.workload.external_dns_role_name}",
                    },
                },
                "domainFilters": [*sorted([site.domain for site in self.workload.cfg.sites.values()])],
                "env": [
                    {
                        "name": "AWS_DEFAULT_REGION",
                        "value": self.workload.cfg.region,
                    },
                    {
                        "name": "AWS_REGION",
                        "value": self.workload.cfg.region,
                    },
                ],
                "extraArgs": (
                    [
                        "--aws-zone-match-parent",
                    ]
                    if version_tuple >= (1, 14, 0)
                    else []
                ),
                "policy": "sync",
                "txtOwnerId": self.workload.eks_cluster_name(self.release),
                "txtPrefix": "_d",
            },
            opts=pulumi.ResourceOptions(parent=self),
        )
