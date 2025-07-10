package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"net/netip"

	"github.com/jtdowney/tsbridge/internal/constants"
	"github.com/stretchr/testify/assert"
	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/tailcfg"
)

// MockWhoisClient simulates tsnet.Server's WhoIs functionality
type MockWhoisClient struct {
	WhoIsFunc func(ctx context.Context, remoteAddr string) (*apitype.WhoIsResponse, error)
}

func (m *MockWhoisClient) WhoIs(ctx context.Context, remoteAddr string) (*apitype.WhoIsResponse, error) {
	if m.WhoIsFunc != nil {
		return m.WhoIsFunc(ctx, remoteAddr)
	}
	return nil, errors.New("not implemented")
}

func TestWhoisMiddleware(t *testing.T) {
	tests := []struct {
		name          string
		enabled       bool
		timeout       time.Duration
		whoisFunc     func(ctx context.Context, remoteAddr string) (*apitype.WhoIsResponse, error)
		wantHeaders   map[string]string
		wantNoHeaders []string
		wantStatus    int
	}{
		{
			name:    "whois disabled",
			enabled: false,
			timeout: 100 * time.Millisecond,
			whoisFunc: func(ctx context.Context, remoteAddr string) (*apitype.WhoIsResponse, error) {
				return &apitype.WhoIsResponse{
					UserProfile: &tailcfg.UserProfile{
						LoginName:   "user@example.com",
						DisplayName: "Test User",
					},
				}, nil
			},
			wantNoHeaders: []string{"X-Tailscale-User", "X-Tailscale-Name", "X-Tailscale-Login"},
			wantStatus:    http.StatusOK,
		},
		{
			name:    "whois success",
			enabled: true,
			timeout: 100 * time.Millisecond,
			whoisFunc: func(ctx context.Context, remoteAddr string) (*apitype.WhoIsResponse, error) {
				return &apitype.WhoIsResponse{
					UserProfile: &tailcfg.UserProfile{
						LoginName:   "user@example.com",
						DisplayName: "Test User",
						ID:          12345,
					},
					Node: &tailcfg.Node{
						ID:   67890,
						Name: "test-node",
						Addresses: []netip.Prefix{
							netip.MustParsePrefix("100.64.1.2/32"),
							netip.MustParsePrefix("fd7a:115c:a1e0::1/128"),
						},
					},
				}, nil
			},
			wantHeaders: map[string]string{
				"X-Tailscale-User":      "user@example.com",
				"X-Tailscale-Name":      "Test User",
				"X-Tailscale-Login":     "user@example.com",
				"X-Tailscale-Addresses": "100.64.1.2,fd7a:115c:a1e0::1",
			},
			wantStatus: http.StatusOK,
		},
		{
			name:    "whois timeout",
			enabled: true,
			timeout: 50 * time.Millisecond,
			whoisFunc: func(ctx context.Context, remoteAddr string) (*apitype.WhoIsResponse, error) {
				// Simulate slow whois by waiting longer than timeout
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(200 * time.Millisecond):
					return &apitype.WhoIsResponse{
						UserProfile: &tailcfg.UserProfile{
							LoginName: "user@example.com",
						},
					}, nil
				}
			},
			wantNoHeaders: []string{"X-Tailscale-User", "X-Tailscale-Name", "X-Tailscale-Login"},
			wantStatus:    http.StatusOK,
		},
		{
			name:    "whois error",
			enabled: true,
			timeout: 100 * time.Millisecond,
			whoisFunc: func(ctx context.Context, remoteAddr string) (*apitype.WhoIsResponse, error) {
				return nil, errors.New("whois lookup failed")
			},
			wantNoHeaders: []string{"X-Tailscale-User", "X-Tailscale-Name", "X-Tailscale-Login"},
			wantStatus:    http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mock whois client
			whoisClient := &MockWhoisClient{
				WhoIsFunc: tt.whoisFunc,
			}

			// Create test handler that echoes the headers
			nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Copy whois headers from request to response for testing
				for _, header := range []string{"X-Tailscale-User", "X-Tailscale-Name", "X-Tailscale-Login", "X-Tailscale-Addresses"} {
					if value := r.Header.Get(header); value != "" {
						w.Header().Set(header, value)
					}
				}
				w.WriteHeader(http.StatusOK)
			})

			// Create middleware without cache
			middleware := Whois(whoisClient, tt.enabled, tt.timeout, 0, 0)
			handler := middleware(nextHandler)

			// Create test request
			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			req.RemoteAddr = "100.64.1.2:12345"
			w := httptest.NewRecorder()

			// Execute request
			handler.ServeHTTP(w, req)

			// Check status
			if w.Code != tt.wantStatus {
				t.Errorf("got status %d, want %d", w.Code, tt.wantStatus)
			}

			// Check expected headers
			for header, want := range tt.wantHeaders {
				got := w.Header().Get(header)
				if got != want {
					t.Errorf("header %s = %q, want %q", header, got, want)
				}
			}

			// Check headers that should not be present
			for _, header := range tt.wantNoHeaders {
				if got := w.Header().Get(header); got != "" {
					t.Errorf("header %s = %q, want empty", header, got)
				}
			}
		})
	}
}

