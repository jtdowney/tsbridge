// Package funnel provides utilities for extracting real client IPs from Tailscale Funnel connections.
package funnel

import (
	"context"
	"crypto/tls"
	"net"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"tailscale.com/ipn"
)

func TestConnContext_FunnelConn(t *testing.T) {
	realClientIP := netip.MustParseAddrPort("203.0.113.42:52341")
	funnelConn := &ipn.FunnelConn{
		Conn: &mockConn{remoteAddr: &net.TCPAddr{IP: net.ParseIP("100.81.217.1"), Port: 12345}},
		Src:  realClientIP,
	}
	tlsConn := tls.Client(funnelConn, &tls.Config{InsecureSkipVerify: true}) //nolint:gosec

	ctx := ConnContext(context.Background(), tlsConn)
	got, ok := SourceAddrFromContext(ctx)

	require.True(t, ok)
	assert.Equal(t, realClientIP, got)
}

func TestConnContext_NonFunnelConn(t *testing.T) {
	plainConn := &mockConn{remoteAddr: &net.TCPAddr{IP: net.ParseIP("100.64.1.1"), Port: 12345}}
	tlsConn := tls.Client(plainConn, &tls.Config{InsecureSkipVerify: true}) //nolint:gosec

	ctx := ConnContext(context.Background(), tlsConn)
	_, ok := SourceAddrFromContext(ctx)

	assert.False(t, ok)
}

func TestConnContext_RawConn(t *testing.T) {
	plainConn := &mockConn{remoteAddr: &net.TCPAddr{IP: net.ParseIP("192.168.1.1"), Port: 80}}

	ctx := ConnContext(context.Background(), plainConn)
	_, ok := SourceAddrFromContext(ctx)

	assert.False(t, ok)
}

type mockConn struct {
	net.Conn
	remoteAddr net.Addr
}

func (c *mockConn) RemoteAddr() net.Addr { return c.remoteAddr }
