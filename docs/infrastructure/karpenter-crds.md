# Karpenter CRD management

How PTD manages Karpenter's CRDs, and the one-time migration existing clusters
need. Implemented in `awsHelmKarpenter` (`lib/steps/helm_aws.go`).

## Problem

The Karpenter controller chart (`oci://public.ecr.aws/karpenter/karpenter`)
bundles its CRDs in `crds/`. Helm installs `crds/` **once, on first install, and
never upgrades them**. So a `karpenter_version` bump upgrades the controller but
leaves the CRDs frozen. When a controller >= 1.9 emits a `Gte`/`Lte` requirement
(normalized from a NodePool `Gt`/`Lt` floor) against a pre-1.9 CRD, the API
server rejects the NodeClaim and no nodes provision.

## Fix

Manage the CRDs with the dedicated **`karpenter-crd`** chart, installed as a
second `helm.cattle.io/v1` HelmChart pinned to the **same `karpenter_version`**
as the controller. Its CRDs are templated, so Helm upgrades them on every bump.
This is Karpenter's recommended way to manage CRD lifecycle.

The chart's CRDs are stamped `helm.sh/resource-policy: keep` (via its
`additionalAnnotations` value) so that a `helm uninstall` — e.g. the HelmChart CR
being deleted, renamed, or the code reverted — never cascade-deletes the CRDs and
every NodePool/NodeClaim/EC2NodeClass. Uninstall leaves the CRDs in place (the safe
default, matching how the controller chart's bundled `crds/` always behaved).

## Why adoption is a manual, one-time step

Every existing cluster already has the Karpenter CRDs — the controller chart's
`crds/` created them — but **without Helm ownership metadata**. A plain
`karpenter-crd` install therefore fails:

```
Unable to continue with install: CustomResourceDefinition "nodepools.karpenter.sh"
in namespace "" exists and cannot be imported into the current release: invalid
ownership metadata; ...
```

Helm's normal escape hatch is `--take-ownership`, but PTD's helm job image is
pinned to `klipper-helm:v0.9.5-build20250306` (**Helm 3.16.4**), which predates
that flag. (The pin is deliberate — newer, Helm-4 klipper-helm builds regressed
install/upgrade detection.) So adoption can't be automated in the chart.

Instead, adopt the CRDs once per existing cluster by writing the ownership
metadata Helm expects, so the subsequent `karpenter-crd` install adopts them
cleanly. This is a **one-time migration**, not steady-state IaC: greenfield
clusters have no CRDs to adopt and skip it entirely.

> ⚠️ **Rollout order matters.** Once this ships, the `helm` step tries to install
> `karpenter-crd` on every cluster. On an un-migrated existing cluster that
> install **fails** until the stamp below is applied — non-destructive (running
> Karpenter is unaffected; the chart just won't reconcile). Migrate each existing
> cluster before, or immediately alongside, its next `helm`-step apply.

## Migration procedure (existing clusters, run once each)

Run against the target cluster (e.g. inside `ptd workon <cluster>`).

**1. Confirm the CRDs exist and are not yet Helm-owned** (empty output = unowned):

```bash
kubectl get crd nodeclaims.karpenter.sh \
  -o jsonpath='{.metadata.labels.app\.kubernetes\.io/managed-by}{"\n"}'
```

**2. Stamp Helm ownership.** Release name is the HelmChart CR name
(`karpenter-crd`); release namespace is its `targetNamespace` (`kube-system`).
Discovering the CRDs dynamically covers installs that lack `nodeoverlays` (a
Karpenter CRD added in v1.7.1, so clusters set up on older Karpenter don't have it):

```bash
for crd in $(kubectl get crd -o name | grep 'karpenter\.'); do
  kubectl label    "$crd" app.kubernetes.io/managed-by=Helm --overwrite
  kubectl annotate "$crd" meta.helm.sh/release-name=karpenter-crd --overwrite
  kubectl annotate "$crd" meta.helm.sh/release-namespace=kube-system --overwrite
done
```

**3. Apply the chart** — the `karpenter-crd` install now adopts and upgrades the
CRDs:

```bash
ptd ensure <cluster> --only-steps helm --dry-run     # review: new karpenter-crd HelmChart
ptd ensure <cluster> --only-steps helm --auto-apply
```

**4. Verify:**

```bash
# adopted → karpenter-crd
kubectl get crd nodeclaims.karpenter.sh -o jsonpath='{.metadata.annotations.meta\.helm\.sh/release-name}{"\n"}'
# CRDs upgraded → enum now includes Gte/Lte
kubectl get crd nodeclaims.karpenter.sh -o jsonpath='{.spec.versions[*].schema.openAPIV3Schema.properties.spec.properties.requirements.items.properties.operator.enum}{"\n"}'
# existing Karpenter resources intact
kubectl get nodepools,ec2nodeclasses
```

**Rollback (before step 3 only):** the stamp is additive metadata; undo it (the
trailing `-` deletes each key). After step 3 the CRDs are owned by the
`karpenter-crd` release.

```bash
for crd in $(kubectl get crd -o name | grep 'karpenter\.'); do
  kubectl label    "$crd" app.kubernetes.io/managed-by-
  kubectl annotate "$crd" meta.helm.sh/release-name- meta.helm.sh/release-namespace-
done
```

## Future: dropping the manual step

When the fleet's helm job image moves to a fixed Helm-4 klipper-helm build
(`>= v0.11.1-build20260615`, which restored install/upgrade detection) paired
with helm-controller `>= v0.16.14`, set `spec.takeOwnership: true` on the
`karpenter-crd` HelmChart and adoption becomes automatic — the manual migration
above is no longer needed. That executor upgrade is tracked separately.
