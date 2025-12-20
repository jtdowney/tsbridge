package tailscale

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/jtdowney/tsbridge/internal/config"
	"github.com/jtdowney/tsbridge/internal/constants"
	tserrors "github.com/jtdowney/tsbridge/internal/errors"
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
			Reusable      bool     `json:"reusable"`
			Ephemeral     bool     `json:"ephemeral"`
			Tags          []string `json:"tags"`
			Preauthorized bool     `json:"preauthorized"`
		} `json:"create"`
	} `json:"devices"`
}

// authKeyResponse represents the response from creating an auth key
type authKeyResponse struct {
	Key     string    `json:"key"`
	Created time.Time `json:"created"`
}

// generateAuthKeyWithOAuth generates a Tailscale auth key using OAuth2 client credentials with retry logic
func generateAuthKeyWithOAuth(oauthConfig *oauth2.Config, apiBaseURL string, tags []string, ephemeral bool, preauthorized bool) (string, error) {
	start := time.Now()
	slog.Debug("starting OAuth authentication for auth key generation",
		"api_base", apiBaseURL,
		"tags", tags,
		"ephemeral", ephemeral,
		"preauthorized", preauthorized,
	)

	// Configure exponential backoff with attempt limit
	b := backoff.NewExponentialBackOff()
	b.InitialInterval = constants.RetryInitialInterval
	b.MaxInterval = constants.RetryMaxInterval
	b.MaxElapsedTime = constants.OAuthRetryMaxElapsedTime
	b.Multiplier = constants.RetryMultiplier
	b.RandomizationFactor = constants.RetryRandomizationFactor

	// Limit to 3 attempts using WithMaxRetries
	backoffWithRetries := backoff.WithMaxRetries(b, constants.RetryMaxAttempts) // 2 retries = 3 total attempts

	var authKey string
	attemptCount := 0
	operation := func() error {
		attemptCount++
		attemptStart := time.Now()

		slog.Debug("attempting OAuth auth key generation",
			"attempt", attemptCount,
			"max_attempts", constants.RetryMaxAttempts+1,
		)

		var err error
		authKey, err = generateAuthKeyWithOAuthDirect(oauthConfig, apiBaseURL, tags, ephemeral, preauthorized)

		if err != nil {
			slog.Debug("OAuth auth key generation attempt failed",
				"attempt", attemptCount,
				"duration", time.Since(attemptStart),
				"error", err,
				"is_network_error", tserrors.IsNetwork(err),
				"is_retryable", isRetryableError(err),
			)
		} else {
			slog.Debug("OAuth auth key generation attempt succeeded",
				"attempt", attemptCount,
				"duration", time.Since(attemptStart),
			)
		}

		// Only retry on network errors and 5xx server errors
		if err != nil && (tserrors.IsNetwork(err) || isRetryableError(err)) {
			return err
		}

		// Don't retry on config errors, auth errors, or other non-retryable errors
		if err != nil {
			return backoff.Permanent(err)
		}

		return nil
	}

	err := backoff.Retry(operation, backoffWithRetries)

	if err != nil {
		slog.Debug("OAuth auth key generation failed after all attempts",
			"total_attempts", attemptCount,
			"total_duration", time.Since(start),
			"error", err,
		)
	} else {
		slog.Debug("OAuth auth key generation completed successfully",
			"total_attempts", attemptCount,
			"total_duration", time.Since(start),
		)
	}

	return authKey, err
}

// isRetryableError determines if an error is worth retrying
func isRetryableError(err error) bool {
	if err == nil {
		return false
	}

	// Check for HTTP status codes that indicate temporary failures
	var tsbridgeErr *tserrors.Error
	if errors.As(err, &tsbridgeErr) && tsbridgeErr.Type == tserrors.ErrTypeNetwork {
		// Retry on 5xx server errors
		if tsbridgeErr.HTTPStatusCode >= 500 && tsbridgeErr.HTTPStatusCode < 600 {
			return true
		}

		// Also check for other retryable network errors in the message
		errStr := tsbridgeErr.Error()
		return strings.Contains(errStr, "timeout") ||
			strings.Contains(errStr, "connection refused") ||
			strings.Contains(errStr, "no such host")
	}

	return false
}

