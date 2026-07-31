# External Secrets on AKS (Azure Key Vault → Kubernetes)

This guide describes how PTD syncs Azure Key Vault secrets into native Kubernetes
Secrets on AKS using the [External Secrets Operator (ESO)](https://external-secrets.io),
and the one-time procedure for migrating an **existing** Azure workload cluster onto
this model.

## Overview

On AKS, ESO replaces the previous approach of hand-applying Kubernetes Secrets (or
baking Key Vault values into Secrets at deploy time). It is the AKS counterpart to
the AWS Secrets Store CSI driver. team-operator is **unaware** of ESO — it continues
to read native Secrets by name (`SecretType: kubernetes`), so no operator changes are
required.

ESO is installed by the `clusters` step when a cluster sets:

```yaml
clusters:
  "<release>":
    external_secrets_enabled: true
```

The step deploys the ESO controller into `posit-team-system` (authenticated to Key
Vault via workload identity) and creates a cluster-scoped `ClusterSecretStore` named
`azure-keyvault` pointing at the workload vault (`kv-ptd-<name[:17]>`).

## Key Vault naming convention

Product secrets are stored in Key Vault as **1:1 entries** (not JSON blobs) named:

```
<compound>-<site>-<field>
```

- `<compound>` — the workload (target) name, e.g. the value returned by `Target.Name()`.
- `<site>` — the site name (e.g. `main`).
- `<field>` — the Kubernetes Secret key the workload expects (e.g. `dev-db-password`).

Example (illustrative): `<compound>-<site>-dev-db-password`.

A per-site `ExternalSecret` selects everything under `^<compound>-<site>-` and rewrites
the key to strip that prefix, so the resulting Secret keys match exactly what
team-operator reads:

```yaml
apiVersion: external-secrets.io/v1
kind: ExternalSecret
spec:
  refreshInterval: 1h
  secretStoreRef:
    kind: ClusterSecretStore
    name: azure-keyvault
  target:
    name: <compound>-<site>-posit-team   # the Secret team-operator reads
    creationPolicy: Owner
  dataFrom:
    - find:
        name:
          regexp: "^<compound>-<site>-"
      rewrite:
        - regexp:
            source: "^<compound>-<site>-(.*)"
            target: "$1"
```

**The `<compound>-<site>-` prefix (not a bare `<compound>-` prefix) is required**: the
infrastructure secrets (`<compound>-mimir-auth`, `<compound>-postgres-admin-secret`,
`<compound>-grafana-postgres-admin-secret`, `<compound>-<release>-postgres-grafana-user`)
also start with the compound name, and must **not** be swept into the product Secret.
Those remain deploy-time (Pulumi) managed for now.

> AWS stores the same data as a **single JSON blob** per site
> (`<compound>-<site>.posit.team`); the ESO equivalent there uses `dataFrom.extract`
> on that one secret rather than `find`.

## Key Vault secret ownership

Every Key Vault secret is either **created by PTD code** or **created by hand/CLI**.
Anything in the hand-created column will not exist on a new workload until someone adds
it, and it is never regenerated — so it must be seeded during a migration and recreated
during a rebuild.

### Created by PTD code

| Key Vault secret | Step | Notes |
| --- | --- | --- |
| `<compound>-<site>-dev-db-password` | `bootstrap` | random, `secrets.NewSiteSecret` |
| `<compound>-<site>-keycloak-db-user` | `bootstrap` | derived from site name |
| `<compound>-<site>-keycloak-db-password` | `bootstrap` | random |
| `<compound>-<site>-pkg-db-password` | `bootstrap` | random |
| `<compound>-<site>-pkg-secret-key` | `bootstrap` | `rskey` generated |
| `<compound>-<site>-pub-db-password` | `bootstrap` | random |
| `<compound>-<site>-pub-secret-key` | `bootstrap` | `rskey` generated |
| `<compound>-postgres-admin-secret` | `persistent` | JSON `{fqdn,username,password}` |
| `<compound>-mimir-auth` | `persistent` | random password |
| `<compound>-<release>-postgres-grafana-user` | `postgres_config` | JSON `{database,password,role}`, per cluster |

### Created by hand / CLI (never written by code)

| Key Vault secret | Consumed by | Notes |
| --- | --- | --- |
| `<compound>-<site>-dev-license` | Workbench | license workflow |
| `<compound>-<site>-pub-license` | Connect | license workflow |
| `<compound>-<site>-pkg-license` | Package Manager | license workflow |
| `<compound>-<site>-dev-admin-token` | Workbench (OIDC only) | often empty; see below |
| `<compound>-<site>-dev-user-token` | Workbench (OIDC only) | often empty; see below |
| `<compound>-workload-main-database-url` | team-operator (workload secret) | see "Workload-level secrets" |
| `<compound>-grafana-postgres-admin-secret` | `postgres_config`, `helm` steps | **read-only in code** — the steps fail/warn if absent |
| `ptd-dockerhub-username` | ACR pull-through cache | shared, not per-workload |
| `ptd-dockerhub-oat` | ACR pull-through cache | shared, not per-workload |

Notes:
- `dev-admin-token` / `dev-user-token` are Workbench API tokens that team-operator only
  **reads** (mounted read-only), and only when Workbench auth is OIDC. Key Vault cannot
  store an empty value, so when they are unused the site's ExternalSecret emits them as
  empty literals via `target.template` (`mergePolicy: Merge`) to preserve the Secret's
  shape.
- `home-auth-map` is deprecated and is **not** synced.

### Workload-level secrets

The Site CR also references a **workload** Secret (`workloadSecret.vaultName` →
`<compound>-posit-team`) holding `main-database-url`. On AWS the `persistent` step writes
this automatically; that code path is **AWS-only**, so on Azure it is hand-created.

Its Key Vault entries use a `<compound>-workload-<field>` prefix — **not**
`<compound>-<field>`. A field named `main-database-url` under the bare compound prefix
would produce `<compound>-main-database-url`, which collides with the `^<compound>-main-`
site selector for a site named `main` (the default) and would be swept into the site
Secret. **`workload` is therefore a reserved site name.**

> There is currently no ExternalSecret generated for the workload Secret — it remains
> hand-applied. Seeding `<compound>-workload-main-database-url` prepares for that.

### Kubernetes Secrets NOT sourced from Key Vault

Do not attempt to bring these under ESO:

| Secret | Created by |
| --- | --- |
| `<component>-connect-key`, `<component>-packagemanager-key`, `<component>-workbench-key`, `<component>-workbench-config` | **team-operator** generates and owns these (keys, launcher PEM, DSN config). Leave them alone. |
| `azure-storage-account-<account>-secret` | `clusters` step, from the Azure Storage API |
| `external-dns/azure-config-file`, `grafana/grafana-db-url`, `alloy/mimir-auth` | `helm` step — Pulumi reads Key Vault and writes a *transformed* value (e.g. a connection string), so these are not 1:1 syncs |
| `<compound>-postgres-admin-secret` (in-cluster) | hand-applied today; could later use `dataFrom.extract` on the JSON blob |

## Secret value format

Key Vault values must be stored as **raw strings** — no surrounding JSON quotes and no
trailing newline — because ESO syncs the bytes verbatim into the Kubernetes Secret. A
quoted or newline-padded value would break consumers (e.g. a DB password carrying
literal `"` characters). The `bootstrap` step stores strings verbatim; when setting
values by hand, always use `--file` (see below) rather than `--value`.

## Migrating an existing cluster

For a cluster that is **already running** with live Secrets, the **cluster is the
source of truth**. Do not let `bootstrap` regenerate the code-generated secrets under
the new names — it would create fresh random values that don't match the live database
and app state. Instead, seed the new-named Key Vault entries from the live cluster
first (`CreateSecretIfNotExists` then no-ops).

For each existing Azure workload cluster:

1. **Enumerate** the site's live Secret keys (the `<compound>-<site>-posit-team` Secret
   and any separate product Secrets team-operator reads).
