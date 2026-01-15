import ipaddress
from unittest.mock import MagicMock, patch

import ptd


class TestConstants:
    def test_constants_exist(self):
        """Test that all expected constants are defined."""
        assert ptd.HELM_CONTROLLER_NAMESPACE == "helm-controller"
        assert ptd.IMAGE_UID_GID == 35559
        assert ptd.KUBE_SYSTEM_NAMESPACE == "kube-system"
        assert ptd.LATEST == "latest"
        assert ptd.MAIN == "main"
        assert ptd.NOTSET == "NOTSET"
        assert ptd.ZERO == "0"


class TestEnums:
    def test_dynamic_roles(self):
        """Test DynamicRoles static methods."""
        result = ptd.DynamicRoles.aws_lbc_name_env("workload", "staging")
        assert result == "aws-load-balancer-controller.workload-staging.posit.team"

    def test_cluster_domain_source(self):
        """Test ClusterDomainSource enum values."""
        assert ptd.ClusterDomainSource.LABEL == "LABEL"
        assert ptd.ClusterDomainSource.ANNOTATION_JSON == "ANNOTATION_JSON"

    def test_roles_enum(self):
        """Test Roles enum values."""
        assert ptd.Roles.AWS_EBS_CSI_DRIVER == "aws-ebs-csi-driver.posit.team"
        assert ptd.Roles.POSIT_TEAM_ADMIN == "admin.posit.team"

    def test_component_images_values(self):
        """Test ComponentImages enum values."""
        assert ptd.ComponentImages.TEAM_OPERATOR == "ptd-team-operator"
        assert ptd.ComponentImages.FLIGHTDECK == "ptd-flightdeck"

    def test_network_trust_flags(self):
        """Test NetworkTrust flag operations."""
        assert ptd.NetworkTrust.ZERO == 0
        assert ptd.NetworkTrust.SAMESITE == 50
        assert ptd.NetworkTrust.FULL == 100
        assert ptd.NetworkTrust.SAMESITE | ptd.NetworkTrust.FULL == 118


class TestUtilityFunctions:
    def test_azure_tag_key_format(self):
        """Test azure_tag_key_format function."""
        assert ptd.azure_tag_key_format("posit.team/environment") == "posit.team:environment"
        assert ptd.azure_tag_key_format("rs/project") == "rs:project"

    def test_default_site_dict(self):
        """Test default_site_dict function."""
        site_dict = ptd.default_site_dict()
        assert site_dict["apiVersion"] == "v1beta1"
        assert "spec" in site_dict
        assert "chronicle" in site_dict["spec"]
        assert site_dict["spec"]["chronicle"]["image"] == "ptd-chronicle:latest"


class TestDataClasses:
    def test_site_config(self):
        """Test SiteConfig dataclass."""
        site_config = ptd.SiteConfig(domain="example.com", domain_type="external", use_traefik_forward_auth=True)
        assert site_config.domain == "example.com"
        assert site_config.domain_type == "external"
        assert site_config.use_traefik_forward_auth is True

    def test_workload_config_properties(self):
        """Test WorkloadConfig properties."""
        site_config = ptd.SiteConfig(domain="example.com")
        workload_config = ptd.WorkloadConfig(
            clusters={},
            control_room_account_id="123456789012",
            control_room_cluster_name="test-cluster",
            control_room_domain="control.example.com",
            control_room_region="us-east-1",
            control_room_role_name=None,
            control_room_state_bucket=None,
            environment="staging",
            network_trust=ptd.NetworkTrust.FULL,
            region="us-east-1",
            sites={ptd.MAIN: site_config, "other": ptd.SiteConfig(domain="other.com")},
            true_name="test-workload",
        )

        assert workload_config.domain == "example.com"
        assert workload_config.domains == ["example.com", "other.com"]

    def test_subnet_cidr_blocks_from_cidr_block(self):
        """Test SubnetCIDRBlocks.from_cidr_block method."""
        vpc_cidr = ipaddress.IPv4Network("10.10.0.0/16")
        subnets = ptd.SubnetCIDRBlocks.from_cidr_block(vpc_cidr)

        assert len(subnets.private) == 3
        assert len(subnets.public) == 3
        assert len(subnets.managed) == 4

        # Check that subnets are properly sized
        assert subnets.private[0].prefixlen == 18  # /16 -> /18 (4 subnets)
        assert subnets.public[0].prefixlen == 20  # /18 -> /20 (4 subnets)
        assert subnets.managed[0].prefixlen == 22  # /20 -> /22 (4 subnets)

    def test_taint_dataclass(self):
        """Test Taint dataclass."""
        taint = ptd.Taint(effect="NoSchedule", key="test-key", value="test-value")
        assert taint.effect == "NoSchedule"
        assert taint.key == "test-key"
        assert taint.value == "test-value"

    def test_node_group_config_defaults(self):
        """Test NodeGroupConfig default values."""
        config = ptd.NodeGroupConfig()
        assert config.instance_type == "t3.large"
        assert config.min_size == 1
        assert config.max_size == 1
        assert config.additional_root_disk_size == 200
        assert config.additional_security_group_ids == []
        assert config.taints == []
        assert config.labels == {}


