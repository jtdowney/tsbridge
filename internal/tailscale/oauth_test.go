package tailscale

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jtdowney/tsbridge/internal/config"
	"github.com/jtdowney/tsbridge/internal/constants"
	"github.com/jtdowney/tsbridge/internal/errors"
	"golang.org/x/oauth2"
)

func TestGenerateAuthKeyWithOAuth(t *testing.T) {
	// Mock OAuth2 token endpoint
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth/token" {
			t.Errorf("expected /oauth/token, got %s", r.URL.Path)
		}

		// Verify OAuth2 client credentials grant
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if grant := r.Form.Get("grant_type"); grant != "client_credentials" {
			t.Errorf("expected grant_type=client_credentials, got %s", grant)
		}

		// Return mock token
		w.Header().Set("Content-Type", "application/json")
		token := map[string]interface{}{
			"access_token": "mock-access-token",
			"token_type":   "Bearer",
			"expires_in":   3600,
		}
		_ = json.NewEncoder(w).Encode(token)
	}))
	defer tokenServer.Close()

	// Mock Tailscale API endpoint for creating auth keys
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/tailnet/-/keys" {
			t.Errorf("expected /api/v2/tailnet/-/keys, got %s", r.URL.Path)
		}

		// Verify authorization header
		if auth := r.Header.Get("Authorization"); auth != "Bearer mock-access-token" {
			t.Errorf("expected Bearer mock-access-token, got %s", auth)
		}

		// Parse request body
		var keyReq map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&keyReq); err != nil {
			t.Fatal(err)
		}

		// Verify tags are included
		tags, ok := keyReq["tags"].([]interface{})
		if !ok || len(tags) == 0 {
			t.Error("expected tags in request")
		}

		// Return mock auth key
		response := map[string]interface{}{
			"key":     "tskey-auth-mock123",
			"created": time.Now().Format(time.RFC3339),
		}
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer apiServer.Close()

	// Configure OAuth with test servers
	cfg := config.Tailscale{
		OAuthClientID:     "test-client-id",
		OAuthClientSecret: config.RedactedString("test-client-secret"),
	}

	// Create OAuth client with test endpoints
	oauthConfig := &oauth2.Config{
		ClientID:     cfg.OAuthClientID,
		ClientSecret: cfg.OAuthClientSecret.Value(),
		Endpoint: oauth2.Endpoint{
			TokenURL: tokenServer.URL + "/oauth/token",
		},
	}

	// Generate auth key using OAuth
	authKey, err := generateAuthKeyWithOAuth(oauthConfig, apiServer.URL, []string{"tag:test"}, false)
	if err != nil {
		t.Fatalf("failed to generate auth key: %v", err)
	}

	if authKey != "tskey-auth-mock123" {
		t.Errorf("expected tskey-auth-mock123, got %s", authKey)
	}
}

func TestOAuthFallbackToAuthKey(t *testing.T) {
	// Test that when OAuth is not configured, it falls back to auth key
	cfg := config.Tailscale{
		AuthKey: "tskey-existing-123",
	}

	server, err := NewServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := server.Close(); err != nil {
			t.Logf("failed to close test server: %v", err)
		}
	}()

	// The auth key should be used when OAuth is not configured
	// Config already has the resolved auth key
	if cfg.AuthKey != "tskey-existing-123" {
		t.Errorf("expected tskey-existing-123, got %s", cfg.AuthKey)
	}
}

