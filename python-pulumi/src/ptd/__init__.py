from __future__ import annotations

import base64
import dataclasses
import enum
import getpass
import json
import mimetypes
import re
import socket
import typing

import boto3
import yaml

import ptd.aws_accounts
import ptd.paths
import ptd.shext

if typing.TYPE_CHECKING:
    import ipaddress
    import uuid

GROUP_VERSION_KIND_NAMESPACE_NAME = tuple[str, str, str, str]

HELM_CONTROLLER_NAMESPACE = "helm-controller"
IMAGE_OVERRIDE = "{image-override}"
IMAGE_UID_GID = 35559
KARPENTER_NAMESPACE = "kube-system"
KUBE_SYSTEM_NAMESPACE = "kube-system"
LATEST = "latest"
MAIN = "main"
MGMT_AZ_KEY_NAME = "posit-team-dedicated"
MGMT_KMS_KEY_ALIAS = "alias/posit-team-dedicated"
NOTSET = "NOTSET"
POSIT_TEAM_NAMESPACE = "posit-team"
POSIT_TEAM_SYSTEM_NAMESPACE = "posit-team-system"
POSTGRES = "postgres"
PULUMI_CLOUD_BACKEND_URL = "https://app.pulumi.com"
PULUMI_VERSION = "3.96.1"
TRAEFIK_NAMESPACE = "traefik"
ZERO = "0"

AMAZON_ACCOUNT_ID = "137112412989"

POSIT_TEAM_CONTROL_ROOM_EXPECTED_NAMESPACES = {
    "grafana",
    "mimir",
}

EKS_URL_REGEX = re.compile("oidc\\.eks\\..*\\.amazonaws.com/id/[A-Z0-9]+$")
OIDC_ARN_URL_REGEX = re.compile("arn:aws:iam::[0-9]+:oidc-provider/(.*)")


class DynamicRoles:
    @staticmethod
    def aws_lbc_name_env(workload_name: str, environ: str) -> str:
        return f"aws-load-balancer-controller.{workload_name}-{environ}.posit.team"


class ClusterDomainSource(enum.StrEnum):
    LABEL = "LABEL"
    ANNOTATION_JSON = "ANNOTATION_JSON"


class Roles(enum.StrEnum):
    ALLOY = "alloy.posit.team"
    AWS_EBS_CSI_DRIVER = "aws-ebs-csi-driver.posit.team"
    AWS_FSX_OPENZFS_CSI_DRIVER = "aws-fsx-openzfs-csi-driver.posit.team"
    AWS_LOAD_BALANCER_CONTROLLER = "aws-load-balancer-controller.posit.team"
    BASTION = "bastion.posit.team"
    GRAFANA_AGENT = "grafana-agent.posit.team"
    LOKI = "loki.posit.team"
    MIMIR = "mimir.posit.team"
    POSIT_TEAM_ADMIN = "admin.posit.team"
    POSIT_TEAM_CONTROL_ROOM = "ctrl.posit.team"
    EXTERNAL_DNS = "external-dns.posit.team"
    TRAEFIK_FORWARD_AUTH = "traefik-forward-auth.posit.team"


class SecurityGroupPrefixes(enum.StrEnum):
    EKS_NODES_FSX_NFS = "eks-nodes-fsx-nfs.posit.team"
    EKS_NODES_EFS_NFS = "eks-nodes-efs-nfs.posit.team"


class TagKeys(enum.StrEnum):
    POSIT_TEAM_ENVIRONMENT = "posit.team/environment"
    POSIT_TEAM_MANAGED_BY = "posit.team/managed-by"
    POSIT_TEAM_NETWORK_ACCESS = "posit.team/network-access"
    POSIT_TEAM_SITE_NAME = "posit.team/site-name"
    POSIT_TEAM_TRUE_NAME = "posit.team/true-name"
    RS_ENVIRONMENT = "rs:environment"
    RS_OWNER = "rs:owner"
    RS_PROJECT = "rs:project"


def azure_tag_key_format(tag_key: str) -> str:
    return tag_key.replace("/", ":")


class Environments(enum.StrEnum):
    development = "development"
    staging = "staging"
    production = "production"
    validation = "validation"


class TagsRSEnvironment(enum.StrEnum):
    infrastructure = "infrastructure"
    qa = "qa"
    production = "production"


class Tags(enum.StrEnum):
    RS_PROJECT = "team-dedicated"
    RS_OWNER = "ptd@posit.co"


class NetworkTrust(enum.IntFlag):
    ZERO = 0
    SAMESITE = 50
    FULL = 100


class AWSSessionCredentials(typing.TypedDict):
    AccessKeyId: str
    SecretAccessKey: str
    SessionToken: str
    Expiration: str


class AWSSessionAssumedRoleUser(typing.TypedDict):
    AssumedRoleId: str
    Arn: str


class AWSSession(typing.TypedDict):
    Credentials: AWSSessionCredentials
    AssumedRoleUser: AWSSessionAssumedRoleUser


class AWSCallerIdentity(typing.TypedDict):
    UserId: str
    Account: str
    Arn: str


class AzureAuthenticatedUser(typing.TypedDict):
    userPrincipalName: str


def aws_whoami(exe_env: dict[str, str] | None = None) -> tuple[AWSCallerIdentity, bool]:
    session = boto3.Session(
        aws_access_key_id=exe_env.get("AWS_ACCESS_KEY_ID") if exe_env else None,
        aws_secret_access_key=exe_env.get("AWS_SECRET_ACCESS_KEY") if exe_env else None,
        aws_session_token=exe_env.get("AWS_SESSION_TOKEN") if exe_env else None,
    )
    sts_client = session.client("sts")

    try:
        response = sts_client.get_caller_identity()
    except Exception:
        return typing.cast(AWSCallerIdentity, {}), False
    else:
        return response, True