func TestWhoisCaching(t *testing.T) {
	lookupCount := 0
	whoisFunc := func(ctx context.Context, remoteAddr string) (*apitype.WhoIsResponse, error) {
		lookupCount++
		return &apitype.WhoIsResponse{
			UserProfile: &tailcfg.UserProfile{
				LoginName:   "user@example.com",
				DisplayName: "Test User",
			},
		}, nil
	}

	whoisClient := &MockWhoisClient{
		WhoIsFunc: whoisFunc,
	}

	middleware := Whois(whoisClient, true, 100*time.Millisecond, 100, 5*time.Minute)

	nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := middleware(nextHandler)

	req1 := httptest.NewRequest(http.MethodGet, "/test", nil)
	req1.RemoteAddr = "100.64.1.2:12345"
	w1 := httptest.NewRecorder()
	handler.ServeHTTP(w1, req1)

	if lookupCount != 1 {
		t.Errorf("First request: lookupCount = %d, want 1", lookupCount)
	}

	req2 := httptest.NewRequest(http.MethodGet, "/test", nil)
	req2.RemoteAddr = "100.64.1.2:12345"
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, req2)

	if lookupCount != 1 {
		t.Errorf("Second request: lookupCount = %d, want 1 (should use cache)", lookupCount)
	}

	req3 := httptest.NewRequest(http.MethodGet, "/test", nil)
	req3.RemoteAddr = "100.64.1.3:12345"
	w3 := httptest.NewRecorder()
	handler.ServeHTTP(w3, req3)

	if lookupCount != 2 {
		t.Errorf("Third request: lookupCount = %d, want 2", lookupCount)
	}
}

