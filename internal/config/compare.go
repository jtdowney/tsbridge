// Package config handles configuration parsing and validation for tsbridge.
package config

import (
	"reflect"
	"slices"
)

// ServiceConfigEqual compares two service configurations and returns true if they are equal.
// This function is used to determine if a service needs to be restarted when configuration changes.
// It compares all fields that would require a service restart if changed.
func ServiceConfigEqual(a, b Service) bool {
	// Compare basic fields
	if a.Name != b.Name {
		return false
	}
	if a.BackendAddr != b.BackendAddr {
		return false
	}
	if a.TLSMode != b.TLSMode {
		return false
	}
	if a.Ephemeral != b.Ephemeral {
		return false
	}

	// Compare pointer fields
	if !boolPtrEqual(a.FunnelEnabled, b.FunnelEnabled) {
		return false
	}
	if !boolPtrEqual(a.WhoisEnabled, b.WhoisEnabled) {
		return false
	}
	if !boolPtrEqual(a.AccessLog, b.AccessLog) {
		return false
	}

	// Compare Duration fields
	if !durationEqual(a.WhoisTimeout, b.WhoisTimeout) {
		return false
	}
	if !durationEqual(a.ReadHeaderTimeout, b.ReadHeaderTimeout) {
		return false
	}
	if !durationEqual(a.WriteTimeout, b.WriteTimeout) {
		return false
	}
	if !durationEqual(a.IdleTimeout, b.IdleTimeout) {
		return false
	}
	if !durationEqual(a.ResponseHeaderTimeout, b.ResponseHeaderTimeout) {
		return false
	}
	if !durationEqual(a.FlushInterval, b.FlushInterval) {
		return false
	}

	// Compare slice fields
	if !stringSliceEqualUnordered(a.Tags, b.Tags) {
		return false
	}
	if !stringSliceEqual(a.RemoveUpstream, b.RemoveUpstream) {
		return false
	}
	if !stringSliceEqual(a.RemoveDownstream, b.RemoveDownstream) {
		return false
	}

	// Compare map fields
	if !stringMapEqual(a.UpstreamHeaders, b.UpstreamHeaders) {
		return false
	}
	if !stringMapEqual(a.DownstreamHeaders, b.DownstreamHeaders) {
		return false
	}

	// Compare ByteSize pointer field
	if !byteSizePtrEqual(a.MaxRequestBodySize, b.MaxRequestBodySize) {
		return false
	}

	return true
}

// boolPtrEqual compares two bool pointers
func boolPtrEqual(a, b *bool) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

// byteSizePtrEqual compares two ByteSize pointers
func byteSizePtrEqual(a, b *ByteSize) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.Value == b.Value && a.IsSet == b.IsSet
}

// durationEqual compares two Duration values
func durationEqual(a, b Duration) bool {
	// Compare both the duration value and whether it was explicitly set
	return a.Duration == b.Duration && a.IsSet == b.IsSet
}

// stringSliceEqual compares two string slices
// Treats nil and empty slices as equal for practical purposes
func stringSliceEqual(a, b []string) bool {
	// Treat nil and empty slices as equal
	if len(a) == 0 && len(b) == 0 {
		return true
	}

	// If lengths differ, they're not equal
	if len(a) != len(b) {
		return false
	}

	// Compare each element
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}

	return true
}

// stringSliceEqualUnordered compares two string slices, ignoring order.
// Treats nil and empty slices as equal.
func stringSliceEqualUnordered(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}

	if len(a) == 0 {
		return true
	}

	// Create copies to avoid modifying the original slices
	aCopy := slices.Clone(a)
	bCopy := slices.Clone(b)

	slices.Sort(aCopy)
	slices.Sort(bCopy)

	return slices.Equal(aCopy, bCopy)
}

// stringMapEqual compares two string maps
// Treats nil and empty maps as equal for practical purposes
func stringMapEqual(a, b map[string]string) bool {
	// Treat nil and empty maps as equal
	if len(a) == 0 && len(b) == 0 {
		return true
	}

	// Use reflect.DeepEqual for maps to handle all cases correctly
	return reflect.DeepEqual(a, b)
}