def az_whoami(exe_env: dict[str, str] | None = None) -> tuple[str, bool]:
    ret = ptd.shext.sh(
        [
            "az",
            "ad",
            "signed-in-user",
            "show",
            "--output",
            "json",
        ],
        env=exe_env,
        check=False,
    )
    if ret.returncode != 0:
        return typing.cast(AzureAuthenticatedUser, {}), False

    return json.loads(ret.stdout), True


def aws_current_account_id(exe_env: dict[str, str] | None = None) -> str:
    account_id = ptd.aws_accounts.aws_current_account_id(exe_env)

    if account_id != "":
        return account_id

    awh, ok = aws_whoami(exe_env=exe_env)
    if ok:
        return awh["Account"]

    return ""


class AWSCloudFormationParameter(typing.TypedDict):
    Description: str
    Type: str
    AllowedPattern: str


class AWSCloudFormationResource(typing.TypedDict):
    Type: str
    Properties: dict[str, typing.Any]


class AWSCloudFormationTemplate(typing.TypedDict):
    AWSTemplateFormatVersion: str
    Description: str
    Parameters: dict[str, AWSCloudFormationParameter]
    Resources: dict[str, AWSCloudFormationResource]


class AWSCloudFormationParametersInput(typing.TypedDict):
    ParameterKey: str
    ParameterValue: str


class AWSRoute53HostedZone(typing.TypedDict):
    HostedZone: AWSRoute53HostedZoneDescription
    DelegationSet: AWSRoute53HostedZoneDelegationSet


class AWSRoute53HostedZoneDescription(typing.TypedDict):
    Id: str
    Name: str
    CallerReference: str
    Config: dict[str, str]
    ResourceRecordSetCount: int


class AWSRoute53HostedZoneDelegationSet(typing.TypedDict):
    NameServers: list[str]


class SiteSpecComponentDict(typing.TypedDict):
    image: str


class SiteSpecChronicleDict(SiteSpecComponentDict):
    agentImage: str


class SiteSpecDict(typing.TypedDict):
    chronicle: SiteSpecChronicleDict
    connect: SiteSpecComponentDict
    packageManager: SiteSpecComponentDict
    workbench: SiteSpecComponentDict


class SiteDict(typing.TypedDict):
    apiVersion: str
    spec: SiteSpecDict


def default_site_dict() -> SiteDict:
    return {
        "apiVersion": "v1beta1",
        "spec": {
            "chronicle": {
                "image": "ptd-chronicle:latest",
                "agentImage": "ptd-chronicle-agent:latest",
            },
            "connect": {"image": "ptd-connect:latest"},
            "packageManager": {"image": "ptd-package-manager:latest"},
            "workbench": {"image": "ptd-rstudio-pro:latest"},
            "flightdeck": {"image": "ptd-flightdeck:latest"},
        },
    }


class ComponentImages(enum.StrEnum):
    """PTD component images hosted on public Docker Hub (docker.io/posit/*)."""

    TEAM_OPERATOR = "ptd-team-operator"
    FLIGHTDECK = "ptd-flightdeck"


class ComponentNames(enum.StrEnum):
    CHRONICLE = "chronicle"
    CHRONICLE_AGENT = "chronicleAgent"
    CONNECT = "connect"
    FLIGHTDECK = "flightdeck"
    PACKAGE_MANAGER = "packageManager"
    TEAM_OPERATOR = "team-operator"
    WORKBENCH = "workbench"


class CloudProvider(enum.StrEnum):
    AWS = "aws"
    AZURE = "azure"


@dataclasses.dataclass(frozen=True)
class SiteConfig:
    domain: str
    domain_type: str = ""
    use_traefik_forward_auth: bool = False


@dataclasses.dataclass(frozen=True)
class WorkloadConfig:
    clusters: dict[str, WorkloadClusterConfig]
    region: str
    control_room_account_id: str
    control_room_cluster_name: str
    control_room_domain: str
    control_room_region: str
    control_room_role_name: str | None
    control_room_state_bucket: str | None
    environment: str
    network_trust: NetworkTrust
    sites: typing.Mapping[str, SiteConfig]
    true_name: str

    @property
    def domain(self) -> str:
        return self.sites[MAIN].domain

    @property
    def domains(self) -> list[str]:
        return [site.domain for site in self.sites.values()]


@dataclasses.dataclass(frozen=True)
class WorkloadClusterComponentConfig:
    alloy_version: str | None = "0.12.6"
    external_dns_version: str | None = "1.14.4"
    grafana_version: str | None = "7.0.14"
    kube_state_metrics_version: str | None = "5.30.1"
    loki_version: str | None = "5.42.0"
    loki_replicas: int = 2
    metrics_server_version: str | None = "3.11.0"
    mimir_version: str | None = "5.2.1"
    mimir_replicas: int = 2
    secret_store_csi_driver_version: str | None = "1.3.4"  # noqa: S105
    tigera_operator_version: str | None = "3.26.1"
    traefik_forward_auth_version: str | None = None
    traefik_version: str | None = "37.1.2"


@dataclasses.dataclass(frozen=True)
class NodeGroupConfig:
    instance_type: str = "t3.large"
    min_size: int = 1
    max_size: int = 1
    additional_security_group_ids: list[str] = dataclasses.field(default_factory=list)
    additional_root_disk_size: int = 200  # Root disk size for additional node groups in GB
    taints: list[Taint] = dataclasses.field(default_factory=list)
    labels: dict[str, str] = dataclasses.field(default_factory=dict)
    ami_type: str | None = None  # If None, will use cluster default
    desired_size: int | None = None  # If None, will use min_size


