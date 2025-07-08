package tailscale

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/jtdowney/tsbridge/internal/config"
	"github.com/jtdowney/tsbridge/internal/constants"
	"github.com/jtdowney/tsbridge/internal/errors"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
)

// tailscaleTokenURL is the OAuth2 token endpoint for Tailscale
const tailscaleTokenURL = "https://api.tailscale.com/api/v2/oauth/token" //nolint:gosec // This is a URL, not a credential

// tailscaleAPIBase is the base URL for Tailscale API
const tailscaleAPIBase = "https://api.tailscale.com"

// authKeyRequest represents the request to create a new auth key
type authKeyRequest struct {
	Capabilities  authKeyCapabilities `json:"capabilities"`
	ExpirySeconds int                 `json:"expirySeconds"`
	Tags          []string            `json:"tags,omitempty"`
}

// authKeyCapabilities defines what the auth key can do
type authKeyCapabilities struct {
	Devices struct {
		Create struct {
			Reusable         bool     `json:"reusable"`
			Ephemeral        bool     `json:"ephemeral"`
			Tags             []string `json:"tags"`
			PreauthorizeOnly bool     `json:"preauthorized"`
		} `json:"create"`
	} `json:"devices"`
}

// authKeyResponse represents the response from creating an auth key
type authKeyResponse struct {
	Key     string    `json:"key"`
	Created time.Time `json:"created"`
}

// generateAuthKeyWithOAuth generates a Tailscale auth key using OAuth2 client credentials
func generateAuthKeyWithOAuth(oauthConfig *oauth2.Config, apiBaseURL string, tags []string, ephemeral bool) (string, error) {
	ctx := context.Background()

	// Create a client credentials config for automatic token management
	ccConfig := &clientcredentials.Config{
		ClientID:     oauthConfig.ClientID,
		ClientSecret: oauthConfig.ClientSecret,
		TokenURL:     oauthConfig.Endpoint.TokenURL,
	}

	// Get HTTP client with automatic token refresh
	client := ccConfig.Client(ctx)

	// Create auth key request
	req := authKeyRequest{
		ExpirySeconds: constants.AuthKeyExpirySeconds,
		Tags:          tags,
	}

	// Set capabilities
	req.Capabilities.Devices.Create.Reusable = false
	req.Capabilities.Devices.Create.Ephemeral = ephemeral
	req.Capabilities.Devices.Create.Tags = tags
	req.Capabilities.Devices.Create.PreauthorizeOnly = false

	// Marshal request
	body, err := json.Marshal(req)
	if err != nil {
		return "", errors.WrapInternal(err, "marshaling auth key request")
	}

	// Create HTTP request
	url := apiBaseURL + "/api/v2/tailnet/-/keys"
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return "", errors.WrapInternal(err, "creating request")
	}
	httpReq.Header.Set("Content-Type", "application/json")

	// Make request
	resp, err := client.Do(httpReq)
	if err != nil {
		return "", errors.WrapNetwork(err, "making request")
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			slog.Debug("failed to close response body", "error", err)
		}
	}()

	// Check status
	if resp.StatusCode != http.StatusOK {
		var errResp map[string]interface{}
		_ = json.NewDecoder(resp.Body).Decode(&errResp)
		return "", errors.NewNetworkError(fmt.Sprintf("API returned status %d: %v", resp.StatusCode, errResp))
	}

	// Parse response
	var authKeyResp authKeyResponse
	if err := json.NewDecoder(resp.Body).Decode(&authKeyResp); err != nil {
		return "", errors.WrapInternal(err, "decoding response")
	}

	return authKeyResp.Key, nil
}

// generateOrResolveAuthKey generates an auth key using OAuth if configured, otherwise uses the resolved auth key
func generateOrResolveAuthKey(cfg config.Config, svc config.Service) (string, error) {
	// Config package has already resolved all secrets, so we can use them directly
	clientID := cfg.Tailscale.OAuthClientID
	clientSecret := cfg.Tailscale.OAuthClientSecret.Value()
	authKey := cfg.Tailscale.AuthKey.Value()

	// If OAuth is configured, use it to generate auth key
	if clientID != "" && clientSecret != "" {
		// Check for test endpoint override
		tokenURL := tailscaleTokenURL
		apiBase := tailscaleAPIBase
		if testEndpoint := os.Getenv("TSBRIDGE_OAUTH_ENDPOINT"); testEndpoint != "" {
			tokenURL = testEndpoint + "/api/v2/oauth/token"
			apiBase = testEndpoint
		}

		oauthConfig := &oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			Endpoint: oauth2.Endpoint{
				TokenURL: tokenURL,
			},
		}
		authKey, err := generateAuthKeyWithOAuth(oauthConfig, apiBase, svc.Tags, svc.Ephemeral)
		if err != nil {
			// Error from generateAuthKeyWithOAuth is already typed
			return "", err
		}

		// Log auth key generation for audit trail
		slog.Info("Generated Tailscale auth key for service registration",
			"service", svc.Name,
		)

		return authKey, nil
	}

	// Otherwise, use the resolved auth key
	if authKey != "" {
		return authKey, nil
	}

	// No auth method configured
	return "", errors.NewConfigError("no authentication method configured")
}
