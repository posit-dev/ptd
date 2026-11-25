package steps

import (
	"fmt"
	"sort"
	"strings"

	"github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes"
	k8syamlv2 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/yaml/v2"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	yaml "sigs.k8s.io/yaml/goyaml.v3"
)

// Helm installs a chart's crds/ directory exactly once, on first install, and
// never upgrades it. A traefik_version bump therefore rolls the controller but
// leaves the CRDs frozen at whatever the cluster was first installed with, and
// new fields the upgraded controller understands get silently dropped by the
// API server. Traefik's companion traefik-crds chart is discontinued and lags
// the main chart, so PTD vendors the traefik.io CRDs (lib/steps/assets/traefik_crds,
// refreshed by `just refresh-traefik-crds <chart-version>`) and applies them as
// first-class Pulumi resources ahead of the Traefik release on every path.
// See docs/infrastructure/traefik-crds.md.

// traefikCRDManifest returns the vendored traefik.io CRDs as a single
// multi-document YAML manifest, in sorted filename order, with the
// pulumi.com/patchForce annotation added to every object.
//
// patchForce matters because these CRDs already exist on every cluster, owned
// by the field managers that Helm and the previous manual `kubectl apply
// --server-side` runs left behind. Under server-side apply, force resolves
// those conflicts in Pulumi's favour instead of erroring out, which is what
// lets Pulumi adopt the existing CRDs with no manual migration.
func traefikCRDManifest() (string, error) {
	entries, err := traefikCRDAssets.ReadDir(traefikCRDsDir)
	if err != nil {
		return "", fmt.Errorf("traefik crds: failed to read embedded assets: %w", err)
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	if len(names) == 0 {
		return "", fmt.Errorf("traefik crds: no embedded CRDs found in %s", traefikCRDsDir)
	}

	docs := make([]string, 0, len(names))
	for _, name := range names {
		raw, err := traefikCRDAssets.ReadFile(traefikCRDsDir + "/" + name)
		if err != nil {
			return "", fmt.Errorf("traefik crds: failed to read %s: %w", name, err)
		}

		var obj map[string]interface{}
		if err := yaml.Unmarshal(raw, &obj); err != nil {
			return "", fmt.Errorf("traefik crds: failed to parse %s: %w", name, err)
		}

		metadata, ok := obj["metadata"].(map[string]interface{})
		if !ok {
			return "", fmt.Errorf("traefik crds: %s has no metadata block", name)
		}
		annotations, ok := metadata["annotations"].(map[string]interface{})
		if !ok {
			annotations = map[string]interface{}{}
			metadata["annotations"] = annotations
		}
		annotations["pulumi.com/patchForce"] = "true"

		out, err := yaml.Marshal(obj)
		if err != nil {
			return "", fmt.Errorf("traefik crds: failed to render %s: %w", name, err)
		}
		docs = append(docs, string(out))
	}

	return strings.Join(docs, "---\n"), nil
}

// newTraefikCRDProvider builds a dedicated Kubernetes provider with server-side
// apply enabled, for the Traefik CRDs only.
//
// Only the control-room provider runs with server-side apply today; the
// workload, Azure and sites providers use client-side apply. Flipping those
// would change apply semantics for every resource they manage, so each path
// that needs the CRDs gets this extra provider instead, pointed at the same
// cluster via the same kubeconfig as its sibling.
func newTraefikCRDProvider(ctx *pulumi.Context, baseName, kubeconfig string) (*kubernetes.Provider, error) {
	name := baseName + "-traefik-crds"
	provider, err := kubernetes.NewProvider(ctx, name, &kubernetes.ProviderArgs{
		EnableServerSideApply: pulumi.Bool(true),
		Kubeconfig:            pulumi.String(kubeconfig),
	})
	if err != nil {
		return nil, fmt.Errorf("traefik crds: failed to create SSA provider %s: %w", name, err)
	}
	return provider, nil
}

// deployTraefikCRDs applies the vendored traefik.io CRDs as a ConfigGroup. The
// caller supplies a resource-name prefix and the options to apply, which must
// include a provider with server-side apply enabled (see newTraefikCRDProvider;
// the control-room provider already has it). The returned resource is meant to
// be passed to pulumi.DependsOn on the Traefik release so the CRDs land first.
//
// RetainOnDelete is set because deleting a CRD cascade-deletes every custom
// resource of that kind: dropping this ConfigGroup would take out every
// IngressRoute, Middleware and TraefikService on the cluster, i.e. all ingress
// routing. Retaining leaves the CRDs in place if the resource is removed,
// renamed or reverted, which matches how the chart's own crds/ always behaved
// and mirrors the helm.sh/resource-policy: keep guard on the Karpenter CRDs.
func deployTraefikCRDs(ctx *pulumi.Context, resourceName string, opts ...pulumi.ResourceOption) (pulumi.Resource, error) {
	manifest, err := traefikCRDManifest()
	if err != nil {
		return nil, err
	}
	// Prepend into a fresh slice rather than appending into the caller's: a
	// caller passing a pre-allocated slice with spare capacity would otherwise
	// have its backing array written to.
	opts = append([]pulumi.ResourceOption{pulumi.RetainOnDelete(true)}, opts...)
	crds, err := k8syamlv2.NewConfigGroup(ctx, resourceName, &k8syamlv2.ConfigGroupArgs{
		Yaml: pulumi.String(manifest),
	}, opts...)
	if err != nil {
		return nil, fmt.Errorf("traefik crds: failed to apply %s: %w", resourceName, err)
	}
	return crds, nil
}