@dataclasses.dataclass(frozen=True)
class Taint:
    # Valid effects are NoSchedule, PreferNoSchedule and NoExecute.
    effect: str
    key: str
    value: str = ""


@dataclasses.dataclass(frozen=True)
class EKSAccessEntriesConfig:
    """Configuration for EKS Access Entries."""

    enabled: bool = True  # Whether to use EKS Access Entries instead of aws-auth ConfigMap
    additional_entries: list[dict] = dataclasses.field(default_factory=list)  # Additional access entries to create
    include_same_account_poweruser: bool = False  # Whether to include PowerUser role from the same account


@dataclasses.dataclass(frozen=True)
class EFSConfig:
    """Configuration for Amazon EFS integration."""

    file_system_id: str
    access_point_id: str | None = None
    mount_targets_managed: bool = True  # False for BYO-EFS scenarios where mount targets are in a different VPC

    def __post_init__(self):
        """Validate EFS resource IDs format."""
        if not self.file_system_id.startswith("fs-"):
            msg = (
                f"Invalid EFS file system ID: '{self.file_system_id}'. "
                f"Must start with 'fs-' (e.g., 'fs-0123456789abcdef0')"
            )
            raise ValueError(msg)
        if self.access_point_id is not None and not self.access_point_id.startswith("fsap-"):
            msg = (
                f"Invalid EFS access point ID: '{self.access_point_id}'. "
                f"Must start with 'fsap-' (e.g., 'fsap-0123456789abcdef0')"
            )
            raise ValueError(msg)


@dataclasses.dataclass(frozen=True)
class Toleration:
    """Kubernetes toleration for scheduling pods on tainted nodes."""

    key: str
    operator: str = "Exists"
    effect: str = "NoSchedule"
    value: str | None = None


@dataclasses.dataclass(frozen=True)
class WorkloadClusterConfig:
    team_operator_image: str = "latest"
    # Overrides team_operator_image when set. Can be a tag (e.g., "test", "dev")
    # or a full image reference. For adhoc images from posit-dev/team-operator PRs:
    #   ghcr.io/posit-dev/team-operator:adhoc-{branch}-{version}
    adhoc_team_operator_image: str | None = None
    # Helm chart version for team-operator (None = latest from OCI registry)
    team_operator_chart_version: str | None = None
    ptd_controller_image: str = "latest"
    eks_access_entries: EKSAccessEntriesConfig = dataclasses.field(default_factory=EKSAccessEntriesConfig)
    custom_k8s_resources: list[str] | None = None  # List of subfolder names from custom_k8s_resources/ to apply
    # Tolerations for team-operator pods (controller and migration job)
    team_operator_tolerations: tuple[Toleration, ...] = ()
    # Skip CRD installation during Helm deployment (for safe migration from kustomize).
    # When True, CRDs are not rendered by Helm templates (crd.enable=false) and the
    # Helm release skips the crds/ directory. This allows the migration job to patch
    # existing CRDs with Helm ownership labels without risk of accidental deletion.
    # After migration, set to False to let Helm manage CRDs going forward.
    team_operator_skip_crds: bool = False


def load_workload_cluster_site_dict(
    cluster_site_dict: dict[str, typing.Any],
) -> tuple[SiteConfig | None, bool]:
    site_spec = cluster_site_dict.get("spec", {})
    for key in list(site_spec.keys()):
        site_spec[key.replace("-", "_")] = site_spec.pop(key)

    return SiteConfig(**site_spec), True


@dataclasses.dataclass
class SubnetCIDRBlocks:
    private: tuple[ipaddress.IPv4Network, ...]
    public: tuple[ipaddress.IPv4Network, ...]
    managed: tuple[ipaddress.IPv4Network, ...]

    @classmethod
    def from_cidr_block(cls, cidr_block: ipaddress.IPv4Network) -> SubnetCIDRBlocks:
        """
        Generates a typical set of private, public, and managed service subnets

        Given a single CIDR block which is expected to be the same thing as the VPC CIDR,
        split into 4 evenly-sized subnets. The first 3 of these 4 is assigned as
        the `private` subnets. The fourth one is split again into 4 evenly-sized subnets,
        with the first 3 of these 4 assigned as `public` subnets. The remaining fourth one
        is split again into 4 evenly-sized subnets and assigned as `managed`. In this way,
        a VPC spanning 3 availability zones may have at least one subnet of each type for
        each availability zone.

        An example based on a VPC CIDR of 10.10.0.0/16 may be visualized here:
        https://www.davidc.net/sites/default/subnets/subnets.html?network=10.10.0.0&mask=16&division=19.3d431
        """
        top_level_subnets = list(cidr_block.subnets(2))

        private = top_level_subnets[:3]

        remaining_subnets = list(top_level_subnets[3].subnets(2))

        public = remaining_subnets[:3]
        managed = list(remaining_subnets[3].subnets(2))

        return cls(
            private=tuple(private),
            public=tuple(public),
            managed=tuple(managed),
        )


def aws_env_from_session_credentials(
    credentials: AWSSessionCredentials,
) -> dict[str, str]:
    return {
        "AWS_ACCESS_KEY_ID": credentials["AccessKeyId"],
        "AWS_SECRET_ACCESS_KEY": credentials["SecretAccessKey"],
        "AWS_SESSION_TOKEN": credentials["SessionToken"],
    }


def aws_secret_from_session_credentials(
    name: str,
    credentials: AWSSessionCredentials,
) -> dict[str, typing.Any]:
    return {
        "apiVersion": "v1",
        "kind": "Secret",
        "type": "Opaque",
        "metadata": {
            "name": name,
        },
        "data": {
            key: base64.b64encode(value.encode()).decode()
            for key, value in aws_env_from_session_credentials(credentials).items()
        },
    }


