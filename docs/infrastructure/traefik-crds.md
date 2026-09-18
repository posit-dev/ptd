# Traefik CRD management

How PTD manages Traefik's CRDs. Implemented in `lib/steps/traefik_crds.go`, wired
into all three Traefik paths: `awsHelmTraefik` (`lib/steps/helm_aws.go`),
`deployControlRoomTraefik` (`lib/steps/eks_helpers.go`), and the Traefik release
in `lib/steps/clusters_azure.go`.

## Problem

The Traefik chart (`https://traefik.github.io/charts`) bundles its CRDs in
`crds/`. Helm installs `crds/` **once, on first install, and never upgrades
them**. So a `traefik_version` bump rolls the controller but leaves the CRDs
frozen at whatever the cluster was first installed with. New fields the upgraded
controller understands are silently pruned by the API server, and manifests that
use them fail validation.

Until now the gap was closed by hand, after every chart bump, on every cluster:

```bash
helm show crds traefik/traefik | kubectl apply --server-side --force-conflicts -f -
```

That is easy to forget, invisible in state, and impossible to review.

## Why not a CRD chart

Karpenter solves the same problem with a dedicated `karpenter-crd` chart pinned
to the controller version (see [karpenter-crds.md](karpenter-crds.md)). That
option is **not available for Traefik**.

Traefik shipped a companion `traefik-crds` chart, and then discontinued it. Its
own changelog for 1.18.0 (2026-05-06) says: "This release is the last release of
this CRD Chart".

It is also already behind. The final `traefik-crds` release tracks the chart
40.0.0 era, and its `middlewares` CRD is missing `errors.errorRequestHeaders`,
which chart 41.6.0 ships. Pinning a frozen, lagging chart would reintroduce
exactly the drift we are trying to remove.

## Fix: vendor the CRDs

The `traefik.io` CRDs from the pinned chart version live in
`lib/steps/assets/traefik_crds/`, embedded into the `ptd` binary via `go:embed`
(`lib/steps/assets.go`), and applied as a `kubernetes:yaml/v2:ConfigGroup` named
`<prefix>-traefik-crds` on each path. Every Traefik release takes a
`pulumi.DependsOn` on that ConfigGroup, so the CRDs are always applied first.

Only the 10 `traefik.io_*` CRDs are vendored. The chart also ships 15
`hub.traefik.io_*` CRDs for Traefik Hub, which PTD does not use. The upstream
`traefik-crds` chart likewise defaulted `hub: false`.

Greenfield clusters are unaffected: our ConfigGroup applies the CRDs first, and
Helm then skips the `crds/` objects that already exist.

## Why a dedicated server-side-apply provider

Adopting CRDs that already exist on every cluster requires **server-side apply**
(SSA). Under SSA, Create becomes an upsert, so Pulumi takes over an existing
object instead of failing on "already exists".

Only the control-room Kubernetes provider (`lib/aws/eks_cluster.go`) sets
`EnableServerSideApply: true`. The workload, Azure and sites providers all run
client-side. Turning SSA on for those would change apply semantics for every
resource they manage, which is a much larger and riskier change.

So each path that needs the CRDs gets a **dedicated, additional** provider,
named `<sibling-provider-name>-traefik-crds`, built from the same kubeconfig and
used only for the CRD ConfigGroup:

| Path | Provider used for the CRDs |
|------|----------------------------|
| AWS workload (`helm` step) | new `<compound>-<release>-traefik-crds` provider, SSA on |
| Azure workload (`clusters` step) | new `<compound>-<release>-traefik-crds` provider, SSA on |
| Control room (`cluster` step) | the existing cluster provider, which already has SSA on |

Each CRD object also carries the `pulumi.com/patchForce: "true"` annotation
(injected in `traefikCRDManifest`). The live CRDs are owned by the field managers
that Helm and the earlier manual `kubectl apply --server-side` runs left behind;
force resolves those conflicts in Pulumi's favour rather than erroring.

## No manual adoption step

Unlike Karpenter, this needs **no one-time per-cluster migration**. Karpenter's
CRDs have to be stamped with Helm ownership metadata before the CRD chart will
adopt them, because the pinned klipper-helm build predates `--take-ownership`.
Here Pulumi does the apply directly, and SSA plus `patchForce` adopts the
existing CRDs on the first run. The first apply on an existing cluster shows the
ConfigGroup and its CRDs as creates in the preview; on the cluster they resolve
to in-place updates of the objects that are already there.

## Refreshing after a `traefik_version` bump

The vendored CRDs must move with the chart. When you change `traefik_version`:

```bash
just refresh-traefik-crds <chart-version>   # e.g. just refresh-traefik-crds 41.6.0
```

The recipe pulls and unpacks the chart, then copies its `crds/traefik.io_*.yaml`
files verbatim into `lib/steps/assets/traefik_crds/`. The vendored copies are
therefore the chart's own files byte for byte, so `git diff` shows exactly what
upstream changed.

Commit the refreshed files alongside the version bump, then run the usual dry
run before applying:

```bash
ptd ensure <target> --only-steps helm --dry-run      # AWS workload
ptd ensure <target> --only-steps clusters --dry-run  # Azure workload
ptd ensure <target> --only-steps cluster --dry-run   # control room
```

## Deletion behaviour

Deleting a CRD makes Kubernetes cascade-delete every custom resource of that
kind, so losing these would take out every IngressRoute, Middleware and
TraefikService on the cluster, which is all ingress routing.

The ConfigGroup is therefore created with `RetainOnDelete`, so removing it,
renaming it, or reverting the code leaves the CRDs in place on the cluster. That
matches how the chart's bundled `crds/` always behaved, and mirrors the
`helm.sh/resource-policy: keep` guard on the Karpenter CRDs. Pulumi drops the
resources from state and leaves the cluster objects alone.

`RetainOnDelete` produces no resource diff, so a `ptd ensure` that has nothing
else to do reports "no changes expected" and exits without writing it to state.
If you ever need to persist an option like this on its own, force the update
with `--preview=false --auto-apply` and confirm it landed with
`pulumi stack export`.
