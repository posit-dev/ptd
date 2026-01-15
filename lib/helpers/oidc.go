package helpers

import (
	"crypto/sha1"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"net/url"
)

func OIDCThumbprint(issuerURL string) (string, error) {
	parsedURL, err := url.Parse(issuerURL)
	if err != nil {
		return "", err
	}

	// Connect to the OIDC provider to get the TLS certificate
	conn, err := tls.Dial("tcp", parsedURL.Host+":443", &tls.Config{})
	if err != nil {
		return "", err
	}
	defer conn.Close()

	// Get the certificate chain
	certs := conn.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return "", fmt.Errorf("no certificates found")
	}

	// Use the last certificate in the chain (root CA)
	cert := certs[len(certs)-1]

	// Calculate SHA1 thumbprint
	hash := sha1.Sum(cert.Raw)
	return hex.EncodeToString(hash[:]), nil
}
