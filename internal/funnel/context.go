// Package funnel provides utilities for extracting real client IPs from Tailscale Funnel connections.
package funnel

import (
	"context"
	"crypto/tls"
	"net"
	"net/netip"

	"tailscale.com/ipn"
)

type contextKey struct{}

// ConnContext returns a context enriched with the real client source address
// when the connection is a Tailscale Funnel connection. This is intended to
// be used as http.Server.ConnContext.
func ConnContext(ctx context.Context, c net.Conn) context.Context {
	if src, ok := extractFunnelSrc(c); ok {
		return context.WithValue(ctx, contextKey{}, src)
	}
	return ctx
}

// SourceAddrFromContext returns the real client address from a Funnel
// connection, if present in the context.
func SourceAddrFromContext(ctx context.Context) (netip.AddrPort, bool) {
	v, ok := ctx.Value(contextKey{}).(netip.AddrPort)
	return v, ok
}

// WithSourceAddr returns a context with the given Funnel source address set.
// This is useful for testing and for cases where the source address is known
// outside of a ConnContext callback.
func WithSourceAddr(ctx context.Context, src netip.AddrPort) context.Context {
	return context.WithValue(ctx, contextKey{}, src)
}

// extractFunnelSrc unwraps TLS and checks for ipn.FunnelConn to get the
// real source address of a Funnel client.
func extractFunnelSrc(c net.Conn) (netip.AddrPort, bool) {
	inner := c

	// Unwrap *tls.Conn — ListenFunnel wraps connections in TLS.
	if tc, ok := inner.(*tls.Conn); ok {
		inner = tc.NetConn()
	}

	if fc, ok := inner.(*ipn.FunnelConn); ok {
		return fc.Src, fc.Src.IsValid()
	}

	return netip.AddrPort{}, false
}
