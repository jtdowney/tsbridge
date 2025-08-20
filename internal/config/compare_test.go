package config

import (
	"reflect"
	"testing"
	"time"

	"github.com/jtdowney/tsbridge/internal/testhelpers"
	"github.com/stretchr/testify/assert"
)

func TestServiceConfigEqual(t *testing.T) {
	tests := []struct {
		name     string
		a        Service
		b        Service
		expected bool
	}{
		{
			name: "identical basic configs",
			a: Service{
				Name:        "test-service",
				BackendAddr: "http://localhost:8080",
				TLSMode:     "strict",
			},
			b: Service{
				Name:        "test-service",
				BackendAddr: "http://localhost:8080",
				TLSMode:     "strict",
			},
			expected: true,
		},
		{
			name: "different names",
			a: Service{
				Name:        "service-a",
				BackendAddr: "http://localhost:8080",
			},
			b: Service{
				Name:        "service-b",
				BackendAddr: "http://localhost:8080",
			},
			expected: false,
		},
		{
			name: "different backend addresses",
			a: Service{
				Name:        "test-service",
				BackendAddr: "http://localhost:8080",
			},
			b: Service{
				Name:        "test-service",
				BackendAddr: "http://localhost:8081",
			},
			expected: false,
		},
		{
			name: "different listen addrs",
			a: Service{
				Name:        "test-service",
				BackendAddr: "http://localhost:8080",
				ListenAddr:  ":443",
			},
			b: Service{
				Name:        "test-service",
				BackendAddr: "http://localhost:8080",
				ListenAddr:  ":8443",
			},
			expected: false,
		},
		{
			name: "same listen addrs",
			a: Service{
				Name:        "test-service",
				BackendAddr: "http://localhost:8080",
				ListenAddr:  ":443",
			},
			b: Service{
				Name:        "test-service",
				BackendAddr: "http://localhost:8080",
				ListenAddr:  ":443",
			},
			expected: true,
		},
		{
			name: "different TLS modes",
			a: Service{
				Name:        "test-service",
				BackendAddr: "http://localhost:8080",
				TLSMode:     "strict",
			},
			b: Service{
				Name:        "test-service",
				BackendAddr: "http://localhost:8080",
				TLSMode:     "",
			},
			expected: false,
		},
		{
			name: "different funnel enabled state",
			a: Service{
				Name:          "test-service",
				BackendAddr:   "http://localhost:8080",
				FunnelEnabled: boolPtr(true),
			},
			b: Service{
				Name:          "test-service",
				BackendAddr:   "http://localhost:8080",
				FunnelEnabled: boolPtr(false),
			},
			expected: false,
		},
		{
			name: "nil vs non-nil funnel enabled",
			a: Service{
				Name:          "test-service",
				BackendAddr:   "http://localhost:8080",
				FunnelEnabled: nil,
			},
			b: Service{
				Name:          "test-service",
				BackendAddr:   "http://localhost:8080",
				FunnelEnabled: boolPtr(false),
			},
			expected: false,
		},
		{
			name: "both nil funnel enabled",
			a: Service{
				Name:          "test-service",
				BackendAddr:   "http://localhost:8080",
				FunnelEnabled: nil,
			},
			b: Service{
				Name:          "test-service",
				BackendAddr:   "http://localhost:8080",
				FunnelEnabled: nil,
			},
			expected: true,
		},
		{
			name: "different ephemeral setting",
			a: Service{
				Name:        "test-service",
				BackendAddr: "http://localhost:8080",
				Ephemeral:   true,
			},
			b: Service{
				Name:        "test-service",
				BackendAddr: "http://localhost:8080",
				Ephemeral:   false,
			},
			expected: false,
		},
		{
			name: "different tags",
			a: Service{
				Name:        "test-service",
				BackendAddr: "http://localhost:8080",
				Tags:        []string{"prod", "api"},
			},
			b: Service{
				Name:        "test-service",
				BackendAddr: "http://localhost:8080",
				Tags:        []string{"dev", "api"},
			},
			expected: false,
		},
		{
			name: "same tags different order",
			a: Service{
				Name:        "test-service",
				BackendAddr: "http://localhost:8080",
				Tags:        []string{"api", "prod"},
			},
			b: Service{
				Name:        "test-service",
				BackendAddr: "http://localhost:8080",
				Tags:        []string{"prod", "api"},
			},
			expected: true, // Order doesn't matter for tags
		},
		{
			name: "different upstream headers",
			a: Service{
				Name:        "test-service",
				BackendAddr: "http://localhost:8080",
				UpstreamHeaders: map[string]string{
					"X-Custom": "value1",
				},
			},
			b: Service{
				Name:        "test-service",
				BackendAddr: "http://localhost:8080",
				UpstreamHeaders: map[string]string{
					"X-Custom": "value2",
				},
			},
			expected: false,
		},
		{
			name: "different downstream headers",
			a: Service{
				Name:        "test-service",
				BackendAddr: "http://localhost:8080",
				DownstreamHeaders: map[string]string{
					"X-Response": "value1",
				},
			},
			b: Service{
				Name:        "test-service",
				BackendAddr: "http://localhost:8080",
				DownstreamHeaders: map[string]string{
					"X-Response": "value2",
				},
			},
			expected: false,
		},
		{
			name: "different response header timeouts",
			a: Service{
				Name:                  "test-service",
				BackendAddr:           "http://localhost:8080",
				ResponseHeaderTimeout: testhelpers.DurationPtr(30 * time.Second),
			},
			b: Service{
				Name:                  "test-service",
				BackendAddr:           "http://localhost:8080",
				ResponseHeaderTimeout: testhelpers.DurationPtr(60 * time.Second),
			},
			expected: false,
		},
		{
			name: "different access log settings",
			a: Service{
				Name:        "test-service",
				BackendAddr: "http://localhost:8080",
				AccessLog:   boolPtr(true),
			},
			b: Service{
				Name:        "test-service",
				BackendAddr: "http://localhost:8080",
				AccessLog:   boolPtr(false),
			},
			expected: false,
		},
		{
			name: "nil vs empty maps",
			a: Service{
				Name:            "test-service",
				BackendAddr:     "http://localhost:8080",
				UpstreamHeaders: nil,
			},
			b: Service{
				Name:            "test-service",
				BackendAddr:     "http://localhost:8080",
				UpstreamHeaders: map[string]string{},
			},
			expected: true, // Treat nil and empty maps as equal
		},
		{
			name: "nil vs empty slices",
			a: Service{
				Name:        "test-service",
				BackendAddr: "http://localhost:8080",
				Tags:        nil,
			},
			b: Service{
				Name:        "test-service",
				BackendAddr: "http://localhost:8080",
				Tags:        []string{},
			},
			expected: true, // Treat nil and empty slices as equal
		},
		{
			name: "different whois enabled",
			a: Service{
				Name:         "test-service",
				BackendAddr:  "http://localhost:8080",
				WhoisEnabled: boolPtr(true),
			},
			b: Service{
				Name:         "test-service",
				BackendAddr:  "http://localhost:8080",
				WhoisEnabled: boolPtr(false),
			},
			expected: false,
		},
		{
			name: "different remove upstream headers",
			a: Service{
				Name:           "test-service",
				BackendAddr:    "http://localhost:8080",
				RemoveUpstream: []string{"X-Header-1"},
			},
			b: Service{
				Name:           "test-service",
				BackendAddr:    "http://localhost:8080",
				RemoveUpstream: []string{"X-Header-2"},
			},
			expected: false,
		},
		{
			name: "different remove downstream headers",
			a: Service{
				Name:             "test-service",
				BackendAddr:      "http://localhost:8080",
				RemoveDownstream: []string{"X-Response-1"},
			},
			b: Service{
				Name:             "test-service",
				BackendAddr:      "http://localhost:8080",
				RemoveDownstream: []string{"X-Response-2"},
			},
			expected: false,
		},
		{
			name: "different flush intervals",
			a: Service{
				Name:          "test-service",
				BackendAddr:   "http://localhost:8080",
				FlushInterval: testhelpers.DurationPtr(1 * time.Second),
			},
			b: Service{
				Name:          "test-service",
				BackendAddr:   "http://localhost:8080",
				FlushInterval: testhelpers.DurationPtr(2 * time.Second),
			},
			expected: false,
		},
		{
			name: "different read header timeouts",
			a: Service{
				Name:              "test-service",
				BackendAddr:       "http://localhost:8080",
				ReadHeaderTimeout: testhelpers.DurationPtr(10 * time.Second),
			},
			b: Service{
				Name:              "test-service",
				BackendAddr:       "http://localhost:8080",
				ReadHeaderTimeout: testhelpers.DurationPtr(20 * time.Second),
			},
			expected: false,
		},
		{
			name: "different write timeouts",
			a: Service{
				Name:         "test-service",
				BackendAddr:  "http://localhost:8080",
				WriteTimeout: testhelpers.DurationPtr(30 * time.Second),
			},
			b: Service{
				Name:         "test-service",
				BackendAddr:  "http://localhost:8080",
				WriteTimeout: testhelpers.DurationPtr(60 * time.Second),
			},
			expected: false,
		},
		{
			name: "different idle timeouts",
			a: Service{
				Name:        "test-service",
				BackendAddr: "http://localhost:8080",
				IdleTimeout: testhelpers.DurationPtr(120 * time.Second),
			},
			b: Service{
				Name:        "test-service",
				BackendAddr: "http://localhost:8080",
				IdleTimeout: testhelpers.DurationPtr(240 * time.Second),
			},
			expected: false,
		},
		{
			name: "different whois timeouts",
			a: Service{
				Name:         "test-service",
				BackendAddr:  "http://localhost:8080",
				WhoisTimeout: testhelpers.DurationPtr(5 * time.Second),
			},
			b: Service{
				Name:         "test-service",
				BackendAddr:  "http://localhost:8080",
				WhoisTimeout: testhelpers.DurationPtr(10 * time.Second),
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ServiceConfigEqual(tt.a, tt.b)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestServiceConfigEqualCoversAllFields uses reflection to ensure all fields
// of the Service struct are compared in ServiceConfigEqual function.
// This test prevents bugs when new fields are added but not included in comparison.
func TestServiceConfigEqualCoversAllFields(t *testing.T) {
	// Use reflection to get all fields of Service struct
	serviceType := reflect.TypeOf(Service{})

	// List of fields we expect to be compared in ServiceConfigEqual
	comparedFields := map[string]bool{
		"Name":                  true,
		"BackendAddr":           true,
		"ListenAddr":            true,
		"WhoisEnabled":          true,
		"WhoisTimeout":          true,
		"TLSMode":               true,
		"Tags":                  true,
		"ReadHeaderTimeout":     true,
		"WriteTimeout":          true,
		"IdleTimeout":           true,
		"ResponseHeaderTimeout": true,
		"AccessLog":             true,
		"FunnelEnabled":         true,
		"Ephemeral":             true,
		"FlushInterval":         true,
		"InsecureSkipVerify":    true,
		"UpstreamHeaders":       true,
		"DownstreamHeaders":     true,
		"RemoveUpstream":        true,
		"RemoveDownstream":      true,
		"MaxRequestBodySize":    true,
	}

	// Check that all struct fields are in our comparison
	for i := 0; i < serviceType.NumField(); i++ {
		field := serviceType.Field(i)
		if !comparedFields[field.Name] {
			t.Errorf("Field %s is not compared in ServiceConfigEqual", field.Name)
		}
	}

	// Also check that we don't have extra fields in our expected list
	// that don't actually exist in the struct
	actualFields := make(map[string]bool)
	for i := 0; i < serviceType.NumField(); i++ {
		field := serviceType.Field(i)
		actualFields[field.Name] = true
	}

	for fieldName := range comparedFields {
		if !actualFields[fieldName] {
			t.Errorf("Expected field %s does not exist in Service struct", fieldName)
		}
	}
}

// Helper functions for creating pointers
func boolPtr(b bool) *bool {
	return &b
}

// TestServiceConfigEqualWithGoCmp tests the improved implementation using go-cmp
func TestServiceConfigEqualWithGoCmp(t *testing.T) {
	tests := []struct {
		name     string
		a        Service
		b        Service
		expected bool
	}{
		{
			name: "complex service with all fields different",
			a: Service{
				Name:                  "service-a",
				BackendAddr:           "http://localhost:8080",
				WhoisEnabled:          boolPtr(true),
				WhoisTimeout:          testhelpers.DurationPtr(5 * time.Second),
				TLSMode:               "strict",
				Tags:                  []string{"prod", "api"},
				ReadHeaderTimeout:     testhelpers.DurationPtr(10 * time.Second),
				WriteTimeout:          testhelpers.DurationPtr(30 * time.Second),
				IdleTimeout:           testhelpers.DurationPtr(120 * time.Second),
				ResponseHeaderTimeout: testhelpers.DurationPtr(30 * time.Second),
				AccessLog:             boolPtr(true),
				MaxRequestBodySize:    testhelpers.Int64Ptr(1048576),
				FunnelEnabled:         boolPtr(false),
				Ephemeral:             true,
				FlushInterval:         testhelpers.DurationPtr(1 * time.Second),
				UpstreamHeaders: map[string]string{
					"X-Custom-Header": "value1",
					"X-Request-ID":    "123",
				},
				DownstreamHeaders: map[string]string{
					"X-Response-Header": "response1",
				},
				RemoveUpstream:   []string{"Authorization", "Cookie"},
				RemoveDownstream: []string{"Server", "X-Powered-By"},
			},
			b: Service{
				Name:                  "service-b",
				BackendAddr:           "http://localhost:8081",
				WhoisEnabled:          boolPtr(false),
				WhoisTimeout:          testhelpers.DurationPtr(10 * time.Second),
				TLSMode:               "off",
				Tags:                  []string{"dev", "internal"},
				ReadHeaderTimeout:     testhelpers.DurationPtr(20 * time.Second),
				WriteTimeout:          testhelpers.DurationPtr(60 * time.Second),
				IdleTimeout:           testhelpers.DurationPtr(240 * time.Second),
				ResponseHeaderTimeout: testhelpers.DurationPtr(60 * time.Second),
				AccessLog:             boolPtr(false),
				MaxRequestBodySize:    testhelpers.Int64Ptr(2097152),
				FunnelEnabled:         boolPtr(true),
				Ephemeral:             false,
				FlushInterval:         testhelpers.DurationPtr(2 * time.Second),
				UpstreamHeaders: map[string]string{
					"X-Custom-Header": "value2",
					"X-Trace-ID":      "456",
				},
				DownstreamHeaders: map[string]string{
					"X-Response-Header": "response2",
					"X-Cache-Control":   "no-cache",
				},
				RemoveUpstream:   []string{"X-Forwarded-For"},
				RemoveDownstream: []string{"Date"},
			},
			expected: false,
		},
		{
			name: "nil and empty slices should be equal",
			a: Service{
				Name:        "test-service",
				BackendAddr: "http://localhost:8080",
				Tags:        nil,
			},
			b: Service{
				Name:        "test-service",
				BackendAddr: "http://localhost:8080",
				Tags:        []string{},
			},
			expected: true,
		},
		{
			name: "nil and empty maps should be equal",
			a: Service{
				Name:            "test-service",
				BackendAddr:     "http://localhost:8080",
				UpstreamHeaders: nil,
			},
			b: Service{
				Name:            "test-service",
				BackendAddr:     "http://localhost:8080",
				UpstreamHeaders: map[string]string{},
			},
			expected: true,
		},
		{
			name: "tags order should not matter",
			a: Service{
				Name:        "test-service",
				BackendAddr: "http://localhost:8080",
				Tags:        []string{"api", "prod", "v2"},
			},
			b: Service{
				Name:        "test-service",
				BackendAddr: "http://localhost:8080",
				Tags:        []string{"v2", "api", "prod"},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ServiceConfigEqual(tt.a, tt.b)
			assert.Equal(t, tt.expected, result)
		})
	}
}
