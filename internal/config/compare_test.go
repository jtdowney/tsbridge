package config

import (
	"testing"
	"time"

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
			expected: false, // Order matters for simplicity
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
				ResponseHeaderTimeout: Duration{Duration: 30 * time.Second, IsSet: true},
			},
			b: Service{
				Name:                  "test-service",
				BackendAddr:           "http://localhost:8080",
				ResponseHeaderTimeout: Duration{Duration: 60 * time.Second, IsSet: true},
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
				FlushInterval: Duration{Duration: 1 * time.Second, IsSet: true},
			},
			b: Service{
				Name:          "test-service",
				BackendAddr:   "http://localhost:8080",
				FlushInterval: Duration{Duration: 2 * time.Second, IsSet: true},
			},
			expected: false,
		},
		{
			name: "different read header timeouts",
			a: Service{
				Name:              "test-service",
				BackendAddr:       "http://localhost:8080",
				ReadHeaderTimeout: Duration{Duration: 10 * time.Second, IsSet: true},
			},
			b: Service{
				Name:              "test-service",
				BackendAddr:       "http://localhost:8080",
				ReadHeaderTimeout: Duration{Duration: 20 * time.Second, IsSet: true},
			},
			expected: false,
		},
		{
			name: "different write timeouts",
			a: Service{
				Name:         "test-service",
				BackendAddr:  "http://localhost:8080",
				WriteTimeout: Duration{Duration: 30 * time.Second, IsSet: true},
			},
			b: Service{
				Name:         "test-service",
				BackendAddr:  "http://localhost:8080",
				WriteTimeout: Duration{Duration: 60 * time.Second, IsSet: true},
			},
			expected: false,
		},
		{
			name: "different idle timeouts",
			a: Service{
				Name:        "test-service",
				BackendAddr: "http://localhost:8080",
				IdleTimeout: Duration{Duration: 120 * time.Second, IsSet: true},
			},
			b: Service{
				Name:        "test-service",
				BackendAddr: "http://localhost:8080",
				IdleTimeout: Duration{Duration: 240 * time.Second, IsSet: true},
			},
			expected: false,
		},
		{
			name: "different whois timeouts",
			a: Service{
				Name:         "test-service",
				BackendAddr:  "http://localhost:8080",
				WhoisTimeout: Duration{Duration: 5 * time.Second, IsSet: true},
			},
			b: Service{
				Name:         "test-service",
				BackendAddr:  "http://localhost:8080",
				WhoisTimeout: Duration{Duration: 10 * time.Second, IsSet: true},
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

// Helper functions for creating pointers
func boolPtr(b bool) *bool {
	return &b
}