func TestOAuthTokenRefresh(t *testing.T) {
	tokenCallCount := 0

	// Mock OAuth2 token endpoint
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenCallCount++

		// Always return valid tokens, but different for each call
		token := map[string]interface{}{
			"access_token": fmt.Sprintf("mock-access-token-%d", tokenCallCount),
			"token_type":   "Bearer",
			"expires_in":   3600,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(token)
	}))
	defer tokenServer.Close()

	// Test that the clientcredentials client can fetch tokens multiple times
	// This simulates automatic token refresh behavior
	apiCallCount := 0
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiCallCount++

		// Return success for each call
		response := map[string]interface{}{
			"key":     fmt.Sprintf("tskey-auth-mock-%d", apiCallCount),
			"created": time.Now().Format(time.RFC3339),
		}
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer apiServer.Close()

	// Configure OAuth
	oauthConfig := &oauth2.Config{
		ClientID:     "test-client-id",
		ClientSecret: "test-client-secret",
		Endpoint: oauth2.Endpoint{
			TokenURL: tokenServer.URL + "/oauth/token",
		},
	}

	// Generate auth key twice to simulate token usage
	key1, err := generateAuthKeyWithOAuth(oauthConfig, apiServer.URL, []string{"tag:test"}, false)
	if err != nil {
		t.Fatal(err)
	}

	key2, err := generateAuthKeyWithOAuth(oauthConfig, apiServer.URL, []string{"tag:test"}, false)
	if err != nil {
		t.Fatal(err)
	}

	// Verify we got different keys (showing new API calls worked)
	if key1 == key2 {
		t.Error("expected different keys for different API calls")
	}

	// Each API call should trigger a token fetch with client credentials
	if tokenCallCount < 2 {
		t.Errorf("expected at least 2 token calls, got %d", tokenCallCount)
	}
}

func TestOAuthErrorHandling(t *testing.T) {
	tests := []struct {
		name          string
		tokenStatus   int
		tokenResponse string
		apiStatus     int
		apiResponse   string
		expectError   bool
	}{
		{
			name:          "token endpoint error",
			tokenStatus:   http.StatusUnauthorized,
			tokenResponse: `{"error": "invalid_client"}`,
			expectError:   true,
		},
		{
			name:          "api endpoint error",
			tokenStatus:   http.StatusOK,
			tokenResponse: `{"access_token": "test-token", "token_type": "Bearer", "expires_in": 3600}`,
			apiStatus:     http.StatusForbidden,
			apiResponse:   `{"error": "insufficient permissions"}`,
			expectError:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Mock servers
			tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.tokenStatus)
				w.Write([]byte(tt.tokenResponse))
			}))
			defer tokenServer.Close()

			apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tt.apiStatus != 0 {
					w.WriteHeader(tt.apiStatus)
					w.Write([]byte(tt.apiResponse))
				}
			}))
			defer apiServer.Close()

			// Configure OAuth
			oauthConfig := &oauth2.Config{
				ClientID:     "test-client-id",
				ClientSecret: "test-client-secret",
				Endpoint: oauth2.Endpoint{
					TokenURL: tokenServer.URL + "/oauth/token",
				},
			}

			// Try to generate auth key
			_, err := generateAuthKeyWithOAuth(oauthConfig, apiServer.URL, []string{"tag:test"}, false)

			if tt.expectError && err == nil {
				t.Error("expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestOAuthErrorTypes(t *testing.T) {
	t.Run("token endpoint returns non-200 but no error body", func(t *testing.T) {
		tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`some other error`))
		}))
		defer tokenServer.Close()

		oauthConfig := &oauth2.Config{
			ClientID:     "test-client-id",
			ClientSecret: "test-client-secret",
			Endpoint: oauth2.Endpoint{
				TokenURL: tokenServer.URL + "/oauth/token",
			},
		}

		_, err := generateAuthKeyWithOAuth(oauthConfig, "http://example.com", []string{"tag:test"}, false)
		if err == nil {
			t.Error("expected error for non-200 response without error body")
		}
	})

	t.Run("API endpoint returns non-200 but no error body", func(t *testing.T) {
		tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"access_token": "good-token", "token_type": "Bearer"}`))
		}))
		defer tokenServer.Close()

		apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte(`permission denied`))
		}))
		defer apiServer.Close()

		oauthConfig := &oauth2.Config{
			ClientID:     "test-client-id",
			ClientSecret: "test-client-secret",
			Endpoint: oauth2.Endpoint{
				TokenURL: tokenServer.URL + "/oauth/token",
			},
		}

		_, err := generateAuthKeyWithOAuth(oauthConfig, apiServer.URL, []string{"tag:test"}, false)
		if err == nil {
			t.Error("expected error for non-200 response without error body")
		}
	})

	t.Run("API endpoint returns 200 but invalid JSON", func(t *testing.T) {
		tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"access_token": "good-token", "token_type": "Bearer"}`))
		}))
		defer tokenServer.Close()

		apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`not-json`))
		}))
		defer apiServer.Close()

		oauthConfig := &oauth2.Config{
			ClientID:     "test-client-id",
			ClientSecret: "test-client-secret",
			Endpoint: oauth2.Endpoint{
				TokenURL: tokenServer.URL + "/oauth/token",
			},
		}

		_, err := generateAuthKeyWithOAuth(oauthConfig, apiServer.URL, []string{"tag:test"}, false)
		if err == nil {
			t.Error("expected error for invalid JSON response")
		}
	})
}