def aws_account_id_from_session(session: AWSSession) -> str:
    return session["AssumedRoleUser"]["Arn"].split(":")[4]


def aws_assume_workload_account_role(
    role_arn: str,
    region: str,
    external_id: str | uuid.UUID | None = None,
    exe_env: dict[str, str] | None = None,
) -> AWSSession:
    hostname = socket.gethostname()
    whoami = getpass.getuser()

    session = boto3.Session(
        aws_access_key_id=exe_env.get("AWS_ACCESS_KEY_ID") if exe_env else None,
        aws_secret_access_key=exe_env.get("AWS_SECRET_ACCESS_KEY") if exe_env else None,
        aws_session_token=exe_env.get("AWS_SESSION_TOKEN") if exe_env else None,
        region_name=region,
    )
    sts_client = session.client("sts")

    assume_role_kwargs = {
        "RoleArn": role_arn,
        "RoleSessionName": f"{whoami}@{hostname}",
    }

    if external_id is not None:
        assume_role_kwargs["ExternalId"] = str(external_id)

    response = sts_client.assume_role(**assume_role_kwargs)

    # Convert boto3 response format to match the expected format
    return {
        "Credentials": {
            "AccessKeyId": response["Credentials"]["AccessKeyId"],
            "SecretAccessKey": response["Credentials"]["SecretAccessKey"],
            "SessionToken": response["Credentials"]["SessionToken"],
            "Expiration": response["Credentials"]["Expiration"].isoformat(),
        },
        "AssumedRoleUser": {
            "AssumedRoleId": response["AssumedRoleUser"]["AssumedRoleId"],
            "Arn": response["AssumedRoleUser"]["Arn"],
        },
    }


def aws_assume_control_room_role(
    account_id: str,
    region: str,
    role_name: str = Roles.POSIT_TEAM_CONTROL_ROOM,
    exe_env: dict[str, str] | None = None,
) -> AWSSession:
    session = boto3.Session(
        aws_access_key_id=exe_env.get("AWS_ACCESS_KEY_ID") if exe_env else None,
        aws_secret_access_key=exe_env.get("AWS_SECRET_ACCESS_KEY") if exe_env else None,
        aws_session_token=exe_env.get("AWS_SESSION_TOKEN") if exe_env else None,
        region_name=region,
    )
    sts_client = session.client("sts")

    response = sts_client.assume_role(
        RoleArn=f"arn:aws:iam::{account_id}:role/{role_name}",
        RoleSessionName=f"{getpass.getuser()}@{socket.gethostname()}",
    )

    # Convert boto3 response format to match the expected format
    return {
        "Credentials": {
            "AccessKeyId": response["Credentials"]["AccessKeyId"],
            "SecretAccessKey": response["Credentials"]["SecretAccessKey"],
            "SessionToken": response["Credentials"]["SessionToken"],
            "Expiration": response["Credentials"]["Expiration"].isoformat(),
        },
        "AssumedRoleUser": {
            "AssumedRoleId": response["AssumedRoleUser"]["AssumedRoleId"],
            "Arn": response["AssumedRoleUser"]["Arn"],
        },
    }


def build_secret(base_name: str, ns: str, site_name: str, managed_account_id: str) -> dict[str, typing.Any]:
    return {
        f"{base_name}-{ns}-{site_name}-secret": {
            "type": "aws:secretsmanager:Secret",
            "properties": {
                "name": f"{base_name}-{ns}-{site_name}",
                "description": f"Secrets for the Site deployment for {base_name}-{site_name}",
                "recoveryWindowInDays": 30,
                "policy": json.dumps(build_secret_store_policy(base_name, ns, site_name, managed_account_id)),
                "tags": {},  # TODO: figure out what tags we want...
            },
        },
    }


def build_secret_store_policy(
    base_name: str, ns: str, site_name: str, managed_account_id: str
) -> dict[str, typing.Any]:
    return {
        "Version": "2012-10-17",
        "Statement": [
            {
                "Effect": "Allow",
                "Principal": {"AWS": f"arn:aws:iam::{managed_account_id}:role/{base_name}-{ns}-{site_name}-pub"},
                "Action": "secretsmanager:GetSecretValue",
                "Resource": "*",
            },
            {
                "Effect": "Allow",
                "Principal": {"AWS": f"arn:aws:iam::{managed_account_id}:role/{base_name}-{ns}-{site_name}-dev"},
                "Action": "secretsmanager:GetSecretValue",
                "Resource": "*",
            },
            {
                "Effect": "Allow",
                "Principal": {"AWS": f"arn:aws:iam::{managed_account_id}:role/{base_name}-{ns}-{site_name}-pkg"},
                "Action": "secretsmanager:GetSecretValue",
                "Resource": "*",
            },
            # TODO: if chronicle needs to read secrets, we will need to add access here
        ],
    }


