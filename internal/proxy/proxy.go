// Package proxy implements reverse proxy functionality for forwarding requests to backend services.
package proxy

import (
	"context"
	goerrors "errors"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"syscall"
	"time"

	"github.com/jtdowney/tsbridge/internal/errors"
	"github.com/jtdowney/tsbridge/internal/middleware"
)

// TransportConfig holds configuration for the HTTP transport
type TransportConfig struct {
	ResponseHeaderTimeout time.Duration
	DialTimeout           time.Duration
	KeepAliveTimeout      time.Duration
	IdleConnTimeout       time.Duration
	TLSHandshakeTimeout   time.Duration
	ExpectContinueTimeout time.Duration
}

// Handler is the interface for all proxy handlers
type Handler interface {
	http.Handler
}

// HTTPHandler implements HTTP reverse proxy
type httpHandler struct {
	proxy          *httputil.ReverseProxy
	backendAddr    string
	trustedProxies []*net.IPNet
	// Header manipulation
	upstreamHeaders   map[string]string
	downstreamHeaders map[string]string
	removeUpstream    []string
	removeDownstream  []string
}

// NewHandler creates a new HTTP reverse proxy handler with custom timeout and trusted proxy configuration
func NewHandler(backendAddr string, transportConfig *TransportConfig, trustedProxies []string) (Handler, error) {
	// Use NewHandlerWithHeaders with nil header configurations
	return NewHandlerWithHeaders(backendAddr, transportConfig, trustedProxies, nil, nil, nil, nil)
}

// NewHandlerWithHeaders creates a new proxy handler with header manipulation support
func NewHandlerWithHeaders(
	backendAddr string,
	transportConfig *TransportConfig,
	trustedProxies []string,
	upstreamHeaders map[string]string,
	downstreamHeaders map[string]string,
	removeUpstream []string,
	removeDownstream []string,
) (Handler, error) {
	h := &httpHandler{
		backendAddr:       backendAddr,
		upstreamHeaders:   upstreamHeaders,
		downstreamHeaders: downstreamHeaders,
		removeUpstream:    removeUpstream,
		removeDownstream:  removeDownstream,
	}

	// Parse trusted proxies
	if err := configureTrustedProxies(h, trustedProxies); err != nil {
		return nil, err
	}

	// Parse backend URL
	target, err := parseBackendURL(backendAddr)
	if err != nil {
		return nil, errors.WrapConfig(err, "invalid backend address")
	}

	// Create reverse proxy using NewSingleHostReverseProxy for simplicity
	h.proxy = httputil.NewSingleHostReverseProxy(target)

	// Configure director
	originalDirector := h.proxy.Director
	h.proxy.Director = createProxyDirector(h, originalDirector)

	// Configure transport
	h.proxy.Transport = createProxyTransport(backendAddr, transportConfig)

	// Configure ModifyResponse to handle downstream headers
	h.proxy.ModifyResponse = func(resp *http.Response) error {
		// Remove headers specified in removeDownstream
		for _, header := range h.removeDownstream {
			resp.Header.Del(header)
		}

		// Add/override headers specified in downstreamHeaders
		for key, value := range h.downstreamHeaders {
			resp.Header.Set(key, value)
		}

		return nil
	}

	// Configure error handler
	h.proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		// Drain request body to free resources
		if r.Body != nil {
			_, _ = io.Copy(io.Discard, r.Body)
			r.Body.Close()
		}

		// Wrap as network error for internal use
		networkErr := errors.WrapNetwork(err, "proxy request failed")

		// Log with request ID from context
		logger := middleware.LogWithRequestID(r.Context())
		logger.Error("proxy error", "backend", backendAddr, "path", r.URL.Path, "error", networkErr)

		// Determine status code and message
		status := http.StatusBadGateway
		message := "Bad Gateway"

		// Check for timeout errors using proper type assertion
		if isTimeoutError(err) {
			status = http.StatusGatewayTimeout
			message = "Gateway Timeout"
		}

		http.Error(w, message, status)
	}

	return h, nil
}

func (h *httpHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.proxy.ServeHTTP(w, r)
}

// isTrustedProxy checks if the given IP is from a trusted proxy
func (h *httpHandler) isTrustedProxy(ip string) bool {
	// If no trusted proxies are configured, no proxy is trusted
	if len(h.trustedProxies) == 0 {
		return false
	}

	parsedIP := net.ParseIP(ip)
	if parsedIP == nil {
		return false
	}

	// Check if IP is in any trusted range
	for _, trustedNet := range h.trustedProxies {
		if trustedNet.Contains(parsedIP) {
			return true
		}
	}

	return false
}

// isTimeoutError checks if an error represents a timeout using proper type assertions
func isTimeoutError(err error) bool {
	// Check for context deadline
	if err == context.DeadlineExceeded {
		return true
	}

	// Check if error implements net.Error interface
	var netErr net.Error
	if goerrors.As(err, &netErr) && netErr.Timeout() {
		return true
	}

	// Check for syscall timeout errors
	if goerrors.Is(err, syscall.ETIMEDOUT) {
		return true
	}

	return false
}

