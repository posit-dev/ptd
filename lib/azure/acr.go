package azure

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"io"
	"net/http"
	"net/url"
)

func GetAcrAuthToken(ctx context.Context, c *Credentials, registryUri string) (string, error) {
	// get a token for the current azure identity, then exchange it for a refresh token to use with ACR
	// https://learn.microsoft.com/en-us/answers/questions/1661926/how-to-get-acr-access-token-from-aad-token-using-m
	token, err := c.credentials.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{"https://management.azure.com/.default"}})
	if err != nil {
		return "", err
	}

	formData := url.Values{
		"grant_type":   {"access_token"},
		"service":      {registryUri},
		"tenant":       {c.TenantID()},
		"access_token": {token.Token},
	}

	refreshToken, err := getRefreshToken(registryUri, formData)
	if err != nil {
		return "", err
	}

	return refreshToken, nil
}

type ExchangeTokenResponse struct {
	RefreshToken string `json:"refresh_token"`
}

func getRefreshToken(registryUri string, formData url.Values) (string, error) {
	jsonResponse, err := http.PostForm(fmt.Sprintf("https://%s/oauth2/exchange", registryUri), formData)
	if err != nil {
		return "", err
	}
	defer func(Body io.ReadCloser) {
		err = Body.Close()
		if err != nil {
			return
		}
	}(jsonResponse.Body)
	body, err := io.ReadAll(jsonResponse.Body)
	var etr ExchangeTokenResponse
	if err = json.Unmarshal(body, &etr); err != nil {
		return "", err
	}

	return etr.RefreshToken, nil
}
