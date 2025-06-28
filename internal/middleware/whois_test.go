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