class TestAWSFunctions:
    @patch("boto3.Session")
    def test_aws_whoami_success(self, mock_session):
        """Test aws_whoami with successful response."""
        mock_sts_client = MagicMock()
        mock_sts_client.get_caller_identity.return_value = {
            "UserId": "test-user",
            "Account": "123456789012",
            "Arn": "arn:aws:iam::123456789012:user/test-user",
        }
        mock_session.return_value.client.return_value = mock_sts_client

        identity, success = ptd.aws_whoami()

        assert success is True
        assert identity["Account"] == "123456789012"
        assert identity["UserId"] == "test-user"

    @patch("boto3.Session")
    def test_aws_whoami_failure(self, mock_session):
        """Test aws_whoami with failure."""
        mock_sts_client = MagicMock()
        mock_sts_client.get_caller_identity.side_effect = Exception("AWS error")
        mock_session.return_value.client.return_value = mock_sts_client

        identity, success = ptd.aws_whoami()

        assert success is False
        assert identity == {}

    @patch("ptd.aws_accounts.aws_current_account_id")
    @patch("ptd.aws_whoami")
    def test_aws_current_account_id(self, mock_whoami, mock_aws_accounts):
        """Test aws_current_account_id function."""
        # Test when aws_accounts returns account ID
        mock_aws_accounts.return_value = "123456789012"
        result = ptd.aws_current_account_id()
        assert result == "123456789012"

        # Test when aws_accounts returns empty but whoami succeeds
        mock_aws_accounts.return_value = ""
        mock_whoami.return_value = ({"Account": "987654321098"}, True)
        result = ptd.aws_current_account_id()
        assert result == "987654321098"

        # Test when both fail
        mock_aws_accounts.return_value = ""
        mock_whoami.return_value = ({}, False)
        result = ptd.aws_current_account_id()
        assert result == ""

    def test_aws_account_id_from_session(self):
        """Test aws_account_id_from_session function."""
        session = {"AssumedRoleUser": {"Arn": "arn:aws:sts::123456789012:assumed-role/test-role/test-session"}}

        account_id = ptd.aws_account_id_from_session(session)
        assert account_id == "123456789012"

    @patch("boto3.Session")
    def test_aws_ensure_state_bucket_success(self, mock_session):
        """Test aws_ensure_state_bucket with successful creation."""
        mock_s3_client = MagicMock()
        mock_s3_client.create_bucket.return_value = {}
        mock_session.return_value.client.return_value = mock_s3_client

        result = ptd.aws_ensure_state_bucket("test-bucket", "us-east-1")

        assert result is True
        mock_s3_client.create_bucket.assert_called_once()

    @patch("boto3.Session")
    def test_aws_ensure_state_bucket_already_exists(self, mock_session):
        """Test aws_ensure_state_bucket when bucket already exists."""
        mock_s3_client = MagicMock()
        mock_s3_client.create_bucket.side_effect = mock_s3_client.exceptions.BucketAlreadyOwnedByYou()
        mock_session.return_value.client.return_value = mock_s3_client

        result = ptd.aws_ensure_state_bucket("test-bucket", "us-east-1")

        assert result is True

    @patch("ptd.shext.sh")
    def test_az_whoami_success(self, mock_sh):
        """Test az_whoami with successful response."""
        mock_sh.return_value.returncode = 0
        mock_sh.return_value.stdout = '{"userPrincipalName": "test@example.com"}'

        result, success = ptd.az_whoami()

        assert success is True
        assert result["userPrincipalName"] == "test@example.com"

    @patch("ptd.shext.sh")
    def test_az_whoami_failure(self, mock_sh):
        """Test az_whoami with failure."""
        mock_sh.return_value.returncode = 1

        result, success = ptd.az_whoami()

        assert success is False

    def test_build_secret_store_policy(self):
        """Test build_secret_store_policy function."""
        policy = ptd.build_secret_store_policy("test-base", "test-ns", "test-site", "123456789012")

        assert policy["Version"] == "2012-10-17"
        assert len(policy["Statement"]) == 3

        # Check that all expected principals are present
        principals = [stmt["Principal"]["AWS"] for stmt in policy["Statement"]]
        assert "arn:aws:iam::123456789012:role/test-base-test-ns-test-site-pub" in principals
        assert "arn:aws:iam::123456789012:role/test-base-test-ns-test-site-dev" in principals
        assert "arn:aws:iam::123456789012:role/test-base-test-ns-test-site-pkg" in principals

    def test_build_secret(self):
        """Test build_secret function."""
        secret = ptd.build_secret("test-base", "test-ns", "test-site", "123456789012")

        secret_key = "test-base-test-ns-test-site-secret"
        assert secret_key in secret
        assert secret[secret_key]["type"] == "aws:secretsmanager:Secret"
        assert secret[secret_key]["properties"]["name"] == "test-base-test-ns-test-site"

    def test_load_workload_cluster_site_dict(self):
        """Test load_workload_cluster_site_dict function."""
        cluster_site_dict = {
            "spec": {"domain": "example.com", "domain-type": "external", "use-traefik-forward-auth": True}
        }

        site_config, success = ptd.load_workload_cluster_site_dict(cluster_site_dict)

        assert success is True
        assert site_config.domain == "example.com"
        assert site_config.domain_type == "external"
        assert site_config.use_traefik_forward_auth is True

    @patch("boto3.Session")
    def test_aws_presign_bucket_object_url(self, mock_session):
        """Test aws_presign_bucket_object_url function."""
        mock_s3_client = MagicMock()
        mock_s3_client.generate_presigned_url.return_value = "https://presigned-url.com"
        mock_session.return_value.client.return_value = mock_s3_client

        url, success = ptd.aws_presign_bucket_object_url("s3://test-bucket/test-key")

        assert success is True
        assert url == "https://presigned-url.com"

    def test_aws_presign_bucket_object_url_invalid_url(self):
        """Test aws_presign_bucket_object_url with invalid S3 URL."""
        url, success = ptd.aws_presign_bucket_object_url("invalid-url")

        assert success is False
        assert url == ""

    def test_get_region_from_workload_config(self):
        """Test get_region_from_workload_config function."""
        # Test with workload config
        workload_config = MagicMock()
        workload_config.region = "us-west-2"

        result = ptd.get_region_from_workload_config(workload_config)
        assert result == "us-west-2"

        # Test without workload config
        result = ptd.get_region_from_workload_config(None)
        assert result == "us-east-2"

        # Test with custom default
        result = ptd.get_region_from_workload_config(None, "eu-west-1")
        assert result == "eu-west-1"


