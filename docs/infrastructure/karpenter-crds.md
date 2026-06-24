# Karpenter CRD management

How PTD manages Karpenter's CRDs, and why it's done this way. Implemented in
`awsHelmKarpenter` (`lib/steps/helm_aws.go`).

## The problem

The Karpenter controller chart (`oci://public.ecr.aws/karpenter/karpenter`)
bundles its CRDs in the chart's `crds/` directory. Helm installs `crds/`
resources **once, on first install, and never upgrades them** on subsequent
`helm upgrade`s (this is intentional, documented Helm behavior).

So historically a `karpenter_version` bump upgraded the controller while leaving
the CRDs frozen at whatever version was first installed. When a controller
>= 1.9 then emits a `Gte`/`Lte` requirement — normalized from a NodePool
`Gt`/`Lt` instance floor — against a pre-1.9 CRD whose schema enum lacks those
operators, the API server rejects the NodeClaim and no nodes provision. The
break needs all three: controller >= 1.9, a NodePool using a `Gt`/`Lt` floor,
and a CRD missing `Gte`/`Lte`.

## The fix

Manage the CRDs with the dedicated **`karpenter-crd`** chart, installed as a
second `helm.cattle.io/v1` HelmChart pinned to the **same `karpenter_version`**
as the controller. The `karpenter-crd` chart templates the CRDs (rather than
shipping them in `crds/`), so Helm upgrades them on every version bump. This is
Karpenter's officially recommended way to manage CRD lifecycle.

### Adopting pre-existing CRDs

Every cluster created before this change already has the Karpenter CRDs — the
controller chart's `crds/` created them — but **without** `karpenter-crd` Helm
ownership metadata. A plain `karpenter-crd` install would therefore fail with
`exists and cannot be imported into the current release — invalid ownership
metadata`.

We resolve this with the helm-controller's native **`spec.takeOwnership`**
(maps to `helm upgrade --take-ownership`). It rewrites the ownership label and
`release-name`/`-namespace` annotations on the existing objects so the
`karpenter-crd` release becomes their owner. It is scoped to exactly the CRDs
the `karpenter-crd` chart renders, and is a no-op on greenfield clusters (the
chart just creates them). This replaced an earlier approach that manually
pre-stamped ownership metadata via `CustomResourcePatch`.

## Version requirements

- **helm-controller >= v0.16.14** — adds `spec.takeOwnership` (PTD sets this image
  in `clusters_aws.go` / `clusters_azure.go`, and the field must also be present
  in PTD's embedded HelmChart CRD schema in `clusters_helpers.go`, or the API
  server prunes it).
- **Helm >= 3.17** in the job image — `--take-ownership` (satisfied by the
  `klipper-helm` job image PTD pins).

## Known limitations

- **The controller chart's bundled `crds/` are left in place.** The k3s
  helm-controller has no `skipCRDs`/`crds` option and no way to pass Helm's
  `--skip-crds` (verified against its `HelmChartSpec`), so the controller chart's
  CRDs can't be disabled. This is benign on existing clusters: Helm skips `crds/`
  resources that already exist.
- **Ordering is best-effort.** `dependsOn` orders CR *creation* (CRD chart before
  the controller chart and the NodePool/EC2NodeClass CRs), but the helm-controller
  reconciles HelmChart CRs asynchronously, so it doesn't strictly guarantee the
  CRD helm job finishes first. The CRD changes are additive/backward-compatible
  and the dependent CRs retry until the CRDs exist.

## Rollout

The helm-controller image bump and the embedded HelmChart CRD schema change apply
to **every** cluster through the `clusters` step, not just Karpenter clusters.
Validate on a staging cluster (ideally one already in the stale-CRD state) before
broad rollout — confirm both the brownfield adoption path and a greenfield
install.