// generateAuthKeyWithOAuthDirect generates a Tailscale auth key using OAuth2 client credentials (no retry)
func generateAuthKeyWithOAuthDirect(oauthConfig *oauth2.Config, apiBaseURL string, tags []string, ephemeral bool, preauthorized bool) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), constants.OAuthHTTPTimeout)
	defer cancel()

	start := time.Now()

	slog.Debug("starting OAuth token exchange",
		"token_url", oauthConfig.Endpoint.TokenURL,
		"client_id", oauthConfig.ClientID,
	)

	// Create a client credentials config for automatic token management
	ccConfig := &clientcredentials.Config{
		ClientID:     oauthConfig.ClientID,
		ClientSecret: oauthConfig.ClientSecret,
		TokenURL:     oauthConfig.Endpoint.TokenURL,
	}

	// Get HTTP client with automatic token refresh
	tokenStart := time.Now()
	client := ccConfig.Client(ctx)
	slog.Debug("OAuth client created",
		"duration", time.Since(tokenStart),
	)

	// Create auth key request
	req := authKeyRequest{
		ExpirySeconds: constants.AuthKeyExpirySeconds,
		Tags:          tags,
	}

	// Set capabilities
	req.Capabilities.Devices.Create.Reusable = false
	req.Capabilities.Devices.Create.Ephemeral = ephemeral
	req.Capabilities.Devices.Create.Tags = tags
	req.Capabilities.Devices.Create.Preauthorized = preauthorized

	// Marshal request
	body, err := json.Marshal(req)
	if err != nil {
		return "", tserrors.WrapInternal(err, "marshaling auth key request")
	}

	slog.Debug("sending auth key creation request",
		"url", apiBaseURL+"/api/v2/tailnet/-/keys",
		"request_size", len(body),
		"ephemeral", ephemeral,
		"preauthorized", preauthorized,
		"tags", tags,
		"expiry_seconds", constants.AuthKeyExpirySeconds,
	)

	// Create HTTP request
	url := apiBaseURL + "/api/v2/tailnet/-/keys"
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return "", tserrors.WrapInternal(err, "creating request")
	}
	httpReq.Header.Set("Content-Type", "application/json")

	// Make request
	apiStart := time.Now()
	resp, err := client.Do(httpReq)
	if err != nil {
		slog.Debug("auth key API request failed",
			"duration", time.Since(apiStart),
			"error", err,
		)
		return "", tserrors.WrapNetwork(err, "making request")
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			slog.Debug("failed to close response body", "error", err)
		}
	}()

	slog.Debug("auth key API response received",
		"duration", time.Since(apiStart),
		"status_code", resp.StatusCode,
		"headers", resp.Header,
	)

	// Check status
	if resp.StatusCode != http.StatusOK {
		var errResp map[string]interface{}
		_ = json.NewDecoder(resp.Body).Decode(&errResp)
		slog.Debug("auth key API error response",
			"status_code", resp.StatusCode,
			"error_response", errResp,
			"total_duration", time.Since(start),
		)
		return "", tserrors.NewNetworkErrorWithStatus(fmt.Sprintf("API returned status %d: %v", resp.StatusCode, errResp), resp.StatusCode)
	}

	// Parse response
	var authKeyResp authKeyResponse
	if err := json.NewDecoder(resp.Body).Decode(&authKeyResp); err != nil {
		return "", tserrors.WrapInternal(err, "decoding response")
	}

	slog.Debug("auth key created successfully",
		"total_duration", time.Since(start),
		"key_created_at", authKeyResp.Created,
		"has_key", authKeyResp.Key != "",
	)

	return authKeyResp.Key, nil
}

// generateOrResolveAuthKey generates an auth key using OAuth if configured, otherwise uses the resolved auth key
func generateOrResolveAuthKey(cfg config.Config, svc config.Service) (string, error) {
	// Config package has already resolved all secrets, so we can use them directly
	clientID := cfg.Tailscale.OAuthClientID
	clientSecret := cfg.Tailscale.OAuthClientSecret.Value()
	authKey := cfg.Tailscale.AuthKey.Value()

	slog.Debug("resolving authentication method for service",
		"service", svc.Name,
		"has_oauth_client_id", clientID != "",
		"has_oauth_client_secret", clientSecret != "",
		"has_auth_key", authKey != "",
	)

	// If OAuth is configured, use it to generate auth key
	if clientID != "" && clientSecret != "" {
		slog.Debug("using OAuth authentication for service",
			"service", svc.Name,
		)

		// Check for test endpoint override
		tokenURL := tailscaleTokenURL
		apiBase := tailscaleAPIBase
		if testEndpoint := os.Getenv("TSBRIDGE_OAUTH_ENDPOINT"); testEndpoint != "" {
			tokenURL = testEndpoint + "/api/v2/oauth/token"
			apiBase = testEndpoint
			slog.Debug("using custom OAuth endpoint",
				"service", svc.Name,
				"endpoint", testEndpoint,
			)
		}

		oauthConfig := &oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			Endpoint: oauth2.Endpoint{
				TokenURL: tokenURL,
			},
		}
		// Get preauthorized setting - service override takes precedence over global setting
		preauthorized := true
		if svc.OAuthPreauthorized != nil {
			// Service-specific override
			preauthorized = *svc.OAuthPreauthorized
		} else if cfg.Tailscale.OAuthPreauthorized != nil {
			// Global setting
			preauthorized = *cfg.Tailscale.OAuthPreauthorized
		}

		authKey, err := generateAuthKeyWithOAuth(oauthConfig, apiBase, svc.Tags, svc.Ephemeral, preauthorized)
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
		slog.Debug("using pre-configured auth key for service",
			"service", svc.Name,
		)
		return authKey, nil
	}

	// No auth method configured
	slog.Debug("no authentication method configured for service",
		"service", svc.Name,
	)
	return "", tserrors.NewConfigError("no authentication method configured")
}
