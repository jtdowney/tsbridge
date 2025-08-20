package docker

import (
	"reflect"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/jtdowney/tsbridge/internal/config"
	"github.com/jtdowney/tsbridge/internal/testhelpers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseDuration(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		expected *time.Duration
		wantErr  bool
	}{
		{
			name:     "valid duration",
			value:    "30s",
			expected: testhelpers.DurationPtr(30 * time.Second),
		},
		{
			name:     "valid duration with ms",
			value:    "500ms",
			expected: testhelpers.DurationPtr(500 * time.Millisecond),
		},
		{
			name:     "zero duration",
			value:    "0s",
			expected: testhelpers.DurationPtr(0),
		},
		{
			name:     "empty string",
			value:    "",
			expected: nil,
		},
		{
			name:     "invalid duration",
			value:    "invalid",
			expected: nil,
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

func TestDockerZeroDurationParsing(t *testing.T) {
	t.Run("zero duration from Docker label", func(t *testing.T) {
		labels := map[string]string{
			"tsbridge.service.write_timeout": "0s",
		}

		parser := newLabelParser(labels, "tsbridge")
		duration := parser.getDuration("service.write_timeout")

		require.NotNil(t, duration)
		assert.Equal(t, time.Duration(0), *duration)
	})

	t.Run("missing duration from Docker label", func(t *testing.T) {
		labels := map[string]string{
			// write_timeout not set
		}

		parser := newLabelParser(labels, "tsbridge")
		duration := parser.getDuration("service.write_timeout")

		assert.Nil(t, duration)
	})
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
		require.NotNil(t, result)
		assert.Equal(t, 30*time.Second, *result)

		result = parser.getDuration("nonexistent")
		assert.Nil(t, result)
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
			errorMsg:    "port in backend address must be between 1 and 65535",
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
		{
			name: "insecure_skip_verify with HTTPS backend is valid",
			labels: map[string]string{
				"tsbridge.enabled":                      "true",
				"tsbridge.service.name":                 "test-service",
				"tsbridge.service.backend_addr":         "https://example.com:8443",
				"tsbridge.service.insecure_skip_verify": "true",
			},
			shouldError: false,
		},
		{
			name: "insecure_skip_verify with HTTP backend is rejected",
			labels: map[string]string{
				"tsbridge.enabled":                      "true",
				"tsbridge.service.name":                 "test-service",
				"tsbridge.service.backend_addr":         "http://example.com:8080",
				"tsbridge.service.insecure_skip_verify": "true",
			},
			shouldError: true,
			errorMsg:    "insecure_skip_verify is only supported for HTTPS backends",
		},
		{
			name: "insecure_skip_verify with TCP backend is rejected",
			labels: map[string]string{
				"tsbridge.enabled":                      "true",
				"tsbridge.service.name":                 "test-service",
				"tsbridge.service.backend_addr":         "localhost:8080",
				"tsbridge.service.insecure_skip_verify": "true",
			},
			shouldError: true,
			errorMsg:    "insecure_skip_verify is only supported for HTTPS backends",
		},
		{
			name: "insecure_skip_verify false with HTTP backend is valid",
			labels: map[string]string{
				"tsbridge.enabled":                      "true",
				"tsbridge.service.name":                 "test-service",
				"tsbridge.service.backend_addr":         "http://example.com:8080",
				"tsbridge.service.insecure_skip_verify": "false",
			},
			shouldError: false,
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

// TestConfigParityBetweenTOMLAndDocker ensures that all config fields that can be
// set via TOML can also be set via Docker labels. This prevents the situation where
// new config fields are added to TOML parsing but forgotten in Docker label parsing.
func TestConfigParityBetweenTOMLAndDocker(t *testing.T) {
	t.Run("Global config fields", func(t *testing.T) {
		// Get all fields from the Global struct
		globalType := reflect.TypeOf(config.Global{})
		dockerParsedFields := getDockerParsedGlobalFields()

		for i := 0; i < globalType.NumField(); i++ {
			field := globalType.Field(i)
			mapstructureTag := field.Tag.Get("mapstructure")
			if mapstructureTag == "" {
				continue // Skip fields without mapstructure tag
			}

			// Check if this field is parsed in Docker
			dockerKey := "global." + mapstructureTag
			assert.Contains(t, dockerParsedFields, dockerKey,
				"Field %s (%s) is in config.Global but not parsed in Docker labels",
				field.Name, mapstructureTag)
		}
	})

	t.Run("Service config fields", func(t *testing.T) {
		// Get all fields from the Service struct
		serviceType := reflect.TypeOf(config.Service{})
		dockerParsedFields := getDockerParsedServiceFields()

		for i := 0; i < serviceType.NumField(); i++ {
			field := serviceType.Field(i)
			mapstructureTag := field.Tag.Get("mapstructure")
			if mapstructureTag == "" {
				continue // Skip fields without mapstructure tag
			}

			// Check if this field is parsed in Docker
			dockerKey := "service." + mapstructureTag
			assert.Contains(t, dockerParsedFields, dockerKey,
				"Field %s (%s) is in config.Service but not parsed in Docker labels",
				field.Name, mapstructureTag)
		}
	})

	t.Run("Tailscale config fields", func(t *testing.T) {
		// Get all fields from the Tailscale struct
		tailscaleType := reflect.TypeOf(config.Tailscale{})
		dockerParsedFields := getDockerParsedTailscaleFields()

		for i := 0; i < tailscaleType.NumField(); i++ {
			field := tailscaleType.Field(i)
			mapstructureTag := field.Tag.Get("mapstructure")
			if mapstructureTag == "" {
				continue // Skip fields without mapstructure tag
			}

			// Check if this field is parsed in Docker
			dockerKey := "tailscale." + mapstructureTag
			assert.Contains(t, dockerParsedFields, dockerKey,
				"Field %s (%s) is in config.Tailscale but not parsed in Docker labels",
				field.Name, mapstructureTag)
		}
	})
}

func TestDockerInsecureSkipVerifyParsing(t *testing.T) {
	provider := &Provider{
		labelPrefix: "tsbridge",
	}

	t.Run("insecure_skip_verify true", func(t *testing.T) {
		container := container.Summary{
			Names: []string{"/test-container"},
			Labels: map[string]string{
				"tsbridge.enabled":                      "true",
				"tsbridge.service.name":                 "test-service",
				"tsbridge.service.backend_addr":         "https://self-signed.example.com",
				"tsbridge.service.insecure_skip_verify": "true",
			},
		}

		svc, err := provider.parseServiceConfig(container)
		require.NoError(t, err)

		assert.NotNil(t, svc.InsecureSkipVerify)
		assert.True(t, *svc.InsecureSkipVerify)
	})

	t.Run("insecure_skip_verify false", func(t *testing.T) {
		container := container.Summary{
			Names: []string{"/test-container"},
			Labels: map[string]string{
				"tsbridge.enabled":                      "true",
				"tsbridge.service.name":                 "test-service",
				"tsbridge.service.backend_addr":         "https://example.com",
				"tsbridge.service.insecure_skip_verify": "false",
			},
		}

		svc, err := provider.parseServiceConfig(container)
		require.NoError(t, err)

		assert.NotNil(t, svc.InsecureSkipVerify)
		assert.False(t, *svc.InsecureSkipVerify)
	})

	t.Run("insecure_skip_verify not specified", func(t *testing.T) {
		container := container.Summary{
			Names: []string{"/test-container"},
			Labels: map[string]string{
				"tsbridge.enabled":              "true",
				"tsbridge.service.name":         "test-service",
				"tsbridge.service.backend_addr": "https://example.com",
			},
		}

		svc, err := provider.parseServiceConfig(container)
		require.NoError(t, err)

		assert.Nil(t, svc.InsecureSkipVerify)
	})
}

// getDockerParsedGlobalFields returns all global.* fields that are parsed in Docker
// This list must be kept in sync with parseGlobalConfig() in labels.go
func getDockerParsedGlobalFields() map[string]bool {
	return map[string]bool{
		"global.metrics_addr":                true,
		"global.read_header_timeout":         true,
		"global.write_timeout":               true,
		"global.idle_timeout":                true,
		"global.shutdown_timeout":            true,
		"global.response_header_timeout":     true,
		"global.access_log":                  true,
		"global.trusted_proxies":             true,
		"global.dial_timeout":                true,
		"global.keep_alive_timeout":          true,
		"global.idle_conn_timeout":           true,
		"global.tls_handshake_timeout":       true,
		"global.expect_continue_timeout":     true,
		"global.metrics_read_header_timeout": true,
		"global.flush_interval":              true,
		"global.default_tags":                true,
		"global.max_request_body_size":       true,
	}
}

// getDockerParsedServiceFields returns all service.* fields that are parsed in Docker
// This list must be kept in sync with parseServiceConfig() in labels.go
func getDockerParsedServiceFields() map[string]bool {
	return map[string]bool{
		"service.name":                    true,
		"service.backend_addr":            true,
		"service.whois_enabled":           true,
		"service.whois_timeout":           true,
		"service.tls_mode":                true,
		"service.read_header_timeout":     true,
		"service.write_timeout":           true,
		"service.idle_timeout":            true,
		"service.response_header_timeout": true,
		"service.access_log":              true,
		"service.funnel_enabled":          true,
		"service.insecure_skip_verify":    true,
		"service.ephemeral":               true,
		"service.flush_interval":          true,
		"service.upstream_headers":        true,
		"service.downstream_headers":      true,
		"service.remove_upstream":         true,
		"service.remove_downstream":       true,
		"service.tags":                    true,
		"service.max_request_body_size":   true,
		"service.listen_addr":             true,
	}
}

// getDockerParsedTailscaleFields returns all tailscale.* fields that are parsed in Docker
// This list must be kept in sync with parseGlobalConfig() in labels.go
func getDockerParsedTailscaleFields() map[string]bool {
	return map[string]bool{
		"tailscale.oauth_client_id":          true,
		"tailscale.oauth_client_id_env":      true,
		"tailscale.oauth_client_id_file":     true,
		"tailscale.oauth_client_secret":      true,
		"tailscale.oauth_client_secret_env":  true,
		"tailscale.oauth_client_secret_file": true,
		"tailscale.auth_key":                 true,
		"tailscale.auth_key_env":             true,
		"tailscale.auth_key_file":            true,
		"tailscale.state_dir":                true,
		"tailscale.state_dir_env":            true,
		"tailscale.default_tags":             true,
		"tailscale.control_url":              true,
	}
}

// TestDockerControlURLParsing tests that control_url is properly parsed from Docker labels
func TestDockerControlURLParsing(t *testing.T) {
	provider := &Provider{
		labelPrefix: "tsbridge",
	}

	container := &container.Summary{
		Names: []string{"/tsbridge"},
		Labels: map[string]string{
			"tsbridge.tailscale.oauth_client_id":     "test-client-id",
			"tsbridge.tailscale.oauth_client_secret": "test-secret",
			"tsbridge.tailscale.control_url":         "https://headscale.example.com",
		},
	}

	cfg := &config.Config{}
	err := provider.parseGlobalConfig(container, cfg)
	require.NoError(t, err)

	assert.Equal(t, "https://headscale.example.com", cfg.Tailscale.ControlURL)
	assert.Equal(t, "test-client-id", cfg.Tailscale.OAuthClientID)
}
