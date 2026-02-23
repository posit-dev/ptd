import re
import typing

import pulumi
import pulumi_aws as aws
import pulumi_kubernetes as k8s

import ptd.pulumi_resources.aws_eks_cluster
import ptd.pulumi_resources.aws_vpc
from ptd.pulumi_resources.lib import format_lb_tags


class Traefik(pulumi.ComponentResource):
    def __init__(
        self,
        cluster: ptd.pulumi_resources.aws_eks_cluster.AWSEKSCluster,
        namespace: str = "default",
        node_selector: str = "",
        cert: aws.acm.Certificate | None = None,
        deployment_replicas: int = 3,
        *args,
        **kwargs,
    ):
        """
        Traefik service class

        :param opts: ResourceOptions
        """

        super().__init__(
            f"ptd:{self.__class__.__name__}",
            cluster.name,
            None,
            *args,
            **kwargs,
        )
        self.namespace: str = namespace
        self.cluster = cluster
        self.node_selector: str = node_selector
        self.deployment_replicas = deployment_replicas
        self.traefik: k8s.helm.v3.Release | None = None
        self._deploy(cert)

        self.register_outputs({})

    # define the DNS record that points to the load balancer
    def define_domains(
        self,
        domains_to_cnames: dict[str, str],
        zone: aws.route53.Zone | aws.route53.GetZoneResult,
    ):
        if self.traefik is None:
            msg = "traefik helm release is not initialized"
            raise ValueError(msg)

        svc = k8s.core.v1.Service.get(
            "traefik",
            id=pulumi.Output.concat(self.traefik.namespace.apply(str), "/", self.traefik.name.apply(str)),
            opts=pulumi.ResourceOptions(
                depends_on=self.traefik,
                provider=self.cluster.provider,
                parent=self,
            ),
        )

        alb = svc.status.apply(
            lambda status: aws.alb.get_load_balancer(
                name=typing.cast(
                    re.Match,
                    re.search(
                        # yeah, this isn't sketchy at all... 😬
                        # maybe defining a name would make this easier? (if less reusable)
                        # match all but the last term of the dashy service name... (as per convention in AWS naming...)
                        "([a-zA-Z0-9-]*)-",
                        (
                            ""
                            if status is None or status.load_balancer is None or status.load_balancer.ingress is None
                            else str(status.load_balancer.ingress[0].hostname)
                        ),
                    ),
                ).groups()[0],
                opts=pulumi.InvokeOptions(parent=svc),
            ),
        )

        for domain in domains_to_cnames:
            aws.route53.Record(
                f"{self.cluster.name}-{domain}-A",
                args=aws.route53.RecordArgs(
                    zone_id=zone.zone_id,
                    name=domain,
                    type="A",
                    aliases=[
                        aws.route53.RecordAliasArgs(
                            evaluate_target_health=True,
                            name=alb.dns_name,
                            zone_id=alb.zone_id,
                        )
                    ],
                ),
                opts=pulumi.ResourceOptions(parent=self),
            )

            if domains_to_cnames[domain] != "":
                external_domain = domains_to_cnames[domain]
                aws.route53.Record(
                    f"{self.cluster.name}-{external_domain}-CNAME",
                    args=aws.route53.RecordArgs(
                        zone_id=zone.zone_id,
                        name=external_domain,
                        type="CNAME",
                        records=[domain],
                        ttl=300,
                    ),
                    opts=pulumi.ResourceOptions(parent=self),
                )

    def _deploy(self, cert: aws.acm.Certificate | None):
        """
        Deploy the traefik helm chart to create resources
        :return:
        """

        # Build tag string from cluster tags for NLB annotation
        if self.cluster.tags is None:
            raise ValueError(
                "Cluster tags must not be None; expected a dict with "
                "'posit.team/true-name' and 'posit.team/environment' for NLB tagging."
            )
        true_name = self.cluster.tags.get("posit.team/true-name")
        environment = self.cluster.tags.get("posit.team/environment")
        if true_name is None or environment is None:
            raise ValueError(
                "Cluster tags must include 'posit.team/true-name' and 'posit.team/environment' "
                f"for NLB tagging. Available tags: {list(self.cluster.tags.keys())}"
            )
        tags = {
            "posit.team/true-name": true_name,
            "posit.team/environment": environment,
            "Name": self.cluster.name,
        }
        nlb_tags = format_lb_tags(tags)

        self.traefik = k8s.helm.v3.Release(
            f"{self.cluster.name}-traefik",
            k8s.helm.v3.ReleaseArgs(
                chart="traefik",
                version="24.0.0",
                namespace=self.namespace,
                name="traefik",
                repository_opts=k8s.helm.v3.RepositoryOptsArgs(
                    repo="https://helm.traefik.io/traefik/",
                ),
                values={
                    "service": {
                        # TODO: we could make this into an ingress if we want...?
                        "type": "LoadBalancer",
                        "annotations": {
                            "service.beta.kubernetes.io/aws-load-balancer-type": "external",
                            "service.beta.kubernetes.io/aws-load-balancer-scheme": "internet-facing",
                            "service.beta.kubernetes.io/aws-load-balancer-ip-address-type": "ipv4",
                            "service.beta.kubernetes.io/aws-load-balancer-nlb-target-type": "ip",
                            "service.beta.kubernetes.io/aws-load-balancer-ssl-cert": (cert.arn if cert else None),
                            "service.beta.kubernetes.io/aws-load-balancer-ssl-ports": "443",
                            "service.beta.kubernetes.io/aws-load-balancer-access-log-enabled": "false",
                            "service.beta.kubernetes.io/aws-load-balancer-ssl-negotiation-policy": "ELBSecurityPolicy-FS-1-2-2019-08",
                            "service.beta.kubernetes.io/aws-load-balancer-healthcheck-healthy-threshold": "3",
                            "service.beta.kubernetes.io/aws-load-balancer-healthcheck-unhealthy-threshold": "3",
                            "service.beta.kubernetes.io/aws-load-balancer-healthcheck-timeout": "10",
                            "service.beta.kubernetes.io/aws-load-balancer-healthcheck-interval": "10",
                            "service.beta.kubernetes.io/aws-load-balancer-additional-resource-tags": nlb_tags,
                        },
                    },
                    "ports": {
                        "web": {
                            "redirectTo": "websecure",
                        },
                        "websecure": {
                            "tls": {
                                "enabled": False,
                            }
                        },
                    },
                    "nodeSelector": (
                        {
                            # "meta" nodes
                            "node.kubernetes.io/instance-type": self.node_selector,
                        }
                        if self.node_selector
                        else None
                    ),
                    "providers": {
                        "kubernetesCRD": {
                            "enabled": True,
                        },
                        "kubernetesIngress": {
                            "enabled": True,
                        },
                        "publishedService": {
                            "enabled": True,
                        },
                    },
                    "additionalArguments": [
                        "--metrics.prometheus=true",
                    ],
                    "deployment": {
                        "replicas": self.deployment_replicas,
                    },
                    "logs": {
                        "general": {"level": "DEBUG"},
                        "access": {"enabled": True},
                    },
                    "image": {
                        "registry": "ghcr.io/traefik",
                    },
                    "ingressClass": {
                        "enabled": True,
                        "default": True,
                    },
                    "ingressRoute": {
                        "dashboard": {
                            "enabled": True,
                        }
                    },
                },
            ),
            opts=pulumi.ResourceOptions(
                provider=self.cluster.provider,
                parent=self,
                delete_before_replace=True,
            ),
        )