class TestPolicyFunctions:
    def test_aws_route53_dns_update_policy(self):
        """Test aws_route53_dns_update_policy function."""
        hosted_zone_ref = "arn:aws:route53:::hostedzone/Z123456789"
        policy = ptd.aws_route53_dns_update_policy(hosted_zone_ref)

        assert policy["Version"] == "2012-10-17"
        assert len(policy["Statement"]) == 2

        # Check first statement
        stmt1 = policy["Statement"][0]
        assert stmt1["Effect"] == "Allow"
        assert stmt1["Action"] == ["route53:ChangeResourceRecordSets"]
        assert stmt1["Resource"] == [hosted_zone_ref]

        # Check second statement
        stmt2 = policy["Statement"][1]
        assert stmt2["Effect"] == "Allow"
        assert stmt2["Resource"] == ["*"]

    def test_aws_traefik_forward_auth_secrets_policy(self):
        """Test aws_traefik_forward_auth_secrets_policy function."""
        policy = ptd.aws_traefik_forward_auth_secrets_policy("us-east-1", "123456789012")

        assert policy["Version"] == "2012-10-17"
        assert len(policy["Statement"]) == 1

        stmt = policy["Statement"][0]
        assert stmt["Effect"] == "Allow"
        assert "secretsmanager:GetSecretValue" in stmt["Action"]
        assert len(stmt["Resource"]) == 3


