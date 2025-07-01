package constants

import (
	"testing"
	"time"
)

func TestTimeoutConstants(t *testing.T) {
	tests := []struct {
		name     string
		constant time.Duration
		expected time.Duration
	}{
		{
			name:     "DefaultReadHeaderTimeout",
			constant: DefaultReadHeaderTimeout,
			expected: 30 * time.Second,
		},
		{
			name:     "DefaultWriteTimeout",
			constant: DefaultWriteTimeout,
			expected: 30 * time.Second,
		},
		{
			name:     "DefaultIdleTimeout",
			constant: DefaultIdleTimeout,
			expected: 120 * time.Second,
		},
		{
			name:     "DefaultShutdownTimeout",
			constant: DefaultShutdownTimeout,
			expected: 30 * time.Second,
		},
		{
			name:     "DefaultWhoisTimeout",
			constant: DefaultWhoisTimeout,
			expected: 5 * time.Second,
		},
		{
			name:     "BackendHealthCheckTimeout",
			constant: BackendHealthCheckTimeout,
			expected: 5 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.constant != tt.expected {
				t.Errorf("%s = %v, want %v", tt.name, tt.constant, tt.expected)
			}
		})
	}
}

func TestOAuthConstants(t *testing.T) {
	// OAuth token expiry is 90 days in seconds
	expectedExpiry := 90 * 24 * 60 * 60
	if OAuthTokenExpirySeconds != expectedExpiry {
		t.Errorf("OAuthTokenExpirySeconds = %d, want %d", OAuthTokenExpirySeconds, expectedExpiry)
	}

	// Verify it equals 90 days
	expectedDays := 90
	actualDays := OAuthTokenExpirySeconds / (24 * 60 * 60)
	if actualDays != expectedDays {
		t.Errorf("OAuthTokenExpirySeconds represents %d days, want %d days", actualDays, expectedDays)
	}
}

func TestDefaultBooleans(t *testing.T) {
	if !DefaultAccessLogEnabled {
		t.Error("DefaultAccessLogEnabled should be true")
	}

	if DefaultWhoisEnabled {
		t.Error("DefaultWhoisEnabled should be false")
	}
}

func TestDefaultStrings(t *testing.T) {
	if DefaultTLSMode != "auto" {
		t.Errorf("DefaultTLSMode = %q, want %q", DefaultTLSMode, "auto")
	}
}

func TestDefaultConstants(t *testing.T) {
	if DefaultAccessLogEnabled != true {
		t.Error("DefaultAccessLogEnabled should be true")
	}
	if DefaultWhoisEnabled {
		t.Error("DefaultWhoisEnabled should be false")
	}
}