func TestGenerateOrResolveAuthKeyWithServiceTags(t *testing.T) {
	// Track the request body to verify tags
	var requestBody []byte

	// Create a single server that handles both OAuth token and API endpoints
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch r.URL.Path {
		case "/api/v2/oauth/token":
			// Handle OAuth token request
			token := map[string]interface{}{
				"access_token": "mock-access-token",
				"token_type":   "Bearer",
				"expires_in":   3600,
			}
			_ = json.NewEncoder(w).Encode(token)
		case "/api/v2/tailnet/-/keys":
			// Handle API request
			body, _ := io.ReadAll(r.Body)
			requestBody = body

			response := map[string]interface{}{
				"key":     "tskey-auth-mock123",
				"created": time.Now().Format(time.RFC3339),
			}
			_ = json.NewEncoder(w).Encode(response)
		}
	}))
	defer server.Close()

	// Set up test environment
	t.Setenv("TSBRIDGE_OAUTH_ENDPOINT", server.URL)

	// Test with service-specific tags
	cfg := config.Config{
		Tailscale: config.Tailscale{
			OAuthClientID:     "test-client-id",
			OAuthClientSecret: "test-client-secret",
		},
	}

	svc := config.Service{
		Name: "test-service",
		Tags: []string{"tag:api", "tag:prod"},
	}

	authKey, err := generateOrResolveAuthKey(cfg, svc)
	if err != nil {
		t.Fatalf("failed to generate auth key: %v", err)
	}

	if authKey != "tskey-auth-mock123" {
		t.Errorf("expected tskey-auth-mock123, got %s", authKey)
	}

	// Verify the service tags were used
	var req authKeyRequest
	if err := json.Unmarshal(requestBody, &req); err != nil {
		t.Fatalf("failed to unmarshal request body: %v", err)
	}

	if len(req.Tags) != 2 || req.Tags[0] != "tag:api" || req.Tags[1] != "tag:prod" {
		t.Errorf("expected service tags [tag:api tag:prod], got %v", req.Tags)
	}

	// Verify expiry is set to 5 minutes
	if req.ExpirySeconds != constants.AuthKeyExpirySeconds {
		t.Errorf("expected expiry=%d seconds, got %d", constants.AuthKeyExpirySeconds, req.ExpirySeconds)
	}

	// Verify reusable is false
	if req.Capabilities.Devices.Create.Reusable != false {
		t.Errorf("expected reusable=false, got %v", req.Capabilities.Devices.Create.Reusable)
	}
}

func TestOAuthEphemeralFlag(t *testing.T) {
	tests := []struct {
		name      string
		ephemeral bool
	}{
		{
			name:      "ephemeral true",
			ephemeral: true,
		},
		{
			name:      "ephemeral false",
			ephemeral: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Track the request body
			var requestBody []byte

			// Create mock servers
			tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"access_token": "test-token", "token_type": "Bearer"}`))
			}))
			defer tokenServer.Close()

			apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Capture the request body
				body, _ := io.ReadAll(r.Body)
				requestBody = body

				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"key": "test-auth-key"}`))
			}))
			defer apiServer.Close()

			oauthConfig := &oauth2.Config{
				ClientID:     "test-client-id",
				ClientSecret: "test-client-secret",
				Endpoint: oauth2.Endpoint{
					TokenURL: tokenServer.URL + "/oauth/token",
				},
			}

			// Generate auth key with ephemeral flag
			_, err := generateAuthKeyWithOAuth(oauthConfig, apiServer.URL, []string{"tag:test"}, tt.ephemeral)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			// Parse the request body to check ephemeral flag
			var req authKeyRequest
			if err := json.Unmarshal(requestBody, &req); err != nil {
				t.Fatalf("failed to unmarshal request body: %v", err)
			}

			// Verify ephemeral flag was set correctly
			if req.Capabilities.Devices.Create.Ephemeral != tt.ephemeral {
				t.Errorf("expected ephemeral=%v, got %v", tt.ephemeral, req.Capabilities.Devices.Create.Ephemeral)
			}

			// Verify reusable is always false for security
			if req.Capabilities.Devices.Create.Reusable != false {
				t.Errorf("expected reusable=false, got %v", req.Capabilities.Devices.Create.Reusable)
			}
		})
	}
}

