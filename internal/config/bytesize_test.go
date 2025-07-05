package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestByteSizeUnmarshalText(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantValue int64
		wantIsSet bool
		wantErr   bool
	}{
		// Plain numbers
		{
			name:      "plain number",
			input:     "1024",
			wantValue: 1024,
			wantIsSet: true,
		},
		{
			name:      "zero",
			input:     "0",
			wantValue: 0,
			wantIsSet: true,
		},
		{
			name:      "negative number",
			input:     "-1",
			wantValue: -1,
			wantIsSet: true,
		},
		// With units
		{
			name:      "kilobytes",
			input:     "10KB",
			wantValue: 10 * 1024,
			wantIsSet: true,
		},
		{
			name:      "megabytes",
			input:     "5MB",
			wantValue: 5 * 1024 * 1024,
			wantIsSet: true,
		},
		{
			name:      "gigabytes",
			input:     "2GB",
			wantValue: 2 * 1024 * 1024 * 1024,
			wantIsSet: true,
		},
		{
			name:      "terabytes",
			input:     "1TB",
			wantValue: 1024 * 1024 * 1024 * 1024,
			wantIsSet: true,
		},
		// Decimal values
		{
			name:      "decimal megabytes",
			input:     "1.5MB",
			wantValue: int64(1.5 * 1024 * 1024),
			wantIsSet: true,
		},
		{
			name:      "decimal gigabytes",
			input:     "0.5GB",
			wantValue: 512 * 1024 * 1024,
			wantIsSet: true,
		},
		// Case insensitive
		{
			name:      "lowercase units",
			input:     "100mb",
			wantValue: 100 * 1024 * 1024,
			wantIsSet: true,
		},
		{
			name:      "mixed case",
			input:     "50Mb",
			wantValue: 50 * 1024 * 1024,
			wantIsSet: true,
		},
		// IEC units (binary)
		{
			name:      "kibibytes",
			input:     "10KiB",
			wantValue: 10 * 1024,
			wantIsSet: true,
		},
		{
			name:      "mebibytes",
			input:     "5MiB",
			wantValue: 5 * 1024 * 1024,
			wantIsSet: true,
		},
		{
			name:      "gibibytes",
			input:     "2GiB",
			wantValue: 2 * 1024 * 1024 * 1024,
			wantIsSet: true,
		},
		// With spaces
		{
			name:      "spaces around",
			input:     "  100MB  ",
			wantValue: 100 * 1024 * 1024,
			wantIsSet: true,
		},
		// Negative with units
		{
			name:    "negative with units",
			input:   "-5MB",
			wantErr: true,
		},
		// Single letter units
		{
			name:      "single K",
			input:     "10K",
			wantValue: 10 * 1024,
			wantIsSet: true,
		},
		{
			name:      "single M",
			input:     "5M",
			wantValue: 5 * 1024 * 1024,
			wantIsSet: true,
		},
		{
			name:      "single G",
			input:     "2G",
			wantValue: 2 * 1024 * 1024 * 1024,
			wantIsSet: true,
		},
		// Edge cases
		{
			name:      "empty string",
			input:     "",
			wantValue: 0,
			wantIsSet: false,
		},
		{
			name:    "just unit",
			input:   "MB",
			wantErr: true,
		},
		{
			name:    "invalid unit",
			input:   "10XX",
			wantErr: true,
		},
		{
			name:    "invalid format",
			input:   "abc123",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var b ByteSize
			err := b.UnmarshalText([]byte(tt.input))

			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			assert.NoError(t, err)
			assert.Equal(t, tt.wantValue, b.Value)
			assert.Equal(t, tt.wantIsSet, b.IsSet)
		})
	}
}

func TestByteSizeString(t *testing.T) {
	tests := []struct {
		name  string
		value int64
		isSet bool
		want  string
	}{
		{
			name:  "not set",
			value: 0,
			isSet: false,
			want:  "",
		},
		{
			name:  "zero bytes",
			value: 0,
			isSet: true,
			want:  "0B",
		},
		{
			name:  "bytes",
			value: 512,
			isSet: true,
			want:  "512B",
		},
		{
			name:  "exact kilobytes",
			value: 10240,
			isSet: true,
			want:  "10KiB",
		},
		{
			name:  "fractional kilobytes",
			value: 10752,
			isSet: true,
			want:  "10.5KiB",
		},
		{
			name:  "exact megabytes",
			value: 5 * 1024 * 1024,
			isSet: true,
			want:  "5MiB",
		},
		{
			name:  "fractional megabytes",
			value: int64(5.5 * 1024 * 1024),
			isSet: true,
			want:  "5.5MiB",
		},
		{
			name:  "gigabytes",
			value: 2 * 1024 * 1024 * 1024,
			isSet: true,
			want:  "2GiB",
		},
		{
			name:  "terabytes",
			value: 1024 * 1024 * 1024 * 1024,
			isSet: true,
			want:  "1TiB",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := ByteSize{
				Value: tt.value,
				IsSet: tt.isSet,
			}
			assert.Equal(t, tt.want, b.String())
		})
	}
}

func TestByteSizeMarshalText(t *testing.T) {
	tests := []struct {
		name  string
		value int64
		isSet bool
		want  string
	}{
		{
			name:  "megabytes",
			value: 10 * 1024 * 1024,
			isSet: true,
			want:  "10MiB",
		},
		{
			name:  "not set",
			value: 0,
			isSet: false,
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := ByteSize{
				Value: tt.value,
				IsSet: tt.isSet,
			}
			data, err := b.MarshalText()
			assert.NoError(t, err)
			assert.Equal(t, tt.want, string(data))
		})
	}
}
