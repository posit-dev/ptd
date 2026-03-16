import json

import azure.identity
import azure.keyvault.secrets
from azure.mgmt.compute import ComputeManagementClient


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


def get_latest_vm_image_version(
    subscription_id: str,
    location: str,
    publisher: str,
    offer: str,
    sku: str,
) -> str:
    """
    Get the latest VM image version for a given publisher, offer, and SKU.

    Equivalent to:
    az vm image list --publisher {publisher} --offer {offer} --sku {sku} --all --location {location}

    Args:
        subscription_id: Azure subscription ID
        location: Azure region (e.g., "eastus")
        publisher: Image publisher (e.g., "Canonical")
        offer: Image offer (e.g., "0001-com-ubuntu-server-jammy")
        sku: Image SKU (e.g., "22_04-lts-gen2")

    Returns:
        Latest version string (e.g., "22.04.202412100")
    """
    credential = azure.identity.DefaultAzureCredential()
    compute_client = ComputeManagementClient(credential, subscription_id)

    # List all versions for the given publisher, offer, and SKU
    images = compute_client.virtual_machine_images.list(
        location=location,
        publisher_name=publisher,
        offer=offer,
        skus=sku,
    )

    # Extract version strings and sort them
    # Azure returns VirtualMachineImageResource objects with a 'name' field containing the version
    versions = [image.name for image in images]

    if not versions:
        msg = f"No images found for {publisher}/{offer}/{sku} in {location}"
        raise ValueError(msg)

    # Sort versions lexicographically (Azure version format sorts correctly this way)
    # Example: "22.04.202401010" < "22.04.202412100"
    versions.sort()

    # Return the latest version
    return versions[-1]
