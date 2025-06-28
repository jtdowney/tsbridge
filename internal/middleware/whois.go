// Package middleware implements HTTP middleware for request processing.
package middleware

import (
	"context"
	"net/http"
	"strings"
	"time"

	"log/slog"

	"github.com/hashicorp/golang-lru/v2/expirable"
	"tailscale.com/client/tailscale/apitype"
)

type WhoisClient interface {
	WhoIs(ctx context.Context, remoteAddr string) (*apitype.WhoIsResponse, error)
}

type WhoisCache struct {
	cache *expirable.LRU[string, *apitype.WhoIsResponse]
}

func NewWhoisCache(size int, ttl time.Duration) *WhoisCache {
	cache := expirable.NewLRU[string, *apitype.WhoIsResponse](size, nil, ttl)
	return &WhoisCache{cache: cache}
}

func Whois(client WhoisClient, enabled bool, timeout time.Duration, cache *WhoisCache) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !enabled {
				next.ServeHTTP(w, r)
				return
			}

			performWhoisLookup(client, timeout, r, cache)

			next.ServeHTTP(w, r)
		})
	}
}

func performWhoisLookup(client WhoisClient, timeout time.Duration, r *http.Request, cache *WhoisCache) {
	var resp *apitype.WhoIsResponse
	var err error

	if cache != nil {
		if cached, ok := cache.cache.Get(r.RemoteAddr); ok {
			resp = cached
		} else {
			ctx, cancel := context.WithTimeout(r.Context(), timeout)
			defer cancel()

			resp, err = client.WhoIs(ctx, r.RemoteAddr)
			if err != nil {
				logWhoisError(err, r.RemoteAddr, timeout)
				return
			}

			if resp != nil {
				cache.cache.Add(r.RemoteAddr, resp)
			}
		}
	} else {
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()

		resp, err = client.WhoIs(ctx, r.RemoteAddr)
		if err != nil {
			logWhoisError(err, r.RemoteAddr, timeout)
			return
		}
	}

	if resp != nil {
		addUserHeaders(r, resp)
		addAddressHeaders(r, resp)
	}
}

// logWhoisError logs the appropriate error message based on the error type
func logWhoisError(err error, remoteAddr string, timeout time.Duration) {
	if err == context.DeadlineExceeded {
		slog.Warn("whois lookup timed out", "remote_addr", remoteAddr, "timeout", timeout)
	} else {
		slog.Warn("whois lookup failed", "remote_addr", remoteAddr, "error", err)
	}
}

// addUserHeaders adds user-related headers from the whois response
func addUserHeaders(r *http.Request, resp *apitype.WhoIsResponse) {
	if resp.UserProfile == nil {
		return
	}

	if resp.UserProfile.LoginName != "" {
		r.Header.Set("X-Tailscale-User", resp.UserProfile.LoginName)
		r.Header.Set("X-Tailscale-Login", resp.UserProfile.LoginName)
	}
	if resp.UserProfile.DisplayName != "" {
		r.Header.Set("X-Tailscale-Name", resp.UserProfile.DisplayName)
	}
}

// addAddressHeaders adds address-related headers from the whois response
func addAddressHeaders(r *http.Request, resp *apitype.WhoIsResponse) {
	if resp.Node == nil || len(resp.Node.Addresses) == 0 {
		return
	}

	// Convert prefixes to IP addresses and join with comma
	var addresses []string
	for _, prefix := range resp.Node.Addresses {
		addresses = append(addresses, prefix.Addr().String())
	}
	r.Header.Set("X-Tailscale-Addresses", strings.Join(addresses, ","))
}