def aws_ensure_state_bucket(
    state_bucket: str,
    aws_region: str,
    exe_env: dict[str, str] | None = None,
) -> bool:
    session = boto3.Session(
        aws_access_key_id=exe_env.get("AWS_ACCESS_KEY_ID") if exe_env else None,
        aws_secret_access_key=exe_env.get("AWS_SECRET_ACCESS_KEY") if exe_env else None,
        aws_session_token=exe_env.get("AWS_SESSION_TOKEN") if exe_env else None,
        region_name=aws_region,
    )
    s3_client = session.client("s3")

    try:
        # Try to create the bucket
        if aws_region == "us-east-1":
            # us-east-1 doesn't need CreateBucketConfiguration
            s3_client.create_bucket(Bucket=state_bucket, ObjectOwnership="BucketOwnerEnforced")
        else:
            s3_client.create_bucket(
                Bucket=state_bucket,
                CreateBucketConfiguration={"LocationConstraint": aws_region},
                ObjectOwnership="BucketOwnerEnforced",
            )
    except Exception as e:
        # Handle all bucket creation exceptions
        if "BucketAlreadyOwnedByYou" in str(e):
            # Bucket already exists and is owned by us
            return True
        if "BucketAlreadyExists" in str(e):
            # Bucket exists but may not be owned by us - check if we can access it
            try:
                s3_client.get_bucket_location(Bucket=state_bucket)
            except Exception:
                return False
            else:
                return True
        else:
            # Try to check if bucket exists and we can access it
            try:
                s3_client.get_bucket_location(Bucket=state_bucket)
            except Exception:
                print(f"Error ensuring S3 bucket: {e}")
                return False
            else:
                return True
    else:
        return True


def aws_ensure_state_key(exe_env: dict[str, str] | None = None, region: str = "us-east-2") -> bool:
    session = boto3.Session(
        aws_access_key_id=exe_env.get("AWS_ACCESS_KEY_ID") if exe_env else None,
        aws_secret_access_key=exe_env.get("AWS_SECRET_ACCESS_KEY") if exe_env else None,
        aws_session_token=exe_env.get("AWS_SESSION_TOKEN") if exe_env else None,
        region_name=region,
    )
    kms_client = session.client("kms")

    try:
        # Try to describe the existing key
        response = kms_client.describe_key(KeyId=MGMT_KMS_KEY_ALIAS)
        key = response["KeyMetadata"]
    except Exception as e:
        if "NotFoundException" in str(e):
            try:
                # Create a new key
                response = kms_client.create_key()
                key = response["KeyMetadata"]

                # Create alias for the key
                kms_client.create_alias(AliasName=MGMT_KMS_KEY_ALIAS, TargetKeyId=key["KeyId"])
            except Exception as create_e:
                print(f"Error creating KMS key: {create_e}")
                return False
        else:
            print(f"Error describing KMS key: {e}")
            return False

    current_account = aws_current_account_id(exe_env=exe_env)

    try:
        # Put key policy
        policy_document = {
            "Version": "2012-10-17",
            "Statement": [
                {
                    "Sid": "Enable IAM User Permissions",
                    "Effect": "Allow",
                    "Principal": {
                        "AWS": [
                            f"arn:aws:iam::{current_account}:root",
                            f"arn:aws:iam::{current_account}:role/{Roles.POSIT_TEAM_CONTROL_ROOM}",
                        ]
                    },
                    "Action": "kms:*",
                    "Resource": "*",
                },
            ],
        }

        kms_client.put_key_policy(KeyId=key["KeyId"], PolicyName="default", Policy=json.dumps(policy_document))
    except Exception as e:
        print(f"Error putting KMS key policy: {e}")
        return False

    return True


def aws_ensure_bucket_object(
    bucket: str, key: str, content: str, exe_env: dict[str, str] | None = None, region: str = "us-east-2"
) -> bool:
    content_type, content_encoding = mimetypes.guess_type(key)

    if content_type is None:
        content_type = "application/octet-stream"

    session = boto3.Session(
        aws_access_key_id=exe_env.get("AWS_ACCESS_KEY_ID") if exe_env else None,
        aws_secret_access_key=exe_env.get("AWS_SECRET_ACCESS_KEY") if exe_env else None,
        aws_session_token=exe_env.get("AWS_SESSION_TOKEN") if exe_env else None,
        region_name=region,
    )
    s3_client = session.client("s3")

    try:
        extra_args = {"ContentType": content_type}
        if content_encoding is not None:
            extra_args["ContentEncoding"] = content_encoding

        s3_client.put_object(Bucket=bucket, Key=key, Body=content.encode("utf-8"), **extra_args)
    except Exception as e:
        print(f"Error uploading object to S3: {e}")
        return False
    else:
        return True


def aws_presign_bucket_object_url(
    s3_url: str, expires_in: int = 3600, exe_env: dict[str, str] | None = None, region: str = "us-east-2"
) -> tuple[str, bool]:
    # Parse S3 URL (format: s3://bucket/key)
    if not s3_url.startswith("s3://"):
        return "", False

    s3_path = s3_url[5:]  # Remove 's3://' prefix
    if "/" not in s3_path:
        return "", False

    bucket, key = s3_path.split("/", 1)

    session = boto3.Session(
        aws_access_key_id=exe_env.get("AWS_ACCESS_KEY_ID") if exe_env else None,
        aws_secret_access_key=exe_env.get("AWS_SECRET_ACCESS_KEY") if exe_env else None,
        aws_session_token=exe_env.get("AWS_SESSION_TOKEN") if exe_env else None,
        region_name=region,
    )
    s3_client = session.client("s3")

    try:
        response = s3_client.generate_presigned_url(
            "get_object", Params={"Bucket": bucket, "Key": key}, ExpiresIn=expires_in
        )
    except Exception as e:
        print(f"Error generating presigned URL: {e}")
        return "", False
    else:
        return response, True


def aws_cert_id_for_domain(domain: str, exe_env: dict[str, str], region: str = "us-east-2") -> str | None:
    session = boto3.Session(
        aws_access_key_id=exe_env.get("AWS_ACCESS_KEY_ID") if exe_env else None,
        aws_secret_access_key=exe_env.get("AWS_SECRET_ACCESS_KEY") if exe_env else None,
        aws_session_token=exe_env.get("AWS_SESSION_TOKEN") if exe_env else None,
        region_name=region,
    )
    acm_client = session.client("acm")

    try:
        response = acm_client.list_certificates()
    except Exception as e:
        print(f"Error listing ACM certificates: {e}")
        return None
    else:
        for cert in response.get("CertificateSummaryList", []):
            if domain == cert["DomainName"] or domain in cert.get("SubjectAlternativeNameSummaries", []):
                return cert["CertificateArn"].rsplit("/")[-1]
        return None


