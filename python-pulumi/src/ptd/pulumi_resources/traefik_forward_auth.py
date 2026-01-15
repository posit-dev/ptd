from abc import ABC, abstractmethod

import pulumi
import pulumi_kubernetes as k8s

import ptd
import ptd.aws_workload
import ptd.pulumi_resources.aws_eks_cluster
import ptd.workload


class TraefikForwardAuth(pulumi.ComponentResource, ABC):
    def __init__(
        self,
        workload: ptd.workload.AbstractWorkload,
        release: str,
        chart_version: str,
        *args,
        **kwargs,
    ):
        super().__init__(
            f"ptd:{self.__class__.__name__}",
            f"{workload.compound_name}-{release}-traefik-forward-auth",
            *args,
            **kwargs,
        )

        self.workload = workload
        self.release = release
        self.chart_version = chart_version

        self._define_service_account()
        self._define_forward_headers_middleware()
        self._define_helm_installs()

        self.register_outputs({})

    @abstractmethod
    def sa_annotations(self):
        pass

    @abstractmethod
    def secret_provider_class(self, site: str):
        pass

    @abstractmethod
    def pod_env(self):
        pass

    def _define_service_account(self):
        k8s.core.v1.ServiceAccount(
            f"{self.workload.compound_name}-{self.release}-traefik-forward-auth",
            api_version="v1",
            metadata=k8s.meta.v1.ObjectMetaArgs(
                name=str(ptd.Roles.TRAEFIK_FORWARD_AUTH),
                namespace=ptd.KUBE_SYSTEM_NAMESPACE,
                annotations=self.sa_annotations(),
            ),
            opts=pulumi.ResourceOptions(parent=self),
        )

    def _define_forward_headers_middleware(self):
        k8s.apiextensions.CustomResource(
            f"{self.workload.compound_name}-{self.release}-traefik-forward-auth-headers-middleware",
            api_version="traefik.io/v1alpha1",
            kind="Middleware",
            metadata=k8s.meta.v1.ObjectMetaArgs(
                name="traefik-forward-auth-add-forwarded-headers",
                namespace=ptd.KUBE_SYSTEM_NAMESPACE,
            ),
            spec={
                "headers": {
                    "customRequestHeaders": {
                        "X-Forwarded-Proto": "https",
                        "X-Forwarded-Port": "443",
                    },
                }
            },
            opts=pulumi.ResourceOptions(parent=self),
        )

    def _define_auth_middleware(self, site: str):
        k8s.apiextensions.CustomResource(
            f"traefik-forward-auth-{self.release}-{site}",
            api_version="traefik.io/v1alpha1",
            kind="Middleware",
            metadata=k8s.meta.v1.ObjectMetaArgs(
                name=f"traefik-forward-auth-{site}",
                namespace=ptd.KUBE_SYSTEM_NAMESPACE,
            ),
            spec={
                "forwardAuth": {
                    "address": f"http://traefik-forward-auth-{site}.{ptd.KUBE_SYSTEM_NAMESPACE}.svc.cluster.local",
                    "trustForwardHeader": True,
                    "authResponseHeaders": ["X-Forwarded-User"],
                    "preserveRequestMethod": True,
                }
            },
            opts=pulumi.ResourceOptions(parent=self),
        )

    def _define_helm_installs(self):
        for site_name, site in self.workload.cfg.sites.items():
            if site.use_traefik_forward_auth is True:
                domain = site.domain

                self._define_auth_middleware(site=site_name)

                k8s.helm.v3.Release(
                    f"{self.workload.compound_name}-{self.release}-traefik-forward-auth-{site_name}",
                    chart=f"https://github.com/colearendt/helm/releases/download/traefik-forward-auth-{self.chart_version}/traefik-forward-auth-{self.chart_version}.tgz",
                    namespace=ptd.KUBE_SYSTEM_NAMESPACE,
                    name=f"traefik-forward-auth-{site_name}",
                    atomic=False,
                    values={
                        "config": helm_config(domain),
                        "serviceAccount": {
                            "create": False,
                            "name": str(ptd.Roles.TRAEFIK_FORWARD_AUTH),
                        },
                        "extraObjects": [self.secret_provider_class(site=site_name)],
                        "pod": {
                            "env": self.pod_env(site=site_name),
                            "volumes": [
                                {
                                    "name": "oidc-client-creds",
                                    "csi": {
                                        "driver": "secrets-store.csi.k8s.io",
                                        "readOnly": True,
                                        "volumeAttributes": {
                                            "secretProviderClass": f"traefik-forward-auth-spc-{site_name}",
                                        },
                                    },
                                },
                            ],
                            "volumeMounts": [
                                {
                                    "name": "oidc-client-creds",
                                    "mountPath": "/mnt/secrets/oidc-client-creds",
                                    "readOnly": True,
                                }
                            ],
                        },
                        "ingress": {
                            "enabled": True,
                            "className": "traefik",
                            "annotations": {
                                "traefik.ingress.kubernetes.io/router.middlewares": f"{ptd.KUBE_SYSTEM_NAMESPACE}-traefik-forward-auth-add-forwarded-headers@kubernetescrd,{ptd.KUBE_SYSTEM_NAMESPACE}-traefik-forward-auth-{site_name}@kubernetescrd",
                            },
                            "hosts": [
                                {
                                    "host": f"sso.{domain}",
                                    "paths": ["/"],
                                }
                            ],
                        },
                    },
                    opts=pulumi.ResourceOptions(parent=self),
                )