class TestRequestFunctions:
    @patch("requests.get")
    def test_mailgun_get_dkim_key_success(self, mock_get):
        """Test mailgun_get_dkim_key with successful response."""
        mock_response = MagicMock()
        mock_response.ok = True
        mock_response.json.return_value = {"items": [{"key": "test-key", "domain": "example.com"}]}
        mock_get.return_value = mock_response

        result, success = ptd.mailgun_get_dkim_key("api-key", "example.com")

        assert success is True
        assert result["key"] == "test-key"

    @patch("requests.get")
    def test_mailgun_get_dkim_key_failure(self, mock_get):
        """Test mailgun_get_dkim_key with failed response."""
        mock_response = MagicMock()
        mock_response.ok = False
        mock_get.return_value = mock_response

        result, success = ptd.mailgun_get_dkim_key("api-key", "example.com")

        assert success is False
        assert result == {}


class TestDefineComponentImage:
    """Test suite for the define_component_image function."""

    def test_basic_tag_resolution(self):
        """Test basic tag resolution."""
        result = ptd.define_component_image(
            image_config="v1.2.3",
            component_image=ptd.ComponentImages.TEAM_OPERATOR,
        )

        assert result == "docker.io/posit/ptd-team-operator:v1.2.3"

    def test_latest_tag(self):
        """Test latest tag."""
        result = ptd.define_component_image(
            image_config="latest",
            component_image=ptd.ComponentImages.TEAM_OPERATOR,
        )

        assert result == "docker.io/posit/ptd-team-operator:latest"

    def test_test_tag(self):
        """Test using 'test' tag for development images."""
        result = ptd.define_component_image(
            image_config="test",
            component_image=ptd.ComponentImages.TEAM_OPERATOR,
        )

        assert result == "docker.io/posit/ptd-team-operator:test"

    def test_dev_tag(self):
        """Test using 'dev' tag for development images."""
        result = ptd.define_component_image(
            image_config="dev",
            component_image=ptd.ComponentImages.FLIGHTDECK,
        )

        assert result == "docker.io/posit/ptd-flightdeck:dev"

    def test_custom_registry(self):
        """Test custom registry hostname."""
        result = ptd.define_component_image(
            image_config="v1.0.0",
            component_image=ptd.ComponentImages.TEAM_OPERATOR,
            image_registry_hostname="custom.registry.io/posit",
        )

        assert result == "custom.registry.io/posit/ptd-team-operator:v1.0.0"

    def test_full_image_passthrough(self):
        """Test that a fully qualified image is passed through as-is."""
        full_image = "custom-registry.io/custom-repo/custom-image:v1.0.0"

        result = ptd.define_component_image(
            image_config=full_image,
            component_image=ptd.ComponentImages.TEAM_OPERATOR,
        )

        assert result == full_image

    def test_empty_config_defaults_to_latest(self):
        """Test that empty config defaults to latest."""
        result = ptd.define_component_image(
            image_config="",
            component_image=ptd.ComponentImages.TEAM_OPERATOR,
        )

        assert result == "docker.io/posit/ptd-team-operator:latest"
