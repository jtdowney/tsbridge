// Package testhelpers provides simple test utility functions that can be used across all test packages.
package testhelpers

import "time"

// DurationPtr returns a pointer to a time.Duration
//
//go:fix inline
func DurationPtr(d time.Duration) *time.Duration {
	return new(d)
}

// Int64Ptr returns a pointer to an int64
//
//go:fix inline
func Int64Ptr(i int64) *int64 {
	return new(i)
}

// BoolPtr returns a pointer to a bool
//
//go:fix inline
func BoolPtr(b bool) *bool {
	return new(b)
}