def helm_config(domain: str):
    return {
        "auth-host": f"sso.{domain}",
        "cookie-domain": domain,
        "cookie-name": "ptd_auth",
        "csrf-cookie-name": "csrf_ptd_auth",
        "default-provider": "oidc",
        "log-level": "debug",
        "providers.oidc.issuer-url": "https://posit.okta.com",
        "url-path": "/__oauth__",
        "rule.ptd-flightdeck.action": "allow",
        "rule.ptd-flightdeck.rule": " ".join(
            [
                s.strip()
                for s in f"""
                                    Host(`{domain}`) && (
                                        HeadersRegexp(`Authorization`, `^B(asic|earer) .*`) ||
                                        PathPrefix(`/static`) ||
                                        PathPrefix(`/dl`) ||
                                        PathPrefix(`/api`)
                                    )
                                    """.splitlines()
            ]
        ),
        "rule.ptd-ide.action": "allow",
        "rule.ptd-ide.rule": " ".join(
            [
                s.strip()
                for s in f"""
                                    (
                                        Host(`dev.{domain}`) ||
                                        Host(`dev-{domain}`)
                                    ) &&
                                    HeadersRegexp(`Authorization`, `^Bearer .*`) && (
                                        PathPrefix(`/api`) ||
                                        PathPrefix(`/scim/v2/`)
                                    )
                                    """.splitlines()
            ]
        ),
        "rule.ptd-ide-client-heartbeat.action": "allow",
        "rule.ptd-ide-client-heartbeat.rule": " ".join(
            [
                s.strip()
                for s in f"""
                                    (
                                        Host(`dev.{domain}`) ||
                                        Host(`dev-{domain}`)
                                    ) && (
                                        PathPrefix(`/heartbeat`)
                                    )
                                    """.splitlines()
            ]
        ),
        "rule.ptd-pub-public.action": "allow",
        "rule.ptd-pub-public.rule": " ".join(
            [
                s.strip()
                for s in f"""
                                    (
                                        Host(`pub.{domain}`) ||
                                        Host(`pub-{domain}`)
                                    ) && (
                                        PathPrefix(`/public`) ||
                                        PathPrefix(`/connect/out/unauthorized/`) ||
                                        Path(`/connect/__favicon__`) ||
                                        Path(`/__api__/server_settings`) ||
                                        Path(`/__api__/v1/user`) ||
                                        Path(`/.well-known/openid-configuration`) ||
                                        Path(`/openid/v1/jwks`) ||
                                        Path(`/__api__/tokens`)
                                    )
                                    """.splitlines()
            ]
        ),
        "rule.ptd-pub.action": "allow",
        "rule.ptd-pub.rule": " ".join(
            [
                s.strip()
                for s in f"""
                                    (
                                        Host(`pub.{domain}`) ||
                                        Host(`pub-{domain}`)
                                    ) && (
                                        HeadersRegexp(`X-Auth-Token`, `.*`) ||
                                        HeadersRegexp(`Authorization`, `^Key .*`)
                                    )
                                    """.splitlines()
            ]
        ),
        "rule.ptd-pkg.action": "allow",
        "rule.ptd-pkg.rule": " ".join(
            [
                s.strip()
                for s in f"""
                                    (
                                        Host(`pkg.{domain}`) ||
                                        Host(`pkg-{domain}`)
                                    )
                                    """.splitlines()
            ]
        ),
        "rule.ptd-dev-health.action": "allow",
        "rule.ptd-dev-health.rule": " ".join(
            [
                s.strip()
                for s in f"""
                        (
                            Host(`dev.{domain}`) ||
                            Host(`dev-{domain}`)
                        ) && (
                            Path(`/health-check`) &&
                            HeadersRegexp(`X-PTD-Health`, `.*`)
                        )
                        """.splitlines()
            ]
        ),
    }