func TestWhoisHeaderInjectionPrevention(t *testing.T) {
	tests := []struct {
		name        string
		userProfile *tailcfg.UserProfile
		wantHeaders map[string]string
		description string
	}{
		{
			name: "header injection with newline in LoginName",
			userProfile: &tailcfg.UserProfile{
				LoginName:     "user@example.com\r\nX-Injected: evil",
				DisplayName:   "Normal User",
				ProfilePicURL: "https://example.com/pic.jpg",
			},
			wantHeaders: map[string]string{
				"X-Tailscale-User":            "user@example.comX-Injected: evil",
				"X-Tailscale-Login":           "user@example.comX-Injected: evil",
				"X-Tailscale-Name":            "Normal User",
				"X-Tailscale-Profile-Picture": "https://example.com/pic.jpg",
			},
			description: "Newlines should be removed from LoginName",
		},
		{
			name: "header injection with CRLF in DisplayName",
			userProfile: &tailcfg.UserProfile{
				LoginName:     "user@example.com",
				DisplayName:   "Evil\r\nX-Malicious: true\r\nUser",
				ProfilePicURL: "https://example.com/pic.jpg",
			},
			wantHeaders: map[string]string{
				"X-Tailscale-User":            "user@example.com",
				"X-Tailscale-Login":           "user@example.com",
				"X-Tailscale-Name":            "EvilX-Malicious: trueUser",
				"X-Tailscale-Profile-Picture": "https://example.com/pic.jpg",
			},
			description: "CRLF sequences should be removed from DisplayName",
		},
		{
			name: "header injection in ProfilePicURL",
			userProfile: &tailcfg.UserProfile{
				LoginName:     "user@example.com",
				DisplayName:   "Test User",
				ProfilePicURL: "https://example.com/pic.jpg\r\nX-Hack: yes",
			},
			wantHeaders: map[string]string{
				"X-Tailscale-User":            "user@example.com",
				"X-Tailscale-Login":           "user@example.com",
				"X-Tailscale-Name":            "Test User",
				"X-Tailscale-Profile-Picture": "https://example.com/pic.jpgX-Hack: yes",
			},
			description: "Newlines should be removed from ProfilePicURL",
		},
		{
			name: "multiple injection attempts",
			userProfile: &tailcfg.UserProfile{
				LoginName:     "bad\r\nX-Bad1: true",
				DisplayName:   "also\nbad\r\nX-Bad2: yes",
				ProfilePicURL: "https://evil.com\r\n\r\nX-Bad3: absolutely",
			},
			wantHeaders: map[string]string{
				"X-Tailscale-User":            "badX-Bad1: true",
				"X-Tailscale-Login":           "badX-Bad1: true",
				"X-Tailscale-Name":            "alsobadX-Bad2: yes",
				"X-Tailscale-Profile-Picture": "https://evil.comX-Bad3: absolutely",
			},
			description: "All injection attempts should be sanitized",
		},
		{
			name: "profile picture URL should be included",
			userProfile: &tailcfg.UserProfile{
				LoginName:     "user@example.com",
				DisplayName:   "Test User",
				ProfilePicURL: "https://example.com/profile.jpg",
			},
			wantHeaders: map[string]string{
				"X-Tailscale-User":            "user@example.com",
				"X-Tailscale-Login":           "user@example.com",
				"X-Tailscale-Name":            "Test User",
				"X-Tailscale-Profile-Picture": "https://example.com/profile.jpg",
			},
			description: "Profile picture URL should be added as a header",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			whoisClient := &MockWhoisClient{
				WhoIsFunc: func(ctx context.Context, remoteAddr string) (*apitype.WhoIsResponse, error) {
					return &apitype.WhoIsResponse{
						UserProfile: tt.userProfile,
					}, nil
				},
			}

			nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				for _, header := range []string{"X-Tailscale-User", "X-Tailscale-Login", "X-Tailscale-Name", "X-Tailscale-Profile-Picture"} {
					if value := r.Header.Get(header); value != "" {
						w.Header().Set(header, value)
					}
				}
				w.WriteHeader(http.StatusOK)
			})

			middleware := Whois(whoisClient, true, 100*time.Millisecond, 0, 0)
			handler := middleware(nextHandler)

			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			req.RemoteAddr = "100.64.1.2:12345"
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			for header, want := range tt.wantHeaders {
				got := w.Header().Get(header)
				if got != want {
					t.Errorf("%s: header %s = %q, want %q", tt.description, header, got, want)
				}
			}

			// Ensure no injected headers were added
			for _, injectedHeader := range []string{"X-Injected", "X-Malicious", "X-Hack", "X-Bad1", "X-Bad2", "X-Bad3"} {
				if got := w.Header().Get(injectedHeader); got != "" {
					t.Errorf("Injected header %s should not exist, but got %q", injectedHeader, got)
				}
			}
		})
	}
}

