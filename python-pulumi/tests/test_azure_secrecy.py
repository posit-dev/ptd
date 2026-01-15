from unittest.mock import patch

import azure.core.exceptions
import pytest

from ptd.azure_secrecy import ensure_secret


@pytest.fixture
def mock_get_secret():
    with patch("ptd.azure_sdk.get_secret") as mock:
        yield mock


@pytest.fixture
def mock_set_secret():
    with patch("ptd.azure_sdk.set_secret") as mock:
        yield mock


@pytest.fixture
def mock_json_signature():
    with patch("ptd.junkdrawer.json_signature") as mock:
        yield mock


def test_ensure_secret_created(mock_get_secret, mock_set_secret, mock_json_signature):
    secret_name = "test-secret"
    vault_name = "test-vault"
    secret = {"key": "value"}
    cur_sig = "signature"

    mock_json_signature.return_value = cur_sig
    mock_get_secret.side_effect = azure.core.exceptions.ResourceNotFoundError

    result = ensure_secret(secret_name, vault_name, secret)

    mock_set_secret.assert_called_once_with(secret_name, vault_name, secret)
    assert result.status == "created"
    assert result.added == tuple(secret.keys())


def test_ensure_secret_updated(mock_get_secret, mock_set_secret, mock_json_signature):
    secret_name = "test-secret"
    vault_name = "test-vault"
    secret = {"key": "new-value"}
    existing_secret = {"key": "old-value"}
    cur_sig = "new-signature"
    existing_sig = "old-signature"

    mock_json_signature.side_effect = [cur_sig, existing_sig]
    mock_get_secret.return_value = existing_secret

    result = ensure_secret(secret_name, vault_name, secret)

    mock_set_secret.assert_called_once_with(secret_name, vault_name, secret)
    assert result.status == "updated"
    assert result.added == ()
    assert result.removed == ()
    assert result.unchanged == ()
    assert result.changed == ("key",)


def test_ensure_secret_unchanged(mock_get_secret, mock_set_secret, mock_json_signature):
    secret_name = "test-secret"
    vault_name = "test-vault"
    secret = {"key": "value"}
    cur_sig = "signature"

    mock_json_signature.return_value = cur_sig
    mock_get_secret.return_value = secret

    result = ensure_secret(secret_name, vault_name, secret)

    mock_set_secret.assert_not_called()
    assert result.status == "unchanged"
    assert result.unchanged == tuple(secret.keys())
