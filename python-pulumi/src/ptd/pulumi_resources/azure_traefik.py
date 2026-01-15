import pulumi
import pulumi_kubernetes as k8s

import ptd.azure_workload

AZURE_TRAEFIK_NAMESPACE = "traefik"


class AzureTraefik(pulumi.ComponentResource):
    domains: list[str]
    release: str
    workload: ptd.azure_workload.AzureWorkload

    traefik: k8s.helm.v3.Release | None

    def __init__(
        self,
        domains: list[str],
        release: str,
        workload: ptd.azure_workload.AzureWorkload,
        *args,
        **kwargs,
    ):
        super().__init__(
            f"ptd:{self.__class__.__name__}",
            f"{workload.compound_name}-traefik",
            None,
            *args,
            **kwargs,
        )

        self.domains = domains
        self.release = release
        self.workload = workload
        self.traefik: k8s.helm.v3.Release | None = None

        self._define_namespace()
        self._define_helm_release()

        self.register_outputs({})

    def _define_namespace(self):
        k8s.core.v1.Namespace(
            f"{self.workload.compound_name}-{self.release}-traefik-namespace",
            metadata=k8s.meta.v1.ObjectMetaArgs(
                name=AZURE_TRAEFIK_NAMESPACE,
            ),
            opts=pulumi.ResourceOptions(parent=self),
        )

    def _define_helm_release(self):
        self.traefik = k8s.helm.v3.Release(
            f"{self.workload.compound_name}-{self.release}-traefik",
            chart="traefik",
            version="33.2.1",
            namespace=AZURE_TRAEFIK_NAMESPACE,
            name="traefik",
            repository_opts=k8s.helm.v3.RepositoryOptsArgs(
                repo="https://traefik.github.io/charts",
            ),
            atomic=True,
            values={
                "logs": {
                    "general": {
                        "level": "DEBUG",
                    },
                },
                "ports": {
                    "web": {
                        "redirections": {
                            "entryPoint": {
                                "to": "websecure",
                                "scheme": "https",
                                "permanent": True,
                            }
                        },
                    },
                    "websecure": {
                        "tls": {
                            "enabled": True,
                        }
                    },
                },
                "ingressClass": {
                    "enabled": True,
                    "isDefaultClass": True,
                },
                "ingressRoute": {
                    "dashboard": {
                        "enabled": True,
                    }
                },
                "providers": {
                    "kubernetesCRD": {
                        "enabled": True,
                        "allowCrossNamespace": True,
                    },
                    "kubernetesIngress": {
                        "enabled": True,
                    },
                },
                "service": {
                    "annotations": {
                        "service.beta.kubernetes.io/azure-load-balancer-internal": "true",
                    },
                    "type": "LoadBalancer",
                },
                "extraObjects": [
                    self._define_redirect_middleware(),
                    *self._define_ingresses(),
                ],
            },
            opts=pulumi.ResourceOptions(parent=self),
        )

    def _define_ingresses(self):
        ingresses = []

        for domain in self.domains:
            domains = []
            domains.extend((domain, f"*.{domain}"))

            annotations = {
                "traefik.ingress.kubernetes.io/router.middlewares": "traefik-redirect-https@kubernetescrd",
            }

            if self.release in self.workload.cfg.clusters and self.workload.cfg.clusters[self.release].use_lets_encrypt:
                annotations["cert-manager.io/cluster-issuer"] = f"letsencrypt-{domain}"

            ingresses.append(
                {
                    "apiVersion": "networking.k8s.io/v1",
                    "kind": "Ingress",
                    "metadata": {
                        "name": f"{self.workload.compound_name}-{self.release}-{domain}",
                        "labels": {"app": "traefik"},
                        "namespace": AZURE_TRAEFIK_NAMESPACE,
                        "annotations": annotations,
                    },
                    "spec": {
                        "ingressClassName": "traefik",
                        "tls": [
                            {
                                "hosts": domains,
                                "secretName": f"{self.workload.compound_name}-{domain}-tls",
                            }
                        ],
                        "rules": [
                            {
                                "http": {
                                    "paths": [
                                        {
                                            "path": "/*",
                                            "pathType": "ImplementationSpecific",
                                            "backend": {
                                                "service": {
                                                    "name": "traefik",
                                                    "port": {
                                                        "number": 80,
                                                    },
                                                }
                                            },
                                        },
                                    ]
                                },
                            }
                        ],
                    },
                }
            )

        return ingresses

    # this middleware could live in the operator and be applied to *all* ingress objects regardless
    # of cloud provider but currently AWS ingress breaks if it has this annotation, needs debugging.
    def _define_redirect_middleware(self):
        return {
            "apiVersion": "traefik.io/v1alpha1",
            "kind": "Middleware",
            "metadata": {
                "name": "redirect-https",
                "namespace": AZURE_TRAEFIK_NAMESPACE,
            },
            "spec": {
                "redirectScheme": {
                    "scheme": "https",
                    "permanent": True,
                }
            },
        }