func TestSanitizeHeaderValue(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "no special characters",
			input: "normal-value",
			want:  "normal-value",
		},
		{
			name:  "carriage return",
			input: "value\rwith\rCR",
			want:  "valuewithCR",
		},
		{
			name:  "line feed",
			input: "value\nwith\nLF",
			want:  "valuewithLF",
		},
		{
			name:  "CRLF sequence",
			input: "value\r\nwith\r\nCRLF",
			want:  "valuewithCRLF",
		},
		{
			name:  "mixed sequences",
			input: "complex\rvalue\nwith\r\nmixed",
			want:  "complexvaluewithmixed",
		},
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
		{
			name:  "only newlines",
			input: "\r\n\r\n",
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeHeaderValue(tt.input)
			if got != tt.want {
				t.Errorf("sanitizeHeaderValue(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// TestWhois_PreservesAllHeaders tests that the Whois middleware correctly
// sets all expected headers when given a full WhoIsResponse
func TestWhois_PreservesAllHeaders(t *testing.T) {
	// Create a full response with all fields populated
	addr1 := netip.MustParsePrefix("100.64.0.1/32")
	addr2 := netip.MustParsePrefix("fd7a:115c:a1e0::1/128")

	client := &MockWhoisClient{
		WhoIsFunc: func(ctx context.Context, remoteAddr string) (*apitype.WhoIsResponse, error) {
			return &apitype.WhoIsResponse{
				UserProfile: &tailcfg.UserProfile{
					LoginName:   "user@example.com",
					DisplayName: "Test User",
				},
				Node: &tailcfg.Node{
					Name:      "test-node",
					Addresses: []netip.Prefix{addr1, addr2},
				},
			}, nil
		},
	}

	// Create middleware
	m := Whois(client, true, 0, 0, 0)

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
			client := &MockWhoisClient{
				WhoIsFunc: func(ctx context.Context, remoteAddr string) (*apitype.WhoIsResponse, error) {
					return tc.response, nil
				},
			}
			m := Whois(client, true, 0, 0, 0)

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

func TestWhoisRetryBehavior(t *testing.T) {
	tests := []struct {
		name          string
		failureCount  int
		expectedCalls int
		shouldSucceed bool
		timeout       time.Duration
	}{
		{
			name:          "succeeds immediately",
			failureCount:  0,
			expectedCalls: 1,
			shouldSucceed: true,
			timeout:       1 * time.Second,
		},
		{
			name:          "succeeds after 2 failures",
			failureCount:  2,
			expectedCalls: 3,
			shouldSucceed: true,
			timeout:       5 * time.Second, // Longer timeout for retries
		},
		{
			name:          "fails after max retries",
			failureCount:  5,
			expectedCalls: 3, // Default max attempts
			shouldSucceed: false,
			timeout:       5 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			callCount := 0

			whoisClient := &MockWhoisClient{
				WhoIsFunc: func(ctx context.Context, remoteAddr string) (*apitype.WhoIsResponse, error) {
					callCount++

					// Fail for the first N calls, then succeed
					if callCount <= tt.failureCount {
						return nil, errors.New("connection refused")
					}

					// Success response
					return &apitype.WhoIsResponse{
						UserProfile: &tailcfg.UserProfile{
							LoginName:   "user@example.com",
							DisplayName: "Test User",
						},
					}, nil
				},
			}

			nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			})

			// Use whois middleware (now includes retry)
			middleware := Whois(whoisClient, true, tt.timeout, 0, 0)
			handler := middleware(nextHandler)

			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			req.RemoteAddr = "100.64.1.2:12345"
			w := httptest.NewRecorder()

			start := time.Now()
			handler.ServeHTTP(w, req)
			duration := time.Since(start)

			// Verify expected number of calls
			assert.Equal(t, tt.expectedCalls, callCount)

			// Verify headers are set on success
			if tt.shouldSucceed {
				assert.Equal(t, "user@example.com", req.Header.Get("X-Tailscale-User"))
			} else {
				assert.Empty(t, req.Header.Get("X-Tailscale-User"))
			}

			// Verify retry timing (should have delays for retries)
			if tt.expectedCalls > 1 {
				minExpectedDuration := time.Duration(tt.expectedCalls-1) * constants.RetryMinTestDelay
				assert.GreaterOrEqual(t, duration, minExpectedDuration)
			}
		})
	}
}
