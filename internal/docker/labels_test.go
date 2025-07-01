package docker

import (
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/jtdowney/tsbridge/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseDuration(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		expected config.Duration
		wantErr  bool
	}{
		{
			name:     "valid duration",
			value:    "30s",
			expected: config.Duration{Duration: 30 * time.Second},
		},
		{
			name:     "valid duration with ms",
			value:    "500ms",
			expected: config.Duration{Duration: 500 * time.Millisecond},
		},
		{
			name:     "empty string",
			value:    "",
			expected: config.Duration{},
		},
		{
			name:     "invalid duration",
			value:    "invalid",
			expected: config.Duration{},
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseDuration(tt.value)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

func TestParseBool(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		expected *bool
		wantErr  bool
	}{
		{
			name:     "true value",
			value:    "true",
			expected: boolPtr(true),
		},
		{
			name:     "false value",
			value:    "false",
			expected: boolPtr(false),
		},
		{
			name:     "empty string",
			value:    "",
			expected: nil,
		},
		{
			name:     "invalid bool",
			value:    "invalid",
			expected: nil,
			wantErr:  true,
		},
		{
			name:     "1 as true",
			value:    "1",
			expected: boolPtr(true),
		},
		{
			name:     "0 as false",
			value:    "0",
			expected: boolPtr(false),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseBool(tt.value)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				if tt.expected == nil {
					assert.Nil(t, result)
				} else {
					require.NotNil(t, result)
					assert.Equal(t, *tt.expected, *result)
				}
			}
		})
	}
}

func TestParseInt(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		expected *int
		wantErr  bool
	}{
		{
			name:     "valid int",
			value:    "42",
			expected: intPtr(42),
		},
		{
			name:     "zero",
			value:    "0",
			expected: intPtr(0),
		},
		{
			name:     "negative int",
			value:    "-5",
			expected: intPtr(-5),
		},
		{
			name:     "empty string",
			value:    "",
			expected: nil,
		},
		{
			name:     "invalid int",
			value:    "not-a-number",
			expected: nil,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseInt(tt.value)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				if tt.expected == nil {
					assert.Nil(t, result)
				} else {
					require.NotNil(t, result)
					assert.Equal(t, *tt.expected, *result)
				}
			}
		})
	}
}

