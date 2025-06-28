package middleware_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"github.com/jtdowney/tsbridge/internal/middleware"
	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/tailcfg"
)

// mockWhoisClient implements middleware.WhoisClient for testing
type mockWhoisClient struct {
	response *apitype.WhoIsResponse
	err      error
}

func (m *mockWhoisClient) WhoIs(ctx context.Context, remoteAddr string) (*apitype.WhoIsResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.response, nil
}

// TestWhois_PreservesAllHeaders tests that the Whois middleware correctly
// sets all expected headers when given a full WhoIsResponse
func TestWhois_PreservesAllHeaders(t *testing.T) {
	// Create a full response with all fields populated
	addr1 := netip.MustParsePrefix("100.64.0.1/32")
	addr2 := netip.MustParsePrefix("fd7a:115c:a1e0::1/128")

	client := &mockWhoisClient{
		response: &apitype.WhoIsResponse{
			UserProfile: &tailcfg.UserProfile{
				LoginName:   "user@example.com",
				DisplayName: "Test User",
			},
			Node: &tailcfg.Node{
				Name:      "test-node",
				Addresses: []netip.Prefix{addr1, addr2},
			},
		},
	}

	// Create middleware
	m := middleware.Whois(client, true, 0, nil)

	// Create a test handler that captures the headers
	var capturedHeaders http.Header
	handler := m(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedHeaders = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	}))

	// Create a test request
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "100.64.0.1:12345"
	resp := httptest.NewRecorder()

	// Execute the middleware
	handler.ServeHTTP(resp, req)

	// Verify all expected headers are set
	expectedHeaders := map[string]string{
		"X-Tailscale-User":      "user@example.com",
		"X-Tailscale-Login":     "user@example.com",
		"X-Tailscale-Name":      "Test User",
		"X-Tailscale-Addresses": "100.64.0.1,fd7a:115c:a1e0::1",
	}

	for header, expected := range expectedHeaders {
		actual := capturedHeaders.Get(header)
		if actual != expected {
			t.Errorf("Header %s = %q, want %q", header, actual, expected)
		}
	}
}

// TestWhois_HandlesPartialResponse tests that the middleware gracefully
// handles responses with missing fields
func TestWhois_HandlesPartialResponse(t *testing.T) {
	testCases := []struct {
		name     string
		response *apitype.WhoIsResponse
		expected map[string]string
	}{
		{
			name: "only login name",
			response: &apitype.WhoIsResponse{
				UserProfile: &tailcfg.UserProfile{
					LoginName: "user@example.com",
				},
			},
			expected: map[string]string{
				"X-Tailscale-User":  "user@example.com",
				"X-Tailscale-Login": "user@example.com",
			},
		},
		{
			name: "only display name",
			response: &apitype.WhoIsResponse{
				UserProfile: &tailcfg.UserProfile{
					DisplayName: "Test User",
				},
			},
			expected: map[string]string{
				"X-Tailscale-Name": "Test User",
			},
		},
		{
			name: "only addresses",
			response: &apitype.WhoIsResponse{
				Node: &tailcfg.Node{
					Addresses: []netip.Prefix{
						netip.MustParsePrefix("100.64.0.1/32"),
					},
				},
			},
			expected: map[string]string{
				"X-Tailscale-Addresses": "100.64.0.1",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			client := &mockWhoisClient{response: tc.response}
			m := middleware.Whois(client, true, 0, nil)

			var capturedHeaders http.Header
			handler := m(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				capturedHeaders = r.Header.Clone()
				w.WriteHeader(http.StatusOK)
			}))

			req := httptest.NewRequest("GET", "/", nil)
			req.RemoteAddr = "100.64.0.1:12345"
			resp := httptest.NewRecorder()

			handler.ServeHTTP(resp, req)

			// Check expected headers are set
			for header, expected := range tc.expected {
				actual := capturedHeaders.Get(header)
				if actual != expected {
					t.Errorf("Header %s = %q, want %q", header, actual, expected)
				}
			}

			// Check unexpected headers are not set
			allHeaders := []string{
				"X-Tailscale-User",
				"X-Tailscale-Login",
				"X-Tailscale-Name",
				"X-Tailscale-Addresses",
			}
			for _, header := range allHeaders {
				if _, shouldExist := tc.expected[header]; !shouldExist {
					if val := capturedHeaders.Get(header); val != "" {
						t.Errorf("Header %s should not be set, but got %q", header, val)
					}
				}
			}
		})
	}
}