def aws_vpc_id(
    name: str,
    exe_env: dict[str, str] | None = None,
    region: str = "us-east-2",
) -> str | None:
    vpc = aws_vpc(name, exe_env, region)
    if vpc is None:
        return None

    return vpc.get("VpcId")


def aws_vpc(
    name: str, exe_env: dict[str, str] | None = None, region: str = "us-east-2"
) -> dict[str, typing.Any] | None:
    session = boto3.Session(
        aws_access_key_id=exe_env.get("AWS_ACCESS_KEY_ID") if exe_env else None,
        aws_secret_access_key=exe_env.get("AWS_SECRET_ACCESS_KEY") if exe_env else None,
        aws_session_token=exe_env.get("AWS_SESSION_TOKEN") if exe_env else None,
        region_name=region,
    )
    ec2_client = session.client("ec2")

    try:
        response = ec2_client.describe_vpcs(
            Filters=[
                {"Name": "tag:Name", "Values": [name]},
                {"Name": "tag-key", "Values": [str(TagKeys.POSIT_TEAM_MANAGED_BY)]},
            ]
        )
        vpcs = response.get("Vpcs", [])
        if len(vpcs) == 0:
            return None
        return vpcs[0]
    except Exception as e:
        print(f"Error describing VPCs: {e}")
        return None


def aws_eks_kubeconfig(cluster_name: str, exe_env: dict[str, str] | None = None, region: str = "us-east-2") -> str:
    session = boto3.Session(
        aws_access_key_id=exe_env.get("AWS_ACCESS_KEY_ID") if exe_env else None,
        aws_secret_access_key=exe_env.get("AWS_SECRET_ACCESS_KEY") if exe_env else None,
        aws_session_token=exe_env.get("AWS_SESSION_TOKEN") if exe_env else None,
        region_name=region,
    )
    eks_client = session.client("eks")

    try:
        response = eks_client.describe_cluster(name=cluster_name)
        cluster = response["cluster"]
        endpoint = cluster["endpoint"]
        ca_data = cluster["certificateAuthority"]["data"]

        # Generate the kubeconfig YAML structure
        kubeconfig = {
            "apiVersion": "v1",
            "clusters": [
                {"cluster": {"certificate-authority-data": ca_data, "server": endpoint}, "name": cluster_name}
            ],
            "contexts": [{"context": {"cluster": cluster_name, "user": cluster_name}, "name": cluster_name}],
            "current-context": cluster_name,
            "kind": "Config",
            "preferences": {},
            "users": [
                {
                    "name": cluster_name,
                    "user": {
                        "exec": {
                            "apiVersion": "client.authentication.k8s.io/v1beta1",
                            "args": ["--region", region, "eks", "get-token", "--cluster-name", cluster_name],
                            "command": "aws",
                            "env": None,
                            "provideClusterInfo": False,
                        }
                    },
                }
            ],
        }

        return yaml.dump(kubeconfig, default_flow_style=False)
    except Exception as e:
        print(f"Error generating EKS kubeconfig: {e}")
        return ""


def aws_eks_clusters(
    compound_name: str, exe_env: dict[str, str] | None = None, region: str = "us-east-2"
) -> list[dict[str, typing.Any]]:
    session = boto3.Session(
        aws_access_key_id=exe_env.get("AWS_ACCESS_KEY_ID") if exe_env else None,
        aws_secret_access_key=exe_env.get("AWS_SECRET_ACCESS_KEY") if exe_env else None,
        aws_session_token=exe_env.get("AWS_SESSION_TOKEN") if exe_env else None,
        region_name=region,
    )
    tag_client = session.client("resourcegroupstaggingapi")
    eks_client = session.client("eks")

    # fetch all clusters with a 'posit.team/managed-by' tag
    res = tag_client.get_resources(
        ResourceTypeFilters=["eks:cluster"],
        TagFilters=[
            {
                "Key": TagKeys.POSIT_TEAM_MANAGED_BY,
            },
        ],
    )
    names: list[str] = [
        resource["ResourceARN"].split("/")[-1]  # preserve the cluster name portion of the arn
        for resource in res.get("ResourceTagMappingList", [])
    ]

    # Use boto3 instead of shelling out
    clusters = []
    for cluster_name in names:
        if compound_name in cluster_name:
            try:
                response = eks_client.describe_cluster(name=cluster_name)
                clusters.append(response)
            except Exception as e:
                print(f"Error describing cluster {cluster_name}: {e}")
                continue

    return clusters


def aws_subnets_for_vpc(
    name: str,
    region: str,
    network_access: str = "private",
    tag_filters: list[dict[str, typing.Any]] | None = None,
    exe_env: dict[str, str] | None = None,
    vpc_id: str | None = None,
) -> list[dict[str, typing.Any]]:
    if vpc_id is None:
        vpc_id = aws_vpc_id(name, exe_env=exe_env, region=region)
        if vpc_id is None:
            return []

    ec2_client = boto3.client("ec2", region_name=region)

    filters = [
        {"Name": "vpc-id", "Values": [vpc_id]},
    ]

    if tag_filters:
        filters.extend(tag_filters)
    else:
        filters.append({"Name": "tag:Name", "Values": [f"{name}-*"]})
        filters.append({"Name": "tag-key", "Values": [str(TagKeys.POSIT_TEAM_MANAGED_BY)]})
        filters.append({"Name": f"tag:{TagKeys.POSIT_TEAM_NETWORK_ACCESS}", "Values": [network_access]})

    response = ec2_client.describe_subnets(Filters=filters)

    return [subnet for subnet in response.get("Subnets", []) if subnet.get("SubnetId") is not None]