2. **Seed Key Vault from the cluster.** For every key — both code-generated and
   external — copy the live value into `<compound>-<site>-<field>`. Copy exact bytes via
   a temp file so there is no quoting, no newline stripping, and no plaintext in the
   process arguments:
   ```bash
   umask 077; tmp=$(mktemp)
   ptd workon <target> -- kubectl get secret <secret> -n posit-team \
     -o jsonpath="{.data.<key>}" | base64 -d > "$tmp"
   az keyvault secret set --vault-name <vault> --name "<compound>-<site>-<field>" \
     --file "$tmp" --encoding utf-8 >/dev/null
   rm -P "$tmp"
   ```
   Include licenses, and (OIDC sites) the Workbench `dev-admin-token` / `dev-user-token`.
3. **Verify** each entry matches the cluster by hash (never print plaintext):
   ```bash
   # Key Vault side
   az keyvault secret show --vault-name <vault> --name "<compound>-<site>-<field>" \
     -o json | jq -j '.value' | shasum -a 256 | cut -c1-16
   # Cluster side
   ptd workon <target> -- kubectl get secret <secret> -n posit-team \
     -o jsonpath="{.data.<key>}" | base64 -d | shasum -a 256 | cut -c1-16
   ```
   The two hashes must match before proceeding.
