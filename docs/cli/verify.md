# ptd verify

Run VIP (Verified Installation of Posit) tests against a PTD deployment to validate that products are installed correctly and functioning.

## Usage

```bash
ptd verify <target> [flags]
```

## How it works

1. Fetches the Site CR from the target cluster
2. Generates a `vip.toml` configuration from the Site CR (product URLs, auth provider)
3. Provisions a test user in Keycloak (if Keycloak is configured)
4. Runs VIP tests either as a Kubernetes Job (default) or locally
5. Streams test output and exits with an appropriate code

## Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--site` | `main` | Name of the Site CR to verify |
| `--categories` | (all) | Test categories to run (pytest `-m` marker) |
| `--local` | `false` | Run tests locally instead of as a K8s Job |
| `--config-only` | `false` | Generate and print `vip.toml` without running tests |
| `--image` | `ghcr.io/posit-dev/vip:latest` | VIP container image for K8s Job mode |
| `--keycloak-url` | (derived from Site CR) | Override Keycloak URL |
| `--realm` | `posit` | Keycloak realm name |
| `--test-username` | `vip-test-user` | Keycloak test user name |

## Examples

```bash
# Run all VIP tests (K8s Job mode)
ptd verify ganso01-staging

# Run only prerequisite checks
ptd verify ganso01-staging --categories prerequisites

# Generate config to inspect without running tests
ptd verify ganso01-staging --config-only

# Run locally (requires VIP + Python installed)
ptd verify ganso01-staging --local

# Verify a non-default site
ptd verify ganso01-staging --site secondary
```

## Authentication modes

VIP tests require authenticated access to Connect and Workbench. How credentials are provided depends on the deployment's identity provider.

### Keycloak deployments (automatic)

When the Site CR has Keycloak enabled, `ptd verify` automatically:

1. Reads Keycloak admin credentials from the `{site}-keycloak-initial-admin` Secret
2. Creates a test user via the Keycloak Admin API
3. Stores credentials in a `vip-test-credentials` Secret
4. Passes credentials to VIP via environment variables

Subsequent runs skip user creation if the Secret already exists.

### Okta / external IdP deployments (interactive)

Deployments using external identity providers (Okta, Azure AD, etc.) cannot provision test users programmatically. Use **local mode with interactive auth**:

```bash
ptd verify ganso01-staging --local --interactive-auth
```

This launches a visible browser window where you authenticate through the IdP's login flow. After login, VIP captures the session state and runs the remaining tests headlessly.

> **Note**: `--interactive-auth` requires `--local` mode. It cannot be used with the K8s Job mode since there is no browser available in-cluster.

For automated/CI verification of Okta deployments, you can pre-create a `vip-test-credentials` Secret manually:

```bash
kubectl create secret generic vip-test-credentials \
  --from-literal=username=<your-test-user> \
  --from-literal=password=<your-test-password> \
  -n posit-team
```

### Summary

| Mode | Auth method | Use case |
|------|-------------|----------|
| K8s Job (default) | Programmatic (Keycloak or pre-existing Secret) | CI, automated checks |
| `--local --interactive-auth` | Browser popup (Okta, Azure AD) | Developer validation |
| `--local` | Pre-existing Secret or env vars | Local automated runs |
