package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestDurationUnmarshalText(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantValue time.Duration
		wantIsSet bool
		wantErr   bool
	}{
		{
			name:      "valid seconds",
			input:     "30s",
			wantValue: 30 * time.Second,
			wantIsSet: true,
		},
		{
			name:      "valid minutes",
			input:     "5m",
			wantValue: 5 * time.Minute,
			wantIsSet: true,
		},
		{
			name:      "valid hours",
			input:     "2h",
			wantValue: 2 * time.Hour,
			wantIsSet: true,
		},
		{
			name:      "valid milliseconds",
			input:     "100ms",
			wantValue: 100 * time.Millisecond,
			wantIsSet: true,
		},
		{
			name:      "valid microseconds",
			input:     "50us",
			wantValue: 50 * time.Microsecond,
			wantIsSet: true,
		},
		{
			name:      "valid nanoseconds",
			input:     "1000ns",
			wantValue: 1000 * time.Nanosecond,
			wantIsSet: true,
		},
		{
			name:      "valid complex duration",
			input:     "1h30m45s",
			wantValue: time.Hour + 30*time.Minute + 45*time.Second,
			wantIsSet: true,
		},
		{
			name:      "negative duration",
			input:     "-5s",
			wantValue: -5 * time.Second,
			wantIsSet: true,
		},
		{
			name:      "zero duration",
			input:     "0s",
			wantValue: 0,
			wantIsSet: true,
		},
		{
			name:      "special negative millisecond",
			input:     "-1ms",
			wantValue: -1 * time.Millisecond,
			wantIsSet: true,
		},
		{
			name:      "invalid format",
			input:     "invalid",
			wantErr:   true,
			wantIsSet: false,
		},
		{
			name:      "empty string",
			input:     "",
			wantErr:   true,
			wantIsSet: false,
		},
		{
			name:      "number without unit",
			input:     "42",
			wantErr:   true,
			wantIsSet: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var d Duration
			err := d.UnmarshalText([]byte(tt.input))

			if tt.wantErr {
				assert.Error(t, err)
				assert.False(t, d.IsSet)
				return
			}

			assert.NoError(t, err)
			assert.Equal(t, tt.wantValue, d.Duration)
			assert.Equal(t, tt.wantIsSet, d.IsSet)
		})
	}
}

func TestDurationMarshalText(t *testing.T) {
	tests := []struct {
		name     string
		duration Duration
		want     string
	}{
		{
			name: "marshal seconds",
			duration: Duration{
				Duration: 30 * time.Second,
				IsSet:    true,
			},
			want: "30s",
		},
		{
			name: "marshal complex duration",
			duration: Duration{
				Duration: time.Hour + 30*time.Minute + 45*time.Second,
				IsSet:    true,
			},
			want: "1h30m45s",
		},
		{
			name: "marshal negative duration",
			duration: Duration{
				Duration: -5 * time.Second,
				IsSet:    true,
			},
			want: "-5s",
		},
		{
			name: "marshal not set",
			duration: Duration{
				Duration: 30 * time.Second,
				IsSet:    false,
			},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := tt.duration.MarshalText()
			assert.NoError(t, err)
			assert.Equal(t, tt.want, string(data))
		})
	}
}

func TestDurationString(t *testing.T) {
	tests := []struct {
		name     string
		duration Duration
		want     string
	}{
		{
			name: "string representation",
			duration: Duration{
				Duration: 5 * time.Minute,
				IsSet:    true,
			},
			want: "5m0s",
		},
		{
			name: "string when not set",
			duration: Duration{
				Duration: 5 * time.Minute,
				IsSet:    false,
			},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.duration.String())
		})
	}
}
