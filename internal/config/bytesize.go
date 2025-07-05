package config

import (
	"fmt"
	"strconv"
	"strings"
)

// ByteSize represents a size in bytes with support for human-readable parsing
type ByteSize struct {
	Value int64 // Size in bytes
	IsSet bool  `mapstructure:"-" toml:"-" json:"-"` // Track if explicitly set
}

// UnmarshalText implements encoding.TextUnmarshaler for ByteSize
// Supports formats like: 1024, "1KB", "10MB", "1.5GB", "100MiB"
// For compatibility, we treat KB/MB/GB as binary units (1KB = 1024 bytes)
func (b *ByteSize) UnmarshalText(text []byte) error {
	s := strings.TrimSpace(string(text))
	if s == "" {
		b.Value = 0
		b.IsSet = false
		return nil
	}

	// Try to parse as plain number first
	if v, err := strconv.ParseInt(s, 10, 64); err == nil {
		b.Value = v
		b.IsSet = true
		return nil
	}

	// Try to parse as float with unit suffix
	var value float64
	var unit string

	// Find where the number ends and unit begins
	i := 0
	for i < len(s) && (s[i] >= '0' && s[i] <= '9' || s[i] == '.' || s[i] == ' ') {
		i++
	}

	if i == 0 || i == len(s) {
		return fmt.Errorf("invalid byte size format: %q", s)
	}

	// Parse the numeric part
	numStr := strings.TrimSpace(s[:i])
	unit = strings.TrimSpace(s[i:])

	value, err := strconv.ParseFloat(numStr, 64)
	if err != nil {
		return fmt.Errorf("invalid byte size format: %q", s)
	}

	if value < 0 {
		return fmt.Errorf("byte size cannot be negative: %q", s)
	}

	// Convert unit to bytes
	var multiplier int64
	switch strings.ToUpper(unit) {
	case "B", "BYTE", "BYTES":
		multiplier = 1
	case "K", "KB":
		multiplier = 1024
	case "KIB":
		multiplier = 1024
	case "M", "MB":
		multiplier = 1024 * 1024
	case "MIB":
		multiplier = 1024 * 1024
	case "G", "GB":
		multiplier = 1024 * 1024 * 1024
	case "GIB":
		multiplier = 1024 * 1024 * 1024
	case "T", "TB":
		multiplier = 1024 * 1024 * 1024 * 1024
	case "TIB":
		multiplier = 1024 * 1024 * 1024 * 1024
	case "P", "PB":
		multiplier = 1024 * 1024 * 1024 * 1024 * 1024
	case "PIB":
		multiplier = 1024 * 1024 * 1024 * 1024 * 1024
	default:
		return fmt.Errorf("unknown unit %q in byte size: %q", unit, s)
	}

	b.Value = int64(value * float64(multiplier))
	b.IsSet = true
	return nil
}

// String returns the human-readable representation
func (b ByteSize) String() string {
	if !b.IsSet {
		return ""
	}

	// Format as human-readable
	const (
		KB = 1024
		MB = 1024 * KB
		GB = 1024 * MB
		TB = 1024 * GB
		PB = 1024 * TB
	)

	switch {
	case b.Value >= PB:
		return formatSize(b.Value, PB, "PiB")
	case b.Value >= TB:
		return formatSize(b.Value, TB, "TiB")
	case b.Value >= GB:
		return formatSize(b.Value, GB, "GiB")
	case b.Value >= MB:
		return formatSize(b.Value, MB, "MiB")
	case b.Value >= KB:
		return formatSize(b.Value, KB, "KiB")
	default:
		return fmt.Sprintf("%dB", b.Value)
	}
}

// formatSize formats a size value with the given divisor and unit
func formatSize(value int64, divisor int64, unit string) string {
	result := float64(value) / float64(divisor)
	if result == float64(int64(result)) {
		return fmt.Sprintf("%d%s", int64(result), unit)
	}
	return fmt.Sprintf("%.1f%s", result, unit)
}

// MarshalText implements encoding.TextMarshaler
func (b ByteSize) MarshalText() ([]byte, error) {
	return []byte(b.String()), nil
}