// configureTrustedProxies parses and configures trusted proxy settings
func configureTrustedProxies(h *httpHandler, trustedProxies []string) error {
	for _, proxy := range trustedProxies {
		// Check if it's a CIDR range
		if strings.Contains(proxy, "/") {
			_, ipNet, err := net.ParseCIDR(proxy)
			if err != nil {
				return errors.WrapConfig(err, "invalid trusted proxy CIDR")
			}
			h.trustedProxies = append(h.trustedProxies, ipNet)
		} else {
			// Single IP address
			ip := net.ParseIP(proxy)
			if ip == nil {
				return errors.NewConfigError("invalid trusted proxy IP: " + proxy)
			}
			// Convert single IP to /32 or /128 CIDR
			mask := net.CIDRMask(32, 32)
			if ip.To4() == nil {
				mask = net.CIDRMask(128, 128)
			}
			h.trustedProxies = append(h.trustedProxies, &net.IPNet{IP: ip, Mask: mask})
		}
	}
	return nil
}

// createProxyDirector creates the director function for the reverse proxy
func createProxyDirector(h *httpHandler, originalDirector func(*http.Request)) func(*http.Request) {
	return func(req *http.Request) {
		// Call original director to set up the request
		originalDirector(req)

		// Get the remote IP
		clientIP, _, _ := net.SplitHostPort(req.RemoteAddr)

		// Check if request is from a trusted proxy
		fromTrustedProxy := h.isTrustedProxy(clientIP)

		// Handle X-Forwarded-For based on trust
		existingXFF := req.Header.Get("X-Forwarded-For")

		// Always delete X-Real-IP to prevent spoofing
		req.Header.Del("X-Real-IP")

		if fromTrustedProxy && existingXFF != "" {
			// Request is from trusted proxy with existing X-Forwarded-For
			// Keep the existing header, ReverseProxy will append the current proxy IP

			// Extract the real client IP (first in the chain)
			ips := strings.Split(existingXFF, ",")
			if len(ips) > 0 {
				realIP := strings.TrimSpace(ips[0])
				req.Header.Set("X-Real-IP", realIP)
			}
		} else {
			// Request is not from trusted proxy or no existing X-Forwarded-For
			// Delete any existing X-Forwarded-For to prevent spoofing
			req.Header.Del("X-Forwarded-For")

			// ReverseProxy will add the immediate client IP
			if clientIP != "" {
				req.Header.Set("X-Real-IP", clientIP)
			}
		}

		// Set X-Forwarded-Proto
		if req.TLS != nil {
			req.Header.Set("X-Forwarded-Proto", "https")
		} else {
			req.Header.Set("X-Forwarded-Proto", "http")
		}

		// Remove headers specified in removeUpstream
		for _, header := range h.removeUpstream {
			req.Header.Del(header)
		}

		// Add/override headers specified in upstreamHeaders
		for key, value := range h.upstreamHeaders {
			req.Header.Set(key, value)
		}
	}
}

// createProxyTransport creates the transport for the reverse proxy
func createProxyTransport(backendAddr string, config *TransportConfig) *http.Transport {
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			// Handle unix socket addresses
			if strings.HasPrefix(backendAddr, "unix://") {
				socketPath := strings.TrimPrefix(backendAddr, "unix://")
				return net.Dial("unix", socketPath)
			}
			// Regular TCP dial
			d := net.Dialer{
				Timeout:   config.DialTimeout,
				KeepAlive: config.KeepAliveTimeout,
			}
			return d.DialContext(ctx, network, addr)
		},
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		MaxConnsPerHost:       50,
		MaxIdleConnsPerHost:   10,
		IdleConnTimeout:       config.IdleConnTimeout,
		TLSHandshakeTimeout:   config.TLSHandshakeTimeout,
		ExpectContinueTimeout: config.ExpectContinueTimeout,
	}

	if config.ResponseHeaderTimeout > 0 {
		transport.ResponseHeaderTimeout = config.ResponseHeaderTimeout
	}

	return transport
}

// parseBackendURL parses the backend address into a URL
func parseBackendURL(addr string) (*url.URL, error) {
	// Check for empty address
	if addr == "" {
		return nil, errors.NewConfigError("backend address cannot be empty")
	}

	// Handle unix socket
	if strings.HasPrefix(addr, "unix://") {
		// For unix sockets, create a dummy http URL
		// The actual dialing is handled in the transport
		return &url.URL{
			Scheme: "http",
			Host:   "unix",
		}, nil
	}

	// Add scheme if missing
	if !strings.Contains(addr, "://") {
		addr = "http://" + addr
	}

	parsedURL, err := url.Parse(addr)
	if err != nil {
		return nil, err
	}

	// Validate the URL has a host
	if parsedURL.Host == "" {
		return nil, errors.NewConfigError("backend address must have a host")
	}

	return parsedURL, nil
}