func TestParseStringSlice(t *testing.T) {
	tests := []struct {
		name      string
		value     string
		separator string
		expected  []string
	}{
		{
			name:      "comma separated",
			value:     "a,b,c",
			separator: ",",
			expected:  []string{"a", "b", "c"},
		},
		{
			name:      "with spaces",
			value:     "a, b, c",
			separator: ",",
			expected:  []string{"a", "b", "c"},
		},
		{
			name:      "empty string",
			value:     "",
			separator: ",",
			expected:  nil,
		},
		{
			name:      "single value",
			value:     "single",
			separator: ",",
			expected:  []string{"single"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseStringSlice(tt.value, tt.separator)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestLabelParser(t *testing.T) {
	labels := map[string]string{
		"tsbridge.service.name":                "test-service",
		"tsbridge.service.whois_enabled":       "true",
		"tsbridge.service.read_header_timeout": "30s",
		"tsbridge.service.remove_upstream":     "X-Forwarded-For,X-Real-IP",
	}

	parser := &labelParser{
		labels: labels,
		prefix: "tsbridge",
	}

	t.Run("getString", func(t *testing.T) {
		assert.Equal(t, "test-service", parser.getString("service.name"))
		assert.Equal(t, "", parser.getString("nonexistent"))
	})

	t.Run("getBool", func(t *testing.T) {
		result := parser.getBool("service.whois_enabled")
		require.NotNil(t, result)
		assert.True(t, *result)

		result = parser.getBool("nonexistent")
		assert.Nil(t, result)
	})

	t.Run("getInt", func(t *testing.T) {
		// No int fields in current config, just test nonexistent key
		result := parser.getInt("nonexistent")
		assert.Nil(t, result)
	})

	t.Run("getDuration", func(t *testing.T) {
		result := parser.getDuration("service.read_header_timeout")
		assert.Equal(t, 30*time.Second, result.Duration)

		result = parser.getDuration("nonexistent")
		assert.Equal(t, time.Duration(0), result.Duration)
	})

	t.Run("getStringSlice", func(t *testing.T) {
		result := parser.getStringSlice("service.remove_upstream", ",")
		assert.Equal(t, []string{"X-Forwarded-For", "X-Real-IP"}, result)

		result = parser.getStringSlice("nonexistent", ",")
		assert.Nil(t, result)
	})
}

// Helper functions
func boolPtr(b bool) *bool {
	return &b
}

func intPtr(i int) *int {
	return &i
}

// TestHeaderInjectionVulnerabilities tests for header injection security issues
func TestHeaderInjectionVulnerabilities(t *testing.T) {
	tests := []struct {
		name           string
		labels         map[string]string
		expectPanic    bool
		expectError    bool
		invalidHeaders []string // Headers that should be rejected
	}{
		{
			name: "CRLF injection in header value",
			labels: map[string]string{
				"tsbridge.service.upstream_headers.X-Custom": "value\r\nX-Injected: malicious",
			},
			invalidHeaders: []string{"X-Custom"},
		},
		{
			name: "CRLF injection with various newline combinations",
			labels: map[string]string{
				"tsbridge.service.upstream_headers.X-Test1": "value\rinjected",
				"tsbridge.service.upstream_headers.X-Test2": "value\ninjected",
				"tsbridge.service.upstream_headers.X-Test3": "value\r\ninjected",
			},
			invalidHeaders: []string{"X-Test1", "X-Test2", "X-Test3"},
		},
		{
			name: "Invalid header names with special characters",
			labels: map[string]string{
				"tsbridge.service.upstream_headers.X-Test space":     "value",
				"tsbridge.service.upstream_headers.X-Test:colon":     "value",
				"tsbridge.service.upstream_headers.X-Test;semicolon": "value",
				"tsbridge.service.upstream_headers.X-Test(paren":     "value",
				"tsbridge.service.upstream_headers.X-Test\"quote":    "value",
			},
			invalidHeaders: []string{"X-Test space", "X-Test:colon", "X-Test;semicolon", "X-Test(paren", "X-Test\"quote"},
		},
		{
			name: "Control characters in header values",
			labels: map[string]string{
				"tsbridge.service.upstream_headers.X-Control": "value\x00null",
				"tsbridge.service.upstream_headers.X-Tab":     "value\ttab",
				"tsbridge.service.upstream_headers.X-Bell":    "value\x07bell",
			},
			invalidHeaders: []string{"X-Control", "X-Tab", "X-Bell"},
		},
		{
			name: "Valid headers that should pass",
			labels: map[string]string{
				"tsbridge.service.upstream_headers.X-Custom-Header": "valid-value",
				"tsbridge.service.upstream_headers.Authorization":   "Bearer token123",
				"tsbridge.service.upstream_headers.X-Request-ID":    "12345-67890",
				"tsbridge.service.upstream_headers.Accept-Language": "en-US,en;q=0.9",
			},
			invalidHeaders: []string{}, // All should be valid
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parser := newLabelParser(tt.labels, "tsbridge")

			headers := parser.getHeaders("service.upstream_headers")

			// Check that invalid headers were rejected
			for _, invalidHeader := range tt.invalidHeaders {
				_, exists := headers[invalidHeader]
				assert.False(t, exists, "Invalid header %q should have been rejected", invalidHeader)
			}

			// For valid headers test case, check they all exist
			if len(tt.invalidHeaders) == 0 && len(headers) > 0 {
				// These are the valid headers from the test case
				assert.Contains(t, headers, "X-Custom-Header")
				assert.Contains(t, headers, "Authorization")
				assert.Contains(t, headers, "X-Request-ID")
				assert.Contains(t, headers, "Accept-Language")
			}
		})
	}
}

// TestValidateHeaderName tests the header name validation function
func TestValidateHeaderName(t *testing.T) {
	tests := []struct {
		name    string
		header  string
		isValid bool
	}{
		// Valid headers (RFC 7230 compliant)
		{"simple header", "X-Custom-Header", true},
		{"authorization", "Authorization", true},
		{"with numbers", "X-Request-ID-123", true},
		{"all caps", "X-API-KEY", true},
		{"lowercase", "x-custom-header", true},
		{"single char", "X", true},

		// Invalid headers
		{"with space", "X-Custom Header", false},
		{"with colon", "X-Custom:Header", false},
		{"with semicolon", "X-Custom;Header", false},
		{"with comma", "X-Custom,Header", false},
		{"with parenthesis", "X-Custom(Header)", false},
		{"with quotes", "X-Custom\"Header\"", false},
		{"with slash", "X-Custom/Header", false},
		{"with brackets", "X-Custom[Header]", false},
		{"with equals", "X-Custom=Header", false},
		{"with at sign", "X-Custom@Header", false},
		{"empty string", "", false},
		{"just spaces", "   ", false},
		{"newline", "X-Custom\nHeader", false},
		{"carriage return", "X-Custom\rHeader", false},
		{"tab", "X-Custom\tHeader", false},
		{"null byte", "X-Custom\x00Header", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// This function doesn't exist yet - it will be implemented
			// to fix the vulnerability
			isValid := isValidHeaderName(tt.header)
			assert.Equal(t, tt.isValid, isValid, "Header: %q", tt.header)
		})
	}
}

// TestValidateHeaderValue tests the header value validation function
func TestValidateHeaderValue(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		isValid bool
	}{
		// Valid values
		{"simple value", "simple-value", true},
		{"with spaces", "Bearer token123", true},
		{"with special chars", "application/json; charset=utf-8", true},
		{"with equals", "key=value", true},
		{"with comma", "value1, value2", true},
		{"empty value", "", true}, // Empty is technically valid

		// Invalid values (containing control characters)
		{"with CRLF", "value\r\nX-Injected: bad", false},
		{"with LF", "value\ninjected", false},
		{"with CR", "value\rinjected", false},
		{"with null", "value\x00null", false},
		{"with bell", "value\x07bell", false},
		{"with backspace", "value\x08bs", false},
		{"with form feed", "value\x0cff", false},
		{"with vertical tab", "value\x0bvt", false},
		{"with DEL", "value\x7fdel", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// This function doesn't exist yet - it will be implemented
			// to fix the vulnerability
			isValid := isValidHeaderValue(tt.value)
			assert.Equal(t, tt.isValid, isValid, "Value: %q", tt.value)
		})
	}
}