4. **Enable and apply**: set `external_secrets_enabled: true` on the cluster and run
   `ptd ensure <target> --only-steps clusters` (preview first).
5. **Confirm the sync**: the `ExternalSecret` should report `Ready=True` /
   `SecretSynced`, and the reproduced Secret should match the original key-for-key
   (hash each key as in step 3).
6. **Clean up**: see the checklist below.

### Post-migration cleanup checklist

Seed the new names *in parallel* and leave the old ones in place during the migration —
reverting is then just "delete the new entries". Once the ExternalSecret is confirmed
healthy and the reproduced Secret matches key-for-key, clean up:

- [ ] **Old-named Key Vault product secrets** — the pre-migration `<site>-<field>` entries
      (e.g. `main-dev-db-password`) that `<compound>-<site>-<field>` replaces.
- [ ] **Old-named license entries** — the `-lic` spellings (e.g. `main-dev-lic`) replaced
      by `<compound>-<site>-dev-license`.
- [ ] **Any test/scratch Key Vault secrets** created while validating the sync (e.g. a
      throwaway `…-findtest-*` prefix or a single-value sync probe).
- [ ] **Any test ExternalSecrets and their target Secrets** in the cluster. Delete the
      **ExternalSecret first** — otherwise ESO immediately recreates the Secret on its
      next refresh and it looks like the delete failed.
- [ ] **The `external-secrets` namespace**, if the cluster was first deployed with ESO
      there before it moved to `posit-team-system`. Pulumi removes it on the next
      `clusters` apply; confirm it is gone.
- [ ] **Verify nothing else matched the selector** — list the Key Vault entries matching
      `^<compound>-<site>-` and confirm each one is an intended product key:
      ```bash
      az keyvault secret list --vault-name <vault> \
        --query "[?starts_with(name,'<compound>-<site>-')].name" -o tsv
      ```

Azure Key Vault deletes are **soft deletes**: the entries remain recoverable (and their
names reserved) for the vault's retention period. Purge only if a name must be reused
immediately.

### Greenfield clusters

New clusters built after the naming change need no migration: `bootstrap` generates the
code-generated secrets under the correct names. Everything in
[Created by hand / CLI](#created-by-hand--cli-never-written-by-code) must still be added
to Key Vault manually — notably the three licenses, the workload
`main-database-url`, and `<compound>-grafana-postgres-admin-secret` (the `postgres_config`
and `helm` steps only ever *read* that one, so a missing entry surfaces as a step failure
or a warning rather than being created for you).

## See also

- External Secrets Operator docs: <https://external-secrets.io>
- Azure Key Vault provider: <https://external-secrets.io/latest/provider/azure-key-vault/>
