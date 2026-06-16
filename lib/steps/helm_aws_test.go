package steps

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/posit-dev/ptd/lib/types"
)

const grafanaForwardAuthMiddlewares = "kube-system-traefik-forward-auth-add-forwarded-headers@kubernetescrd,kube-system-traefik-forward-auth-main@kubernetescrd"

func TestGrafanaIngressValues(t *testing.T) {
	t.Run("main site opts into forward-auth adds annotation", func(t *testing.T) {
		sites := map[string]types.SiteConfig{
			"main": {Spec: types.SiteConfigSpec{UseTraefikForwardAuth: true}},
		}

		ingress := grafanaIngressValues("example.com", sites)

		annotations, ok := ingress["annotations"].(map[string]interface{})
		assert.True(t, ok, "expected annotations to be present")
		assert.Equal(t, grafanaForwardAuthMiddlewares, annotations["traefik.ingress.kubernetes.io/router.middlewares"])
	})

	t.Run("main site with forward-auth disabled has no annotation", func(t *testing.T) {
		sites := map[string]types.SiteConfig{
			"main": {Spec: types.SiteConfigSpec{UseTraefikForwardAuth: false}},
		}

		ingress := grafanaIngressValues("example.com", sites)

		_, hasAnnotations := ingress["annotations"]
		assert.False(t, hasAnnotations, "expected no annotations when forward-auth disabled")
	})

	t.Run("no main site has no annotation", func(t *testing.T) {
		sites := map[string]types.SiteConfig{
			"other": {Spec: types.SiteConfigSpec{UseTraefikForwardAuth: true}},
		}

		ingress := grafanaIngressValues("example.com", sites)

		_, hasAnnotations := ingress["annotations"]
		assert.False(t, hasAnnotations, "expected no annotations when no main site is present")
	})

	t.Run("nil sites map has no annotation", func(t *testing.T) {
		ingress := grafanaIngressValues("example.com", nil)

		_, hasAnnotations := ingress["annotations"]
		assert.False(t, hasAnnotations, "expected no annotations when sites map is nil")
	})

	t.Run("basic ingress values", func(t *testing.T) {
		ingress := grafanaIngressValues("example.com", nil)

		assert.Equal(t, true, ingress["enabled"])
		assert.Equal(t, []interface{}{"grafana.example.com"}, ingress["hosts"])
		assert.Equal(t, "/", ingress["path"])
	})
}
