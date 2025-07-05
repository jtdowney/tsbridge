// Package config handles configuration parsing and validation for tsbridge.
package config

import (
	"time"
)

// Duration wraps time.Duration for TOML unmarshaling with explicit tracking
type Duration struct {
	time.Duration      // Embedded time.Duration value
	IsSet         bool `mapstructure:"-" toml:"-" json:"-"` // Track if explicitly set
}

// UnmarshalText implements encoding.TextUnmarshaler for Duration
func (d *Duration) UnmarshalText(text []byte) error {
	var err error
	d.Duration, err = time.ParseDuration(string(text))
	if err == nil {
		d.IsSet = true
	}
	return err
}

// MarshalText implements encoding.TextMarshaler for Duration
func (d Duration) MarshalText() ([]byte, error) {
	if !d.IsSet {
		return []byte{}, nil
	}
	return []byte(d.Duration.String()), nil
}

// String returns the string representation of the duration
func (d Duration) String() string {
	if !d.IsSet {
		return ""
	}
	return d.Duration.String()
}
