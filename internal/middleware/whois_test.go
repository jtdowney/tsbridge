package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"net/netip"

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

			// Create middleware
			middleware := Whois(whoisClient, tt.enabled, tt.timeout, nil)
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

	cache := NewWhoisCache(100, 5*time.Minute)
	middleware := Whois(whoisClient, true, 100*time.Millisecond, cache)

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

			middleware := Whois(whoisClient, true, 100*time.Millisecond, nil)
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
