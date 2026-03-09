# Migration: ReverseProxy Director → Rewrite

Go 1.26 deprecated `httputil.ReverseProxy.Director` (SA1019). The replacement is `Rewrite`, available since Go 1.20.

## Current Implementation

`internal/proxy/proxy.go` lines 104–116:

```go
h.proxy = httputil.NewSingleHostReverseProxy(target)
originalDirector := h.proxy.Director
h.proxy.Director = createProxyDirector(h, originalDirector)
```

`createProxyDirector` (lines 218–273) wraps the default Director to:
1. Call the original director (sets URL scheme/host/path)
2. Handle X-Forwarded-For based on trusted proxy status
3. Set X-Real-IP (first IP in XFF chain for trusted, client IP otherwise)
4. Set X-Forwarded-Proto from TLS state
5. Remove/add upstream headers

## Migration Plan

Replace `NewSingleHostReverseProxy` + `Director` with a bare `ReverseProxy` + `Rewrite`:

```go
h.proxy = &httputil.ReverseProxy{
    Rewrite: createProxyRewrite(h, target),
}
```

### Key Differences in Rewrite API

- Receives `*httputil.ProxyRequest` with `In` (original, read-only) and `Out` (outbound, mutable)
- X-Forwarded-For, X-Forwarded-Host, X-Forwarded-Proto are **stripped** before Rewrite is called
- Call `pr.SetXForwarded()` to populate them (replaces manual X-Forwarded-Proto and ReverseProxy's auto XFF)
- Call `pr.SetURL(target)` to route to backend (replaces original Director)
- `SetURL` rewrites `Out.Host` — add `pr.Out.Host = pr.In.Host` to preserve inbound Host (matching current behavior)

### Rewrite Function Skeleton

```go
func createProxyRewrite(h *httpHandler, target *url.URL) func(*httputil.ProxyRequest) {
    return func(pr *httputil.ProxyRequest) {
        pr.SetURL(target)
        pr.Out.Host = pr.In.Host // preserve inbound Host

        clientIP, _, _ := net.SplitHostPort(pr.In.RemoteAddr)
        fromTrustedProxy := h.isTrustedProxy(clientIP)

        // For trusted proxies, copy inbound XFF so SetXForwarded appends to it
        if fromTrustedProxy {
            if xff := pr.In.Header["X-Forwarded-For"]; len(xff) > 0 {
                pr.Out.Header["X-Forwarded-For"] = xff
            }
        }

        // Sets XFF (appending client IP), X-Forwarded-Host, X-Forwarded-Proto
        pr.SetXForwarded()

        // Set X-Real-IP
        if fromTrustedProxy {
            if existingXFF := pr.In.Header.Get("X-Forwarded-For"); existingXFF != "" {
                ips := strings.Split(existingXFF, ",")
                if len(ips) > 0 {
                    pr.Out.Header.Set("X-Real-IP", strings.TrimSpace(ips[0]))
                }
            }
        } else if clientIP != "" {
            pr.Out.Header.Set("X-Real-IP", clientIP)
        }

        // Remove/add upstream headers
        for _, header := range h.removeUpstream {
            pr.Out.Header.Del(header)
        }
        for key, value := range h.upstreamHeaders {
            pr.Out.Header.Set(key, value)
        }
    }
}
```

### Test Impact

- No tests directly reference `Director` or `createProxyDirector`
- Existing proxy tests (`proxy_test.go`) cover XFF, X-Real-IP, trusted proxies, headers, and error handling — they validate behavior, not implementation
- Run full proxy test suite after migration: `go test -race ./internal/proxy/`

### Behavioral Notes

- `SetXForwarded()` also sets `X-Forwarded-Host` (from inbound Host header) — verify this doesn't conflict with any existing header logic
- With Director, ReverseProxy auto-appended client IP to XFF after Director ran. With Rewrite, `SetXForwarded()` handles this explicitly
- User-Agent suppression (`""` for missing UA) happens in ReverseProxy.ServeHTTP for both Director and Rewrite paths — no change needed
