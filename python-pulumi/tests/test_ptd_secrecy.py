import json
from unittest.mock import MagicMock, patch

import pytest
from botocore.exceptions import ClientError

import ptd.junkdrawer
import ptd.secrecy


@pytest.mark.parametrize(
    ("secret_id", "stored", "cur_secret", "expected_result"),
    [
        pytest.param(
            "swordfish",
            None,
            None,
            ptd.secrecy.SecretResult(
                secret_id="swordfish",
                status="created",
                signature="unknowable",
            ),
            id="created",
        ),
        pytest.param(
            "swordfish",
            {"bagel": "everything"},
            {"bagel": "pizza"},
            ptd.secrecy.SecretResult(
                secret_id="swordfish",
                status="updated",
                changed=("bagel",),
                signature=ptd.junkdrawer.json_signature({"bagel": "pizza"}),
            ),
            id="updated",
        ),
        pytest.param(
            "swordfish",
            {"bagel": "pizza"},
            {"bagel": "pizza"},
            ptd.secrecy.SecretResult(
                secret_id="swordfish",
                status="unchanged",
                unchanged=("bagel",),
                signature=ptd.junkdrawer.json_signature({"bagel": "pizza"}),
            ),
            id="unchanged",
        ),
        pytest.param(
            "bobomb",
            {"bagel": "pizza"},
            {"bagel": "pizza"},
            ptd.secrecy.SecretResult(
                secret_id="bobomb",
                status="failed",
                signature=ptd.junkdrawer.json_signature({"bagel": "pizza"}),
            ),
            id="failed",
        ),
    ],
)
def test_aws_ensure_secret(
    secret_id: str,
    stored: dict[str, str] | None,
    cur_secret: dict[str, str] | None,
    expected_result: ptd.secrecy.SecretResult,
) -> None:
    with patch("boto3.client") as mock_boto_client:
        mock_client = MagicMock()
        mock_boto_client.return_value = mock_client

        # Mock create_secret behavior
        if secret_id == "bobomb":
            # Simulate an unexpected error for create_secret
            mock_client.create_secret.side_effect = ClientError(
                error_response={"Error": {"Code": "UnexpectedError"}}, operation_name="CreateSecret"
            )
        elif stored is None:
            # Secret doesn't exist, create will succeed
            mock_client.create_secret.return_value = {}
        else:
            # Secret exists, create will fail with ResourceExistsException
            mock_client.create_secret.side_effect = ClientError(
                error_response={"Error": {"Code": "ResourceExistsException"}}, operation_name="CreateSecret"
            )

            # Mock get_secret_value for existing secret
            mock_client.get_secret_value.return_value = {"SecretString": json.dumps(stored)}

            # Mock put_secret_value success
            mock_client.put_secret_value.return_value = {}

        actual_result = ptd.secrecy.aws_ensure_secret(secret_id, cur_secret=cur_secret)
        assert expected_result.secret_id == actual_result.secret_id
        assert expected_result.status == actual_result.status

        if expected_result.signature != "unknowable":
            assert expected_result.signature == actual_result.signature

        if expected_result.changed != ():
            assert expected_result.changed == actual_result.changed

        if expected_result.unchanged != ():
            assert expected_result.unchanged == actual_result.unchanged
