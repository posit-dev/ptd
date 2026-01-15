"""
OIDC-related functions like those implemented in `rstudio_pulumi.utils.oidc` but with
shelling out to `thumbprint` instead.

Ref: https://stackoverflow.com/questions/69247498/how-can-i-calculate-the-thumbprint-of-an-openid-connect-server
"""

import json
import urllib.parse
import urllib.request

import ptd.shext


def get_network_location_for_oidc_endpoint(url: str) -> str:
    """Get the 'netloc' portion of a given `url`'s `jwks_uri`"""
    # Remove trailing slashes to avoid double slashes when appending /.well-known/openid-configuration
    # This is specifically needed for Connect URLs that require trailing slashes for IdP functionality
    # for service account integrations
    url = url.rstrip("/") + "/.well-known/openid-configuration"

    if not url.startswith(("http:", "https:")):
        msg = "URL must start with 'http:' or 'https:'"
        raise ValueError(msg)

    with urllib.request.urlopen(url) as response:  # noqa: S310
        return urllib.parse.urlparse(json.load(response)["jwks_uri"]).netloc


def get_thumbprint(network_location: str) -> str:
    """
    Calculate the 'thumbprint' ('fingerprint') of the given `network_location`'s root
    certificate.
    """
    return ptd.shext.sh([ptd.paths.top() / ".local" / "bin" / "thumbprint", f"{network_location}:443"]).stdout.strip()