def aws_route_tables_for_vpc(
    name: str,
    network_access: str = "private",
    exe_env: dict[str, str] | None = None,
    region: str = "us-east-2",
) -> list[dict[str, typing.Any]]:
    session = boto3.Session(
        aws_access_key_id=exe_env.get("AWS_ACCESS_KEY_ID") if exe_env else None,
        aws_secret_access_key=exe_env.get("AWS_SECRET_ACCESS_KEY") if exe_env else None,
        aws_session_token=exe_env.get("AWS_SESSION_TOKEN") if exe_env else None,
        region_name=region,
    )
    ec2_client = session.client("ec2")

    try:
        response = ec2_client.describe_route_tables(
            Filters=[
                {"Name": "tag:Name", "Values": [f"{name}-*"]},
                {"Name": "tag-key", "Values": [str(TagKeys.POSIT_TEAM_MANAGED_BY)]},
                {"Name": f"tag:{TagKeys.POSIT_TEAM_NETWORK_ACCESS}", "Values": [network_access]},
            ]
        )

        route_tables = response.get("RouteTables", [])
        return [rt for rt in route_tables if rt.get("RouteTableId") is not None]
    except Exception as e:
        print(f"Error describing route tables: {e}")
        return []


def _aws_nfs_sg_id_by_prefix(
    vpc_id: str,
    sg_prefix: SecurityGroupPrefixes,
    exe_env: dict[str, str] | None = None,
    region: str = "us-east-2",
) -> tuple[str, bool]:
    """
    Find a security group by name prefix in a VPC.

    Searches for the first security group in the specified VPC whose name starts
    with the given prefix. This is used to locate pre-created security groups for
    NFS access (FSX or EFS).

    :param vpc_id: VPC ID to search in (e.g., 'vpc-xxxxx')
    :param sg_prefix: Security group name prefix to match (SecurityGroupPrefixes enum)
    :param exe_env: Optional AWS credentials dict with AWS_ACCESS_KEY_ID and
                    AWS_SECRET_ACCESS_KEY. If None, uses default credential chain.
    :param region: AWS region. Default: 'us-east-2'
    :return: A tuple of (security_group_id, found).
             - If found: (str, True) where str is the security group ID
             - If not found or error: ("", False)

    Example:
        sg_id, found = _aws_nfs_sg_id_by_prefix("vpc-123", SecurityGroupPrefixes.EKS_NODES_EFS_NFS)
        if found:
            print(f"Found security group: {sg_id}")
    """
    session = boto3.Session(
        aws_access_key_id=exe_env.get("AWS_ACCESS_KEY_ID") if exe_env else None,
        aws_secret_access_key=exe_env.get("AWS_SECRET_ACCESS_KEY") if exe_env else None,
        aws_session_token=exe_env.get("AWS_SESSION_TOKEN") if exe_env else None,
        region_name=region,
    )
    ec2_client = session.client("ec2")

    try:
        response = ec2_client.describe_security_groups(
            Filters=[
                {"Name": "vpc-id", "Values": [vpc_id]},
            ]
        )
    except Exception as e:
        print(f"Error describing security groups in VPC {vpc_id}: {e}")
        return "", False

    for sg in response.get("SecurityGroups", []):
        if sg.get("GroupName", "").startswith(str(sg_prefix)):
            sg_id = sg.get("GroupId", "").strip()
            return sg_id, sg_id != ""

    return "", False


def aws_fsx_nfs_sg_id(
    vpc_id: str, exe_env: dict[str, str] | None = None, region: str = "us-east-2"
) -> tuple[str, bool]:
    """Find FSX NFS security group ID in a VPC."""
    return _aws_nfs_sg_id_by_prefix(vpc_id, SecurityGroupPrefixes.EKS_NODES_FSX_NFS, exe_env, region)


def aws_efs_nfs_sg_id(
    vpc_id: str, exe_env: dict[str, str] | None = None, region: str = "us-east-2"
) -> tuple[str, bool]:
    """Find EFS NFS security group ID in a VPC."""
    return _aws_nfs_sg_id_by_prefix(vpc_id, SecurityGroupPrefixes.EKS_NODES_EFS_NFS, exe_env, region)


def get_oidc_url(cluster: dict[str, typing.Any]) -> str:
    return cluster["cluster"].get("identity", {}).get("oidc", {}).get("issuer")


def aws_route53_dns_update_policy(hosted_zone_ref: str):
    return {
        "Version": "2012-10-17",
        "Statement": [
            {
                "Effect": "Allow",
                "Action": ["route53:ChangeResourceRecordSets"],
                "Resource": [hosted_zone_ref],
            },
            {
                "Effect": "Allow",
                "Action": [
                    "route53:ListHostedZones",
                    "route53:ListResourceRecordSets",
                    "route53:ListTagsForResource",
                ],
                "Resource": ["*"],
            },
        ],
    }


def aws_route53_get_hosted_zone(
    zone_id: str, exe_env: dict[str, str] | None = None, region: str = "us-east-2"
) -> tuple[AWSRoute53HostedZone, bool]:
    session = boto3.Session(
        aws_access_key_id=exe_env.get("AWS_ACCESS_KEY_ID") if exe_env else None,
        aws_secret_access_key=exe_env.get("AWS_SECRET_ACCESS_KEY") if exe_env else None,
        aws_session_token=exe_env.get("AWS_SESSION_TOKEN") if exe_env else None,
        region_name=region,
    )
    route53_client = session.client("route53")

    try:
        response = route53_client.get_hosted_zone(Id=zone_id)
    except Exception as e:
        print(f"Error getting Route53 hosted zone: {e}")
        return typing.cast(AWSRoute53HostedZone, {}), False
    else:
        return response, True


