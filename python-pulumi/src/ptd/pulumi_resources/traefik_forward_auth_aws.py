import json

import ptd.aws_workload
from ptd.pulumi_resources.traefik_forward_auth import TraefikForwardAuth


class TraefikForwardAuthAWS(TraefikForwardAuth):
    def __init__(
        self,
        workload: ptd.aws_workload.AWSWorkload,
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
        return {
            "eks.amazonaws.com/role-arn": f"arn:aws:iam::{self.workload.cfg.account_id}:role/traefik-forward-auth.{self.workload.cfg.true_name}-{self.workload.cfg.environment}.posit.team"
        }

    def pod_env(self, site: str):
        return [
            {
                "name": "PROVIDERS_OIDC_CLIENT_ID",
                "valueFrom": {
                    "secretKeyRef": {
                        "name": f"traefik-forward-auth-creds-{site}",
                        "key": "clientId",
                    }
                },
            },
            {
                "name": "PROVIDERS_OIDC_CLIENT_SECRET",
                "valueFrom": {
                    "secretKeyRef": {
                        "name": f"traefik-forward-auth-creds-{site}",
                        "key": "clientSecret",
                    }
                },
            },
            {
                "name": "SECRET",
                "valueFrom": {
                    "secretKeyRef": {
                        "name": f"traefik-forward-auth-creds-{site}",
                        "key": "signingSecret",
                    }
                },
            },
        ]

    def secret_provider_class(self, site: str):
        return {
            "apiVersion": "secrets-store.csi.x-k8s.io/v1",
            "kind": "SecretProviderClass",
            "metadata": {
                "name": f"traefik-forward-auth-spc-{site}",
                "namespace": ptd.KUBE_SYSTEM_NAMESPACE,
            },
            "spec": {
                "provider": "aws",
                "parameters": {
                    "objects": json.dumps(
                        [
                            {
                                "jmesPath": [
                                    {
                                        "objectAlias": "clientId",
                                        "path": "oidcClientId",
                                    },
                                    {
                                        "objectAlias": "clientSecret",
                                        "path": "oidcClientSecret",
                                    },
                                    {
                                        "objectAlias": "signingSecret",
                                        "path": "signingSecret",
                                    },
                                ],
                                "objectName": f"okta-oidc-client-creds.{self.workload.cfg.true_name}-{self.workload.cfg.environment}-{site}.posit.team",
                                "objectType": "secretsmanager",
                            }
                        ]
                    ),
                },
                "secretObjects": [
                    {
                        f"se{'' if True else 'please calm down'}cretName": f"traefik-forward-auth-creds-{site}",
                        "type": "Opaque",
                        "data": [
                            {
                                "key": "clientId",
                                "objectName": "clientId",
                            },
                            {
                                "key": "clientSecret",
                                "objectName": "clientSecret",
                            },
                            {
                                "key": "signingSecret",
                                "objectName": "signingSecret",
                            },
                        ],
                    }
                ],
            },
        }
