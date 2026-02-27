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

        # Install Gateway API CRDs if enabled (before Traefik)
        if self.release in self.workload.cfg.clusters and self.workload.cfg.clusters[self.release].enable_gateway_api:
            self._define_gateway_api_crds()

        self._define_namespace()
        self._define_helm_release()

        # Create Gateway resources if enabled (after Traefik)
        if self.release in self.workload.cfg.clusters and self.workload.cfg.clusters[self.release].enable_gateway_api:
            self._define_gateway_resources()

        self.register_outputs({})

    def _build_providers_config(self) -> dict:
        """Build Traefik providers configuration conditionally based on enable_gateway_api flag."""
        providers_config = {
            "kubernetesCRD": {
                "enabled": True,
                "allowCrossNamespace": True,
            },
            "kubernetesIngress": {
                "enabled": True,
            },
        }

        # Add Gateway API provider if enabled
        if self.release in self.workload.cfg.clusters and self.workload.cfg.clusters[self.release].enable_gateway_api:
            providers_config["kubernetesGateway"] = {
                "enabled": True,
            }

        return providers_config

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
                "providers": self._build_providers_config(),
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

    def _define_gateway_api_crds(self) -> None:
        """Install Gateway API standard CRDs before Traefik deployment.

        Installs Gateway API v1.2.1 standard CRDs (Gateway, GatewayClass, HTTPRoute, ReferenceGrant).
        This must be installed before Traefik's Gateway API provider is enabled.
        """
        k8s.yaml.ConfigFile(
            f"{self.workload.compound_name}-{self.release}-gateway-api-crds",
            file="https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.2.1/standard-install.yaml",
            opts=pulumi.ResourceOptions(parent=self),
        )

    def _define_gateway_resources(self) -> None:
        """Create Gateway API resources (GatewayClass, Gateway, ReferenceGrant).

        Creates the infrastructure Gateway resources that the team-operator will reference
        via gatewayRef in Site CRs.
        """
        # Create GatewayClass
        k8s.apiextensions.CustomResource(
            f"{self.workload.compound_name}-{self.release}-traefik-gateway-class",
            api_version="gateway.networking.k8s.io/v1",
            kind="GatewayClass",
            metadata=k8s.meta.v1.ObjectMetaArgs(
                name="traefik",
            ),
            spec={
                "controllerName": "traefik.io/gateway-controller",
            },
            opts=pulumi.ResourceOptions(parent=self),
        )

        # Create Gateway in traefik namespace
        k8s.apiextensions.CustomResource(
            f"{self.workload.compound_name}-{self.release}-posit-team-gateway",
            api_version="gateway.networking.k8s.io/v1",
            kind="Gateway",
            metadata=k8s.meta.v1.ObjectMetaArgs(
                name="posit-team",
                namespace=AZURE_TRAEFIK_NAMESPACE,
            ),
            spec={
                "gatewayClassName": "traefik",
                "listeners": [
                    {
                        "name": "https",
                        "protocol": "HTTPS",
                        "port": 443,
                        "allowedRoutes": {
                            "namespaces": {
                                "from": "All",
                            },
                        },
                    },
                    {
                        "name": "http",
                        "protocol": "HTTP",
                        "port": 80,
                        "allowedRoutes": {
                            "namespaces": {
                                "from": "All",
                            },
                        },
                    },
                ],
            },
            opts=pulumi.ResourceOptions(parent=self),
        )

        # Create ReferenceGrant to allow HTTPRoutes in posit-team namespace
        # to reference Services in traefik namespace
        k8s.apiextensions.CustomResource(
            f"{self.workload.compound_name}-{self.release}-allow-posit-team-routes",
            api_version="gateway.networking.k8s.io/v1beta1",
            kind="ReferenceGrant",
            metadata=k8s.meta.v1.ObjectMetaArgs(
                name="allow-posit-team",
                namespace=AZURE_TRAEFIK_NAMESPACE,
            ),
            spec={
                "from": [
                    {
                        "group": "gateway.networking.k8s.io",
                        "kind": "HTTPRoute",
                        "namespace": "posit-team",
                    }
                ],
                "to": [
                    {
                        "group": "",
                        "kind": "Service",
                    }
                ],
            },
            opts=pulumi.ResourceOptions(parent=self),
        )
