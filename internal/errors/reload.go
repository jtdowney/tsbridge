// Package errors provides standardized error types and handling for tsbridge.
package errors

import (
	"fmt"
	"strings"
)

// ReloadError represents errors that occurred during configuration reload
type ReloadError struct {
	AddErrors    map[string]error // Services that failed to add
	RemoveErrors map[string]error // Services that failed to remove
	UpdateErrors map[string]error // Services that failed to update
	Successful   int              // Number of successful operations
	Failed       int              // Number of failed operations
}

// Error implements the error interface
func (e *ReloadError) Error() string {
	var parts []string

	if e.Failed == 0 {
		return "configuration reload completed successfully"
	}

	parts = append(parts, fmt.Sprintf("configuration reload partially failed (%d errors, %d successful):",
		e.Failed, e.Successful))

	// Report removal errors first (cleanup failures)
	if len(e.RemoveErrors) > 0 {
		parts = append(parts, "\nFailed to remove services:")
		for name, err := range e.RemoveErrors {
			parts = append(parts, fmt.Sprintf("  - %s: %v", name, err))
		}
	}

	// Then update errors
	if len(e.UpdateErrors) > 0 {
		parts = append(parts, "\nFailed to update services:")
		for name, err := range e.UpdateErrors {
			parts = append(parts, fmt.Sprintf("  - %s: %v", name, err))
		}
	}

	// Finally addition errors
	if len(e.AddErrors) > 0 {
		parts = append(parts, "\nFailed to add services:")
		for name, err := range e.AddErrors {
			parts = append(parts, fmt.Sprintf("  - %s: %v", name, err))
		}
	}

	return strings.Join(parts, "")
}

// HasErrors returns true if there were any errors during reload
func (e *ReloadError) HasErrors() bool {
	return e.Failed > 0
}

// AllFailed returns true if all operations failed
func (e *ReloadError) AllFailed() bool {
	return e.Failed > 0 && e.Successful == 0
}

// NewReloadError creates a new reload error if there were any failures
func NewReloadError() *ReloadError {
	return &ReloadError{
		AddErrors:    make(map[string]error),
		RemoveErrors: make(map[string]error),
		UpdateErrors: make(map[string]error),
	}
}

// RecordAddError records a service addition error
func (e *ReloadError) RecordAddError(serviceName string, err error) {
	e.AddErrors[serviceName] = err
	e.Failed++
}

// RecordRemoveError records a service removal error
func (e *ReloadError) RecordRemoveError(serviceName string, err error) {
	e.RemoveErrors[serviceName] = err
	e.Failed++
}

// RecordUpdateError records a service update error
func (e *ReloadError) RecordUpdateError(serviceName string, err error) {
	e.UpdateErrors[serviceName] = err
	e.Failed++
}

// RecordSuccess increments the successful operation counter
func (e *ReloadError) RecordSuccess() {
	e.Successful++
}

// ToError returns nil if no errors occurred, otherwise returns the ReloadError
func (e *ReloadError) ToError() error {
	if !e.HasErrors() {
		return nil
	}
	return e
}
