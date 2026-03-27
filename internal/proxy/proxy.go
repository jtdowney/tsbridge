// Package proxy implements reverse proxy functionality for forwarding requests to backend services.
package proxy

import (
	"context"
	"crypto/tls"
	goerrors "errors"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/jtdowney/tsbridge/internal/constants"
	"github.com/jtdowney/tsbridge/internal/errors"
	"github.com/jtdowney/tsbridge/internal/funnel"
	"github.com/jtdowney/tsbridge/internal/metrics"
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

// HandlerConfig holds all configuration options for creating a proxy handler
type HandlerConfig struct {
	BackendAddr       string
	TransportConfig   *TransportConfig
	TrustedProxies    []string
	MetricsCollector  *metrics.Collector
	ServiceName       string
	UpstreamHeaders   map[string]string
	DownstreamHeaders map[string]string
	RemoveUpstream    []string
	RemoveDownstream  []string
	// FlushInterval specifies the duration between flushes to the client.
	// If nil, defaults to 0 (standard buffering). Negative values cause immediate flushing.
	FlushInterval      *time.Duration
	InsecureSkipVerify bool
}

// Handler is the interface for all proxy handlers
type Handler interface {
	http.Handler
	Close() error
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
	// Metrics
	metricsCollector *metrics.Collector
	serviceName      string
	transport        *http.Transport
	stopMetrics      chan struct{}
	// Request tracking for metrics
	activeRequests int64
	// Streaming support
	flushInterval *time.Duration
}

// NewHandler creates a new HTTP reverse proxy handler with the provided configuration
func NewHandler(cfg *HandlerConfig) (Handler, error) {
	h := &httpHandler{
		backendAddr:       cfg.BackendAddr,
		trustedProxies:    make([]*net.IPNet, 0),
		upstreamHeaders:   cfg.UpstreamHeaders,
		downstreamHeaders: cfg.DownstreamHeaders,
		removeUpstream:    cfg.RemoveUpstream,
		removeDownstream:  cfg.RemoveDownstream,
		metricsCollector:  cfg.MetricsCollector,
		serviceName:       cfg.ServiceName,
		flushInterval:     cfg.FlushInterval,
	}

	// Parse trusted proxies
	if err := configureTrustedProxies(h, cfg.TrustedProxies); err != nil {
		return nil, err
	}

	// Parse backend URL
	target, err := parseBackendURL(cfg.BackendAddr)
	if err != nil {
		return nil, errors.WrapConfig(err, "invalid backend address")
	}

	// Create reverse proxy with Rewrite function
	flushInterval := time.Duration(0)
	if cfg.FlushInterval != nil {
		flushInterval = *cfg.FlushInterval
	}
	h.proxy = &httputil.ReverseProxy{
		Rewrite:       createProxyRewrite(h, target),
		FlushInterval: flushInterval,
	}

	// Configure transport (default to empty config if nil)
	transportConfig := cfg.TransportConfig
	if transportConfig == nil {
		transportConfig = &TransportConfig{}
	}
	h.transport = createProxyTransport(cfg.BackendAddr, transportConfig, cfg.InsecureSkipVerify)
	h.proxy.Transport = h.transport

	// Configure ModifyResponse to handle downstream headers
	h.proxy.ModifyResponse = createModifyResponse(h.removeDownstream, h.downstreamHeaders)

	// Configure error handler
	h.proxy.ErrorHandler = createErrorHandler(cfg.BackendAddr)

	// Start metrics collection if collector is provided
	if cfg.MetricsCollector != nil {
		h.startMetricsCollection()
	}

	return h, nil
}

func (h *httpHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Track active requests for metrics
	atomic.AddInt64(&h.activeRequests, 1)
	defer atomic.AddInt64(&h.activeRequests, -1)

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

// createProxyRewrite creates the Rewrite function for the reverse proxy.
// The Rewrite API strips X-Forwarded-For/Host/Proto before calling this function;
// SetXForwarded() repopulates them. For trusted proxies, we copy the inbound XFF
// first so SetXForwarded appends to the existing chain.
//
// For Funnel connections, the real client IP is stored in the request context
// by the ConnContext callback. The Tailscale ingress node is implicitly trusted,
// and the real client IP is injected into the X-Forwarded-For chain.
func createProxyRewrite(h *httpHandler, target *url.URL) func(*httputil.ProxyRequest) {
	return func(pr *httputil.ProxyRequest) {
		pr.SetURL(target)
		pr.Out.Host = pr.In.Host

		clientIP, _, _ := net.SplitHostPort(pr.In.RemoteAddr)
		fromTrustedProxy := h.isTrustedProxy(clientIP)

		// For Funnel connections, the ingress node is implicitly trusted and
		// the real client IP is injected as the start of the XFF chain.
		funnelSrc, isFunnel := funnel.SourceAddrFromContext(pr.In.Context())
		if isFunnel {
			pr.Out.Header.Set("X-Forwarded-For", funnelSrc.Addr().String())
			fromTrustedProxy = true
		} else if fromTrustedProxy {
			// For trusted proxies, copy inbound XFF so SetXForwarded appends to it
			if xff := pr.In.Header["X-Forwarded-For"]; len(xff) > 0 {
				pr.Out.Header["X-Forwarded-For"] = xff
			}
		}

		pr.SetXForwarded()

		// Set X-Real-IP
		switch {
		case isFunnel:
			pr.Out.Header.Set("X-Real-IP", funnelSrc.Addr().String())
		case fromTrustedProxy:
			if existingXFF := pr.In.Header.Get("X-Forwarded-For"); existingXFF != "" {
				ips := strings.Split(existingXFF, ",")
				if len(ips) > 0 {
					pr.Out.Header.Set("X-Real-IP", strings.TrimSpace(ips[0]))
				}
			} else if clientIP != "" {
				pr.Out.Header.Set("X-Real-IP", clientIP)
			}
		case clientIP != "":
			pr.Out.Header.Set("X-Real-IP", clientIP)
		}

		// Remove headers specified in removeUpstream
		for _, header := range h.removeUpstream {
			pr.Out.Header.Del(header)
		}

		// Add/override headers specified in upstreamHeaders
		for key, value := range h.upstreamHeaders {
			pr.Out.Header.Set(key, value)
		}
	}
}

// createProxyTransport creates the transport for the reverse proxy
func createProxyTransport(backendAddr string, config *TransportConfig, insecureSkipVerify bool) *http.Transport {
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			// Handle unix socket addresses
			if after, ok := strings.CutPrefix(backendAddr, "unix://"); ok {
				socketPath := after
				// Use DialContext so dialing respects the request context (timeouts/cancellation)
				d := net.Dialer{
					Timeout: config.DialTimeout,
				}
				return d.DialContext(ctx, "unix", socketPath)
			}
			// Regular TCP dial
			d := net.Dialer{
				Timeout:   config.DialTimeout,
				KeepAlive: config.KeepAliveTimeout,
			}
			return d.DialContext(ctx, network, addr)
		},
		DisableCompression:    true,
		ForceAttemptHTTP2:     false,
		MaxIdleConns:          constants.DefaultMaxIdleConns,
		MaxConnsPerHost:       constants.DefaultMaxConnsPerHost,
		MaxIdleConnsPerHost:   constants.DefaultMaxIdleConnsPerHost,
		IdleConnTimeout:       config.IdleConnTimeout,
		TLSHandshakeTimeout:   config.TLSHandshakeTimeout,
		ExpectContinueTimeout: config.ExpectContinueTimeout,
	}

	// Configure TLS settings for HTTPS backends
	if strings.HasPrefix(backendAddr, "https://") {
		transport.TLSClientConfig = &tls.Config{
			InsecureSkipVerify: insecureSkipVerify, // #nosec G402 - configurable per service
		}
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

// startMetricsCollection starts a goroutine to periodically collect connection pool metrics
func (h *httpHandler) startMetricsCollection() {
	h.stopMetrics = make(chan struct{})

	go func() {
		ticker := time.NewTicker(constants.DefaultMetricsCollectionInterval)
		defer ticker.Stop()

		// Collect initial metrics immediately
		h.collectMetrics()

		for {
			select {
			case <-ticker.C:
				h.collectMetrics()
			case <-h.stopMetrics:
				return
			}
		}
	}()
}

// getActiveRequests returns the current number of active requests
func (h *httpHandler) getActiveRequests() int64 {
	return atomic.LoadInt64(&h.activeRequests)
}

// collectMetrics collects current connection pool stats from the transport
func (h *httpHandler) collectMetrics() {
	if h.transport == nil || h.metricsCollector == nil {
		return
	}

	active := int(h.getActiveRequests())
	h.metricsCollector.UpdateConnectionPoolMetrics(h.serviceName, active)
}

// Close stops metrics collection and cleans up resources
func (h *httpHandler) Close() error {
	if h.stopMetrics != nil {
		close(h.stopMetrics)
	}

	// Close idle transport connections to prevent lingering after handler shutdown
	if h.transport != nil {
		h.transport.CloseIdleConnections()
	}

	return nil
}

// createModifyResponse creates a ModifyResponse function for handling downstream headers
func createModifyResponse(removeDownstream []string, downstreamHeaders map[string]string) func(*http.Response) error {
	return func(resp *http.Response) error {
		// Remove headers specified in removeDownstream
		for _, header := range removeDownstream {
			resp.Header.Del(header)
		}

		// Add/override headers specified in downstreamHeaders
		for key, value := range downstreamHeaders {
			resp.Header.Set(key, value)
		}

		return nil
	}
}

// createErrorHandler creates an error handler function for the reverse proxy
func createErrorHandler(backendAddr string) func(http.ResponseWriter, *http.Request, error) {
	return func(w http.ResponseWriter, r *http.Request, err error) {
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
}
