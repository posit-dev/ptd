package aws

// OIDC thumbprint computation for the EKS IAM OpenID Connect provider.
//
// This is a Go port of python-pulumi/src/ptd/oidc.py. Python computes the
// thumbprint by (a) GETting the issuer's /.well-known/openid-configuration,
// reading the netloc (host[:port]) of its jwks_uri, then (b) shelling out to the
// `thumbprint` CLI (github.com/rstudio/goex/cmd/thumbprint) which TLS-dials that
// netloc:443 and SHA1-hashes the LAST peer certificate the server presents.
//
// We replicate that exact algorithm inline (it is ~6 lines: dial, take the last
// peer cert, SHA1 it). We deliberately do NOT depend on the goex module — it is
// unmaintained — and we do NOT use the Pulumi tls.GetCertificate data source,
// which returns a different certificate chain than a raw tls.Dial and yields the
// wrong thumbprint. The work is done in the step's pre-fetch layer (a plain
// network dial, like the other live-state AWS lookups).

import (
	"context"
	"crypto/sha1" //nolint:gosec // SHA1 is the required IAM OIDC-provider thumbprint algorithm
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// GetOIDCThumbprint computes the CA thumbprint for an EKS cluster's OIDC issuer
// URL, matching python-pulumi/src/ptd/oidc.py exactly. It resolves the jwks_uri
// netloc from the issuer's OpenID configuration document, then returns the SHA1
// fingerprint (hex) of the root certificate served by that netloc on port 443.
func GetOIDCThumbprint(ctx context.Context, issuerURL string) (string, error) {
	netloc, err := getNetworkLocationForOIDCEndpoint(ctx, issuerURL)
	if err != nil {
		return "", err
	}
	return thumbprintForNetloc(netloc)
}

// thumbprintForNetloc dials "<netloc>:443" and returns the SHA1 fingerprint (hex)
// of the LAST peer certificate the server presents (the root/top-most cert in the
// served chain). This is a dependency-free inline port of
// github.com/rstudio/goex/crypto/tlsex.Thumbprint, the algorithm Python's
// `thumbprint` CLI uses, kept identical so the computed value matches Python.
func thumbprintForNetloc(netloc string) (string, error) {
	conn, err := tls.Dial("tcp", netloc+":443", &tls.Config{}) //nolint:gosec // zero-value tls.Config matches Python's thumbprint CLI
	if err != nil {
		return "", fmt.Errorf("aws: failed to dial %s:443 for OIDC thumbprint: %w", netloc, err)
	}
	defer conn.Close()

	peerCerts := conn.ConnectionState().PeerCertificates
	if len(peerCerts) == 0 {
		return "", fmt.Errorf("aws: no peer certificates presented by %s:443", netloc)
	}
	rootCert := peerCerts[len(peerCerts)-1]
	return fmt.Sprintf("%x", sha1.Sum(rootCert.Raw)), nil //nolint:gosec // SHA1 required by IAM OIDC thumbprint
}

// getNetworkLocationForOIDCEndpoint ports oidc.get_network_location_for_oidc_endpoint:
// strip trailing slashes from the issuer URL, append /.well-known/openid-configuration,
// GET it, and return the netloc (host[:port]) of the document's jwks_uri.
func getNetworkLocationForOIDCEndpoint(ctx context.Context, issuerURL string) (string, error) {
	// Remove trailing slashes to avoid double slashes when appending the
	// well-known path (mirrors Python's url.rstrip("/")).
	configURL := strings.TrimRight(issuerURL, "/") + "/.well-known/openid-configuration"

	if !strings.HasPrefix(configURL, "http:") && !strings.HasPrefix(configURL, "https:") {
		return "", fmt.Errorf("aws: OIDC issuer URL must start with 'http:' or 'https:': %s", issuerURL)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, configURL, nil)
	if err != nil {
		return "", fmt.Errorf("aws: failed to build OIDC config request for %s: %w", configURL, err)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("aws: failed to fetch OIDC config from %s: %w", configURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("aws: unexpected status %d fetching OIDC config from %s", resp.StatusCode, configURL)
	}

	var doc struct {
		JwksURI string `json:"jwks_uri"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return "", fmt.Errorf("aws: failed to decode OIDC config from %s: %w", configURL, err)
	}
	if doc.JwksURI == "" {
		return "", fmt.Errorf("aws: OIDC config from %s has no jwks_uri", configURL)
	}

	parsed, err := url.Parse(doc.JwksURI)
	if err != nil {
		return "", fmt.Errorf("aws: failed to parse jwks_uri %q: %w", doc.JwksURI, err)
	}
	return parsed.Host, nil
}
