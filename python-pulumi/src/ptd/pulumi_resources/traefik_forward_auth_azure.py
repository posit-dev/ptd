import textwrap

import ptd.azure_workload
from ptd.pulumi_resources.traefik_forward_auth import TraefikForwardAuth


class TraefikForwardAuthAzure(TraefikForwardAuth):
    def __init__(
        self,
        workload: ptd.azure_workload.AzureWorkload,
        release: str,
        chart_version: str,
        *args,
        **kwargs,
    ):
        super().__init__(
            workload,
            release,
            chart_version,
            *args,
            **kwargs,
        )

        self.workload = workload

    def sa_annotations(self):
        return {}

    def pod_env(self, site: str):
        return [
            {
                "name": "PROVIDERS_OIDC_CLIENT_ID",
                "valueFrom": {
                    "secretKeyRef": {
                        "name": f"traefik-forward-auth-creds-{site}",
                        "key": "clientid",
                    }
                },
            },
            {
                "name": "PROVIDERS_OIDC_CLIENT_SECRET",
                "valueFrom": {
                    "secretKeyRef": {
                        "name": f"traefik-forward-auth-creds-{site}",
                        "key": "clientsecret",
                    }
                },
            },
            {
                "name": "SECRET",
                "valueFrom": {
                    "secretKeyRef": {
                        "name": f"traefik-forward-auth-creds-{site}",
                        "key": "signingsecret",
                    }
                },
            },
        ]

    def secret_provider_class(self, site: str):
        client_id = f"{self.workload.cfg.true_name}-{self.workload.cfg.environment}-{site}-okta-clientid"
        client_secret = f"{self.workload.cfg.true_name}-{self.workload.cfg.environment}-{site}-okta-clientsecret"
        signing_secret = f"{self.workload.cfg.true_name}-{self.workload.cfg.environment}-{site}-okta-signingsecret"

        return {
            "apiVersion": "secrets-store.csi.x-k8s.io/v1",
            "kind": "SecretProviderClass",
            "metadata": {
                "name": f"traefik-forward-auth-spc-{site}",
                "namespace": ptd.KUBE_SYSTEM_NAMESPACE,
            },
            "spec": {
                "provider": "azure",
                "parameters": {
                    "usePodIdentity": "false",
                    "useVMManagedIdentity": "true",
                    "userAssignedIdentityID": self.workload.cfg.secrets_provider_client_id,
                    "keyvaultName": self.workload.key_vault_name,
                    "tenantId": self.workload.cfg.tenant_id,
                    "objects": textwrap.dedent(f"""
                            array:
                            - |
                                objectName: {client_id}
                                objectType: secret
                            - |
                                objectName: {client_secret}
                                objectType: secret
                            - |
                                objectName: {signing_secret}
                                objectType: secret
                            """),
                },
                "secretObjects": [
                    {
                        "secretName": f"traefik-forward-auth-creds-{site}",
                        "type": "Opaque",
                        "data": [
                            {
                                "key": "clientid",
                                "objectName": client_id,
                                "objectAlias": "clientid",
                            },
                            {
                                "key": "clientsecret",
                                "objectName": client_secret,
                                "objectAlias": "clientsecret",
                            },
                            {
                                "key": "signingsecret",
                                "objectName": signing_secret,
                                "objectAlias": "signingsecret",
                            },
                        ],
                    },
                ],
            },
        }
