import pulumi
import pulumi_kubernetes as kubernetes
import yaml

import ptd
import ptd.aws_workload


class NetworkPolicies(pulumi.ComponentResource):
    workload: ptd.aws_workload.AWSWorkload
    release: str

    def __init__(
        self,
        workload: ptd.aws_workload.AWSWorkload,
        release: str,
        network_trust: ptd.NetworkTrust = ptd.NetworkTrust.FULL,
        *args,
        **kwargs,
    ):
        super().__init__(
            f"ptd:{self.__class__.__name__}",
            f"{workload.compound_name}-{release}-network-policies",
            *args,
            **kwargs,
        )

        self.workload = workload
        self.release = release

        if network_trust > ptd.NetworkTrust.SAMESITE:
            self._define_allow_external()

        if network_trust <= ptd.NetworkTrust.SAMESITE:
            self._define_default_deny_policy()

        if network_trust == ptd.NetworkTrust.ZERO:
            self._define_global_default_deny_policy()

        self._define_egress_allow_dns_policy()
        self._define_egress_explicit_deny_policy()
        self._define_ec2_imds_network_set()
        self._define_flightdeck_policy()

        self.register_outputs({})

    def _define_allow_external(self) -> None:
        kubernetes.yaml.ConfigGroup(
            f"{self.workload.compound_name}-{self.release}-calico-policy-allow-external",
            yaml=[
                yaml.dump(
                    {
                        "apiVersion": "projectcalico.org/v3",
                        "kind": "NetworkPolicy",
                        "metadata": {
                            "name": f"allow-external-{self.release}",
                            "namespace": ptd.POSIT_TEAM_NAMESPACE,
                        },
                        "spec": {
                            "ingress": [
                                {
                                    "action": "Allow",
                                    "destination": {
                                        "nets": ["0.0.0.0/0"],
                                    },
                                }
                            ],
                            "egress": [
                                {
                                    "action": "Allow",
                                    "destination": {
                                        "nets": ["0.0.0.0/0"],
                                    },
                                }
                            ],
                        },
                    }
                )
            ],
            opts=pulumi.ResourceOptions(parent=self),
        )

    def _define_default_deny_policy(self) -> None:
        kubernetes.yaml.ConfigGroup(
            f"{self.workload.compound_name}-{self.release}-calico-policy-default-deny",
            yaml=[
                yaml.dump(
                    {
                        "apiVersion": "projectcalico.org/v3",
                        "kind": "NetworkPolicy",
                        "metadata": {"name": f"default-deny-{self.release}", "namespace": ptd.POSIT_TEAM_NAMESPACE},
                        "spec": {
                            "selector": "all()",
                            "types": ["Ingress", "Egress"],
                        },
                    }
                )
            ],
            opts=pulumi.ResourceOptions(parent=self),
        )

    def _define_global_default_deny_policy(self) -> None:
        kubernetes.yaml.ConfigGroup(
            f"{self.workload.compound_name}-{self.release}-calico-policy-global-default-deny",
            yaml=[
                yaml.dump(
                    {
                        "apiVersion": "projectcalico.org/v3",
                        "kind": "GlobalNetworkPolicy",
                        "metadata": {"name": f"default-deny-{self.release}"},
                        "spec": {
                            "selector": "projectcalico.org/namespace not in {'kube-system', 'calico-system', 'calico-apiserver'}",
                            "types": ["Ingress", "Egress"],
                        },
                    }
                )
            ],
            opts=pulumi.ResourceOptions(parent=self),
        )

    def _define_egress_allow_dns_policy(self) -> None:
        kubernetes.yaml.ConfigGroup(
            f"{self.workload.compound_name}-{self.release}-calico-policy-egress-allow-dns",
            yaml=[
                yaml.dump(
                    {
                        "apiVersion": "projectcalico.org/v3",
                        "kind": "NetworkPolicy",
                        "metadata": {"name": f"egress-allow-dns-{self.release}", "namespace": ptd.POSIT_TEAM_NAMESPACE},
                        "spec": {
                            "order": 100,
                            "egress": [
                                {
                                    "action": "Allow",
                                    "protocol": "TCP",
                                    "destination": {
                                        "namespaceSelector": f"projectcalico.org/name == '{ptd.KUBE_SYSTEM_NAMESPACE}'",
                                        "ports": [53],
                                    },
                                },
                                {
                                    "action": "Allow",
                                    "protocol": "UDP",
                                    "destination": {
                                        "namespaceSelector": f"projectcalico.org/name == '{ptd.KUBE_SYSTEM_NAMESPACE}'",
                                        "ports": [53],
                                    },
                                },
                            ],
                        },
                    }
                )
            ],
            opts=pulumi.ResourceOptions(parent=self),
        )

    # this policy denies egress to labeled NetworkSets for Workbench, Workbench Sessions, and Connect Sessions
    def _define_egress_explicit_deny_policy(self) -> None:
        kubernetes.yaml.ConfigGroup(
            f"{self.workload.compound_name}-{self.release}-calico-policy-egress-explicit-deny",
            yaml=[
                yaml.dump(
                    {
                        "apiVersion": "projectcalico.org/v3",
                        "kind": "NetworkPolicy",
                        "metadata": {
                            "name": f"egress-explicit-deny-{self.release}",
                            "namespace": ptd.POSIT_TEAM_NAMESPACE,
                        },
                        "spec": {
                            "selector": "posit.team/component == 'workbench' || posit.team/component == 'workbench-session' || posit.team/component == 'connect-session'",
                            "order": 160,
                            "egress": [{"action": "Deny", "destination": {"selector": "posit.team/egress == 'deny'"}}],
                        },
                    }
                )
            ],
            opts=pulumi.ResourceOptions(parent=self),
        )

    def _define_ec2_imds_network_set(self) -> None:
        kubernetes.yaml.ConfigGroup(
            f"{self.workload.compound_name}-{self.release}-calico-network-set-ec2-imds",
            yaml=[
                yaml.dump(
                    {
                        "apiVersion": "projectcalico.org/v3",
                        "kind": "NetworkSet",
                        "metadata": {
                            "name": f"ec2-imds-{self.release}",
                            "namespace": ptd.POSIT_TEAM_NAMESPACE,
                            "labels": {
                                "posit.team/egress": "deny",
                            },
                        },
                        "spec": {"nets": ["169.254.169.254/32"]},
                    }
                )
            ],
            opts=pulumi.ResourceOptions(parent=self),
        )

    def _define_flightdeck_policy(self) -> None:
        kubernetes.yaml.ConfigGroup(
            f"{self.workload.compound_name}-{self.release}-calico-policy-flightdeck-team-operator-allow",
            yaml=[
                yaml.dump(
                    {
                        "apiVersion": "projectcalico.org/v3",
                        "kind": "NetworkPolicy",
                        "metadata": {
                            "name": f"flightdeck-team-operator-policy-allow-{self.release}",
                            "namespace": ptd.POSIT_TEAM_NAMESPACE,
                        },
                        "spec": {
                            "selector": "app.kubernetes.io/managed-by == 'team-operator' && app.kubernetes.io/name == 'flightdeck'",
                            "types": ["Ingress", "Egress"],
                            "ingress": [
                                # Allow ingress from Traefik (web traffic)
                                {
                                    "action": "Allow",
                                    "protocol": "TCP",
                                    "source": {
                                        "namespaceSelector": "projectcalico.org/name == 'traefik'",
                                    },
                                    "destination": {
                                        "ports": [8080],
                                    },
                                },
                                # Allow ingress from Alloy (metrics/monitoring)
                                {
                                    "action": "Allow",
                                    "protocol": "TCP",
                                    "source": {
                                        "namespaceSelector": "projectcalico.org/name == 'alloy'",
                                    },
                                    "destination": {
                                        "ports": [8080],
                                    },
                                },
                            ],
                            "egress": [
                                # Allow access to Kubernetes API server (service CIDR and VPC endpoints)
                                {
                                    "action": "Allow",
                                    "protocol": "TCP",
                                    "destination": {
                                        "nets": ["10.0.0.0/8", "172.16.0.0/12"],
                                        "ports": [443],
                                    },
                                },
                                # Allow access to kube-system for DNS and other cluster services
                                {
                                    "action": "Allow",
                                    "destination": {"namespaceSelector": "projectcalico.org/name == 'kube-system'"},
                                },
                            ],
                        },
                    }
                )
            ],
            opts=pulumi.ResourceOptions(parent=self),
        )