def aws_traefik_forward_auth_secrets_policy(
    region: str,
    account_id: str,
) -> dict[str, typing.Any]:
    return {
        "Version": "2012-10-17",
        "Statement": [
            {
                "Effect": "Allow",
                "Action": [
                    "secretsmanager:GetSecretValue",
                    "secretsmanager:DescribeSecret",
                ],
                "Resource": [
                    f"arn:aws:secretsmanager:{region}:{account_id}:secret:okta-oidc-client-creds-*",
                    f"arn:aws:secretsmanager:{region}:{account_id}:secret:okta-oidc-client-creds.*.posit.team",
                    f"arn:aws:secretsmanager:{region}:{account_id}:secret:okta-oidc-client-creds.*.posit.team*",
                ],
            },
        ],
    }


def aws_rds_describe_db_instance(
    instance_identifier: str, exe_env: dict[str, str] | None = None, region: str = "us-east-2"
) -> dict[str, typing.Any]:
    session = boto3.Session(
        aws_access_key_id=exe_env.get("AWS_ACCESS_KEY_ID") if exe_env else None,
        aws_secret_access_key=exe_env.get("AWS_SECRET_ACCESS_KEY") if exe_env else None,
        aws_session_token=exe_env.get("AWS_SESSION_TOKEN") if exe_env else None,
        region_name=region,
    )
    rds_client = session.client("rds")

    try:
        response = rds_client.describe_db_instances(DBInstanceIdentifier=instance_identifier)
        return response.get("DBInstances", [])[0]
    except Exception as e:
        print(f"Error describing RDS instance: {e}")
        return {}


def aws_eks_cluster_oidc_issuer_url(
    cluster_name: str,
    exe_env: dict[str, str] | None = None,
    region: str = "us-east-2",
) -> tuple[str, bool]:
    session = boto3.Session(
        aws_access_key_id=exe_env.get("AWS_ACCESS_KEY_ID") if exe_env else None,
        aws_secret_access_key=exe_env.get("AWS_SECRET_ACCESS_KEY") if exe_env else None,
        aws_session_token=exe_env.get("AWS_SESSION_TOKEN") if exe_env else None,
        region_name=region,
    )
    eks_client = session.client("eks")

    try:
        response = eks_client.describe_cluster(name=cluster_name)
        cluster = response.get("cluster", {})
        issuer_url = cluster.get("identity", {}).get("oidc", {}).get("issuer", "")
        return issuer_url, issuer_url.strip() != ""
    except Exception as e:
        print(f"Error describing EKS cluster: {e}")
        return "", False


def mailgun_get_dkim_key(
    api_key: str,
    signing_domain: str,
    base_url: str = "https://api.mailgun.net",
) -> tuple[dict[str, typing.Any], bool]:
    # NOTE: as described here https://documentation.mailgun.com/docs/mailgun/api-reference/openapi-final/tag/Domain-Keys/
    import requests

    resp = requests.get(
        f"{base_url}/v1/dkim/keys",
        headers={"Accept": "application/json"},
        auth=("api", api_key),
        timeout=(5, 30),
        params={
            "signing_domain": signing_domain,
            "limit": "1",
        },
    )

    if not resp.ok:
        return {}, False

    data = resp.json()

    if "items" not in data or len(data["items"]) == 0:
        return {}, False

    return data["items"][0], True


def get_region_from_workload_config(
    workload_config: WorkloadConfig | None = None, default_region: str = "us-east-2"
) -> str:
    """
    Get AWS region from workload configuration, falling back to default if not available.

    Args:
        workload_config: Optional workload configuration containing region info
        default_region: Default region to use if workload config is not provided

    Returns:
        AWS region string
    """
    if workload_config is not None:
        return workload_config.region
    return default_region


def define_component_image(
    image_config: str,
    component_image: ComponentImages,
    image_registry_hostname: str = "docker.io/posit",
) -> str:
    """
    Define a component image based on configuration.

    Images are pulled from public Docker Hub (docker.io/posit/*).

    Args:
        image_config: Image configuration string. Can be:
            - A tag (e.g., "latest", "v1.2.3", "test", "dev")
            - A full image reference (e.g., "myregistry.io/custom-image:v1")
        component_image: The ComponentImages enum value for the component
        image_registry_hostname: The image registry hostname (default: "docker.io/posit")

    Returns:
        The fully qualified image string with tag

    Examples:
        >>> define_component_image("latest", ComponentImages.TEAM_OPERATOR)
        "docker.io/posit/ptd-team-operator:latest"

        >>> define_component_image("v1.2.3", ComponentImages.TEAM_OPERATOR)
        "docker.io/posit/ptd-team-operator:v1.2.3"

        >>> define_component_image("test", ComponentImages.TEAM_OPERATOR)
        "docker.io/posit/ptd-team-operator:test"

        >>> define_component_image("myregistry.io/custom:v1", ComponentImages.TEAM_OPERATOR)
        "myregistry.io/custom:v1"  # Pass-through for custom images
    """
    # If the image_config contains a "/" it's likely a full image reference
    # Pass it through as-is (allows using custom registries/images)
    if "/" in image_config:
        return image_config

    # Otherwise, treat it as a tag and construct the full image reference
    tag = image_config if image_config else "latest"
    return f"{image_registry_hostname}/{component_image}:{tag}"