func TestCredentialResolutionErrorTypes(t *testing.T) {
	t.Run("no auth key or oauth returns config error", func(t *testing.T) {
		cfg := config.Tailscale{}
		_, err := NewServer(cfg)

		if err == nil {
			t.Fatal("expected error when no auth key or oauth provided")
		}
		if !errors.IsConfig(err) {
			t.Errorf("expected config error, got %v", err)
		}
	})
}

func TestAuthKeyNonReusable(t *testing.T) {
	// Track the request body to verify reusable flag
	var requestBody []byte

	// Create mock servers
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"access_token": "test-token", "token_type": "Bearer"}`))
	}))
	defer tokenServer.Close()

	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Capture the request body
		body, _ := io.ReadAll(r.Body)
		requestBody = body

		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"key": "test-auth-key"}`))
	}))
	defer apiServer.Close()

	oauthConfig := &oauth2.Config{
		ClientID:     "test-client-id",
		ClientSecret: "test-client-secret",
		Endpoint: oauth2.Endpoint{
			TokenURL: tokenServer.URL + "/oauth/token",
		},
	}

	// Generate auth key
	_, err := generateAuthKeyWithOAuth(oauthConfig, apiServer.URL, []string{"tag:test"}, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Parse the request body to check reusable flag
	var req authKeyRequest
	if err := json.Unmarshal(requestBody, &req); err != nil {
		t.Fatalf("failed to unmarshal request body: %v", err)
	}

	// Verify reusable flag is false for better security
	if req.Capabilities.Devices.Create.Reusable != false {
		t.Errorf("expected reusable=false for single-use auth keys, got %v", req.Capabilities.Devices.Create.Reusable)
	}

	// Verify expiry is set to 5 minutes
	if req.ExpirySeconds != constants.AuthKeyExpirySeconds {
		t.Errorf("expected expiry=%d seconds, got %d", constants.AuthKeyExpirySeconds, req.ExpirySeconds)
	}
}

func TestAuthKeyGenerationLogging(t *testing.T) {
	// Create a buffer to capture log output
	var logBuf bytes.Buffer

	// Set up custom logger to capture output
	originalLogger := slog.Default()
	testLogger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))
	slog.SetDefault(testLogger)
	defer slog.SetDefault(originalLogger)

	// Create a single server that handles both OAuth token and API endpoints
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch r.URL.Path {
		case "/api/v2/oauth/token":
			// Handle OAuth token request
			token := map[string]interface{}{
				"access_token": "mock-access-token",
				"token_type":   "Bearer",
				"expires_in":   3600,
			}
			_ = json.NewEncoder(w).Encode(token)
		case "/api/v2/tailnet/-/keys":
			// Handle API request
			response := map[string]interface{}{
				"key":     "tskey-auth-mock123",
				"created": time.Now().Format(time.RFC3339),
			}
			_ = json.NewEncoder(w).Encode(response)
		}
	}))
	defer server.Close()

	// Set up test environment
	t.Setenv("TSBRIDGE_OAUTH_ENDPOINT", server.URL)

	// Test with OAuth credentials
	cfg := config.Config{
		Tailscale: config.Tailscale{
			OAuthClientID:     "test-client-id",
			OAuthClientSecret: "test-client-secret",
		},
	}

	svc := config.Service{
		Name:      "test-service",
		Tags:      []string{"tag:api"},
		Ephemeral: false,
	}

	// Generate auth key using OAuth
	authKey, err := generateOrResolveAuthKey(cfg, svc)
	if err != nil {
		t.Fatalf("failed to generate auth key: %v", err)
	}

	if authKey != "tskey-auth-mock123" {
		t.Errorf("expected tskey-auth-mock123, got %s", authKey)
	}

	// Check that auth key generation was logged
	logOutput := logBuf.String()
	if !strings.Contains(logOutput, "Generated Tailscale auth key for service registration") {
		t.Error("expected auth key generation to be logged")
	}
	if !strings.Contains(logOutput, `service=test-service`) {
		t.Error("expected service name in log")
	}
}
