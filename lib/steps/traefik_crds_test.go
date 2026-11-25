package steps

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	yaml "sigs.k8s.io/yaml/goyaml.v3"
)

func TestTraefikCRDManifest(t *testing.T) {
	manifest, err := traefikCRDManifest()
	require.NoError(t, err)

	// Pinned deliberately rather than derived from the embedded directory: if a
	// refresh silently adds or drops a CRD, deriving the expected count from the
	// same files would move with it and assert nothing. 10 is the traefik.io CRD
	// count as of chart 41.6.0, so a change here should be a conscious update.
	docs := strings.Split(manifest, "---\n")
	require.Len(t, docs, 10, "expected the 10 vendored traefik.io CRDs")

	entries, err := traefikCRDAssets.ReadDir(traefikCRDsDir)
	require.NoError(t, err)
	require.Len(t, docs, len(entries), "every embedded CRD file must reach the manifest")

	var names []string
	for _, doc := range docs {
		var obj map[string]interface{}
		require.NoError(t, yaml.Unmarshal([]byte(doc), &obj))

		assert.Equal(t, "apiextensions.k8s.io/v1", obj["apiVersion"])
		assert.Equal(t, "CustomResourceDefinition", obj["kind"])

		metadata, ok := obj["metadata"].(map[string]interface{})
		require.True(t, ok)
		annotations, ok := metadata["annotations"].(map[string]interface{})
		require.True(t, ok)
		assert.Equal(t, "true", annotations["pulumi.com/patchForce"],
			"every CRD needs patchForce so SSA adopts the existing object")

		name, ok := metadata["name"].(string)
		require.True(t, ok)
		assert.True(t, strings.HasSuffix(name, ".traefik.io"),
			"only traefik.io CRDs are vendored, got %q", name)
		names = append(names, name)
	}

	assert.Contains(t, names, "middlewares.traefik.io")
	assert.Contains(t, names, "ingressroutes.traefik.io")

	// The manifest is an input to a Pulumi resource, so it has to be stable
	// across runs or every preview would show a spurious diff.
	again, err := traefikCRDManifest()
	require.NoError(t, err)
	assert.Equal(t, manifest, again)
}
