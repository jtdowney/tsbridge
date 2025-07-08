package integration

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jtdowney/tsbridge/internal/config"
	"github.com/jtdowney/tsbridge/internal/service"
	"github.com/jtdowney/tsbridge/internal/testhelpers"
)

func TestServiceWithHeaderConfiguration(t *testing.T) {
	// Create a test backend that echoes headers
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Echo request headers back as response headers with X-Echo- prefix
		for key, values := range r.Header {
			if len(values) > 0 {
				w.Header().Set("X-Echo-"+key, values[0])
			}
		}

		// Add some backend-specific headers
		w.Header().Set("X-Backend-Version", "1.0")
		w.Header().Set("Server", "test-backend")
		w.Header().Set("X-Internal-Debug", "sensitive-data")

		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	}))
	defer backend.Close()

	tests := []struct {
		name                string
		serviceConfig       config.Service
		requestHeaders      map[string]string
		expectedReqEcho     map[string]string // Headers we expect the backend to have received
		expectedRespHeaders map[string]string // Headers we expect in the response
		notExpectedResp     []string          // Headers that should NOT be in the response
	}{
		{
			name: "add upstream headers",
			serviceConfig: config.Service{
				Name:        "test-service",
				BackendAddr: backend.URL,
				UpstreamHeaders: map[string]string{
					"X-Service-Name": "tsbridge",
					"X-Request-ID":   "test-123",
				},
			},
			requestHeaders: map[string]string{
				"User-Agent": "test-client",
			},
			expectedReqEcho: map[string]string{
				"X-Echo-X-Service-Name": "tsbridge",
				"X-Echo-X-Request-ID":   "test-123",
				"X-Echo-User-Agent":     "test-client",
			},
			expectedRespHeaders: map[string]string{
				"X-Backend-Version": "1.0",
				"Server":            "test-backend",
			},
		},
		{
			name: "remove upstream headers",
			serviceConfig: config.Service{
				Name:           "test-service",
				BackendAddr:    backend.URL,
				RemoveUpstream: []string{"Authorization", "Cookie"},
			},
			requestHeaders: map[string]string{
				"Authorization": "Bearer secret-token",
				"Cookie":        "session=secret",
				"User-Agent":    "test-client",
			},
			expectedReqEcho: map[string]string{
				"X-Echo-User-Agent": "test-client",
			},
			expectedRespHeaders: map[string]string{
				"X-Backend-Version": "1.0",
			},
		},
		{
			name: "add downstream headers",
			serviceConfig: config.Service{
				Name:        "test-service",
				BackendAddr: backend.URL,
				DownstreamHeaders: map[string]string{
					"X-Powered-By":    "tsbridge",
					"X-Cache-Control": "no-cache",
				},
			},
			expectedRespHeaders: map[string]string{
				"X-Powered-By":      "tsbridge",
				"X-Cache-Control":   "no-cache",
				"X-Backend-Version": "1.0",
			},
		},
		{
			name: "remove downstream headers",
			serviceConfig: config.Service{
				Name:             "test-service",
				BackendAddr:      backend.URL,
				RemoveDownstream: []string{"Server", "X-Internal-Debug"},
			},
			expectedRespHeaders: map[string]string{
				"X-Backend-Version": "1.0",
			},
			notExpectedResp: []string{"Server", "X-Internal-Debug"},
		},
		{
			name: "combined header manipulation",
			serviceConfig: config.Service{
				Name:        "test-service",
				BackendAddr: backend.URL,
				UpstreamHeaders: map[string]string{
					"X-Service": "tsbridge",
				},
				RemoveUpstream: []string{"Authorization"},
				DownstreamHeaders: map[string]string{
					"X-Response-Time": "fast",
				},
				RemoveDownstream: []string{"Server"},
			},
			requestHeaders: map[string]string{
				"Authorization": "Bearer token",
				"Accept":        "application/json",
			},
			expectedReqEcho: map[string]string{
				"X-Echo-X-Service": "tsbridge",
				"X-Echo-Accept":    "application/json",
			},
			expectedRespHeaders: map[string]string{
				"X-Response-Time":   "fast",
				"X-Backend-Version": "1.0",
			},
			notExpectedResp: []string{"Server"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create service with the test configuration
			svc := &service.Service{
				Config: tt.serviceConfig,
			}

			// Create handler
			handler, err := svc.CreateHandler()
			require.NoError(t, err)

			// Create test request
			req := httptest.NewRequest("GET", "http://example.com/test", nil)
			for key, value := range tt.requestHeaders {
				req.Header.Set(key, value)
			}

			// Make request
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)

			// Check response status
			assert.Equal(t, http.StatusOK, w.Code)

			// Check expected request headers were received by backend
			for key, expected := range tt.expectedReqEcho {
				assert.Equal(t, expected, w.Header().Get(key), "backend should have received header %s", key)
			}

			// Check expected response headers
			for key, expected := range tt.expectedRespHeaders {
				assert.Equal(t, expected, w.Header().Get(key), "response header %s", key)
			}

			// Check headers that should NOT be present
			for _, key := range tt.notExpectedResp {
				assert.Empty(t, w.Header().Get(key), "header %s should have been removed", key)
			}
		})
	}
}

func TestHeaderConfigurationWithWhois(t *testing.T) {
	// Create a test backend
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Echo the whois header if present
		if whois := r.Header.Get("Tailscale-User-Login"); whois != "" {
			w.Header().Set("X-Echo-Whois", whois)
		}

		// Echo custom headers
		if custom := r.Header.Get("X-Custom"); custom != "" {
			w.Header().Set("X-Echo-Custom", custom)
		}

		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	// Create service with header configuration
	whoisEnabled := true
	svc := &service.Service{
		Config: config.Service{
			Name:         "test-service",
			BackendAddr:  backend.URL,
			WhoisEnabled: &whoisEnabled,
			WhoisTimeout: testhelpers.DurationPtr(100 * time.Millisecond),
			UpstreamHeaders: map[string]string{
				"X-Custom": "custom-value",
			},
		},
	}

	// Create handler (whois will be disabled since no tsServer)
	handler, err := svc.CreateHandler()
	require.NoError(t, err)

	// Make request
	req := httptest.NewRequest("GET", "http://example.com/test", nil)
	req.RemoteAddr = "100.64.1.1:12345" // Tailscale IP

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	// Check that custom header was added
	assert.Equal(t, "custom-value", w.Header().Get("X-Echo-Custom"))

	// Whois header won't be present since we don't have a real tsServer
	assert.Empty(t, w.Header().Get("X-Echo-Whois"))
}

func TestHeaderConfigurationValidation(t *testing.T) {
	// Test that header configuration doesn't break service validation
	cfg := &config.Config{
		Tailscale: config.Tailscale{
			AuthKey: "tskey-auth-test123",
		},
		Services: []config.Service{
			{
				Name:        "test-service",
				BackendAddr: "localhost:8080",
				UpstreamHeaders: map[string]string{
					"X-Custom-1": "value1",
					"X-Custom-2": "value2",
				},
				DownstreamHeaders: map[string]string{
					"X-Response-1": "resp1",
				},
				RemoveUpstream:   []string{"Cookie", "Authorization"},
				RemoveDownstream: []string{"Server", "X-Powered-By"},
			},
		},
	}

	// Set defaults and normalize before validation
	cfg.SetDefaults()
	cfg.Normalize()

	// Should validate successfully
	err := cfg.Validate("")
	assert.NoError(t, err)
}
