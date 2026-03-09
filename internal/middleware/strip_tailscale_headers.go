// Package middleware implements HTTP middleware for request processing.
package middleware

import (
	"net/http"
	"strings"
)

// StripTailscaleHeaders returns middleware that removes all X-Tailscale-*
// headers from incoming requests to prevent identity spoofing. This must
// run before any middleware that sets these headers (e.g., Whois).
func StripTailscaleHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for key := range r.Header {
			if strings.HasPrefix(strings.ToLower(key), "x-tailscale-") {
				delete(r.Header, key)
			}
		}
		next.ServeHTTP(w, r)
	})
}
