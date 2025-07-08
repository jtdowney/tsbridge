// Package testhelpers provides simple test utility functions that can be used across all test packages.
package testhelpers

import "time"

// DurationPtr returns a pointer to a time.Duration
func DurationPtr(d time.Duration) *time.Duration {
	return &d
}

// Int64Ptr returns a pointer to an int64
func Int64Ptr(i int64) *int64 {
	return &i
}

// BoolPtr returns a pointer to a bool
func BoolPtr(b bool) *bool {
	return &b
}
