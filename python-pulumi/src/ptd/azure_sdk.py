import json

import azure.identity
import azure.keyvault.secrets


def get_secret(secret_name: str, vault_name: str) -> str:
    client = azure.keyvault.secrets.SecretClient(
        vault_url=f"https://{vault_name}.vault.azure.net",
        credential=azure.identity.DefaultAzureCredential(),
    )

    secret = client.get_secret(secret_name)
    return secret.value


def get_secret_json(secret_name: str, vault_name: str) -> dict[str]:
    secret_val = get_secret(secret_name, vault_name)
    return json.loads(secret_val)


def set_secret(secret_name: str, vault_name: str, secret_value: dict[str, str]) -> None:
    client = azure.keyvault.secrets.SecretClient(
        vault_url=f"https://{vault_name}.vault.azure.net",
        credential=azure.identity.DefaultAzureCredential(),
    )

    secret_value_json = json.dumps(secret_value)
    client.set_secret(secret_name, secret_value_json)