// TestValidateBackendAddress tests backend address validation
func TestValidateBackendAddress(t *testing.T) {
	tests := []struct {
		name    string
		addr    string
		isValid bool
		errMsg  string
	}{
		// Valid network addresses
		{"valid host:port", "localhost:8080", true, ""},
		{"valid IP:port", "127.0.0.1:3000", true, ""},
		{"valid IPv6:port", "[::1]:8080", true, ""},
		{"valid domain:port", "api.example.com:443", true, ""},
		{"valid high port", "0.0.0.0:65535", true, ""},
		{"valid low port", "localhost:1", true, ""},

		// Valid unix socket addresses
		{"valid unix socket", "unix:///var/run/app.sock", true, ""},
		{"valid unix socket with complex path", "unix:///tmp/sockets/app.sock", true, ""},

		// Valid addresses - port only (binds to all interfaces)
		{"port only", ":8080", true, ""},
		{"port only high", ":65535", true, ""},

		// Invalid addresses - format issues
		{"missing port", "localhost", false, "invalid backend address format"},
		{"empty address", "", false, "backend address cannot be empty"},
		{"just colon", ":", false, "invalid port"},
		{"invalid unix prefix", "unix:/var/run/app.sock", false, "unix socket path must start with unix://"},
		{"unix with port", "unix://socket:8080", false, "unix socket cannot have port"},

		// Invalid addresses - port range
		{"port zero", "localhost:0", false, "port must be between 1 and 65535"},
		{"port too high", "localhost:65536", false, "port must be between 1 and 65535"},
		{"negative port", "localhost:-1", false, "port must be between 1 and 65535"},
		{"non-numeric port", "localhost:abc", false, "invalid port"},

		// Invalid addresses - path traversal in unix sockets
		{"unix path traversal", "unix://../../../etc/passwd", false, "invalid unix socket path"},
		{"unix relative path", "unix://./socket", false, "unix socket path must be absolute"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateBackendAddress(tt.addr)
			if tt.isValid {
				assert.NoError(t, err, "Expected address %q to be valid", tt.addr)
			} else {
				assert.Error(t, err, "Expected address %q to be invalid", tt.addr)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			}
		})
	}
}

// TestParseServiceConfigBackendValidation tests that backend address validation is applied
func TestParseServiceConfigBackendValidation(t *testing.T) {
	provider := &Provider{
		labelPrefix: "tsbridge",
	}

	tests := []struct {
		name        string
		labels      map[string]string
		shouldError bool
		errorMsg    string
	}{
		{
			name: "valid backend address",
			labels: map[string]string{
				"tsbridge.enabled":              "true",
				"tsbridge.service.name":         "test-service",
				"tsbridge.service.backend_addr": "localhost:8080",
			},
			shouldError: false,
		},
		{
			name: "invalid port rejected",
			labels: map[string]string{
				"tsbridge.enabled":              "true",
				"tsbridge.service.name":         "test-service",
				"tsbridge.service.backend_addr": "localhost:70000",
			},
			shouldError: true,
			errorMsg:    "port must be between 1 and 65535",
		},
		{
			name: "unix socket path traversal rejected",
			labels: map[string]string{
				"tsbridge.enabled":              "true",
				"tsbridge.service.name":         "test-service",
				"tsbridge.service.backend_addr": "unix://../../../etc/passwd",
			},
			shouldError: true,
			errorMsg:    "invalid unix socket path",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			container := container.Summary{
				Names:  []string{"/test-container"},
				Labels: tt.labels,
			}

			svc, err := provider.parseServiceConfig(container)
			if tt.shouldError {
				assert.Error(t, err)
				if tt.errorMsg != "" {
					assert.Contains(t, err.Error(), tt.errorMsg)
				}
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, svc)
			}
		})
	}
}
