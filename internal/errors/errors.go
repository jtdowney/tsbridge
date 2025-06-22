// Package errors provides standardized error types and handling for tsbridge.
// It implements error classification, wrapping, and utility functions for
// consistent error handling across the codebase.
package errors

import (
	"errors"
	"fmt"
	"net/http"
)

// ErrorType represents the category of error
type ErrorType string

const (
	// ErrTypeUnknown is for errors that don't fit other categories
	ErrTypeUnknown ErrorType = "unknown"

	// ErrTypeValidation is for input validation errors
	ErrTypeValidation ErrorType = "validation"

	// ErrTypeNetwork is for network-related errors (connection, timeout, etc)
	ErrTypeNetwork ErrorType = "network"

	// ErrTypeConfig is for configuration errors
	ErrTypeConfig ErrorType = "config"

	// ErrTypeResource is for resource availability errors (ports, files, etc)
	ErrTypeResource ErrorType = "resource"

	// ErrTypeInternal is for internal/unexpected errors
	ErrTypeInternal ErrorType = "internal"
)

// Error is the standard error type with classification
type Error struct {
	Type    ErrorType
	Message string
	Err     error
}

// Error implements the error interface
func (e *Error) Error() string {
	typeStr := string(e.Type)
	if e.Type == ErrTypeConfig {
		typeStr = "configuration"
	}

	if e.Err != nil {
		return fmt.Sprintf("%s error: %s: %v", typeStr, e.Message, e.Err)
	}
	return fmt.Sprintf("%s error: %s", typeStr, e.Message)
}

// Unwrap allows errors.Is and errors.As to work
func (e *Error) Unwrap() error {
	return e.Err
}

// NewValidationError creates a new validation error
func NewValidationError(message string) error {
	return &Error{
		Type:    ErrTypeValidation,
		Message: message,
	}
}

// NewNetworkError creates a new network error
func NewNetworkError(message string) error {
	return &Error{
		Type:    ErrTypeNetwork,
		Message: message,
	}
}

// NewConfigError creates a new configuration error
func NewConfigError(message string) error {
	return &Error{
		Type:    ErrTypeConfig,
		Message: message,
	}
}

// NewResourceError creates a new resource error
func NewResourceError(message string) error {
	return &Error{
		Type:    ErrTypeResource,
		Message: message,
	}
}

// NewInternalError creates a new internal error
func NewInternalError(message string) error {
	return &Error{
		Type:    ErrTypeInternal,
		Message: message,
	}
}

// WrapValidation wraps an error as a validation error
func WrapValidation(err error, message string) error {
	return &Error{
		Type:    ErrTypeValidation,
		Message: message,
		Err:     err,
	}
}

// WrapNetwork wraps an error as a network error
func WrapNetwork(err error, message string) error {
	return &Error{
		Type:    ErrTypeNetwork,
		Message: message,
		Err:     err,
	}
}

// WrapConfig wraps an error as a configuration error
func WrapConfig(err error, message string) error {
	return &Error{
		Type:    ErrTypeConfig,
		Message: message,
		Err:     err,
	}
}

// WrapResource wraps an error as a resource error
func WrapResource(err error, message string) error {
	return &Error{
		Type:    ErrTypeResource,
		Message: message,
		Err:     err,
	}
}

// WrapInternal wraps an error as an internal error
func WrapInternal(err error, message string) error {
	return &Error{
		Type:    ErrTypeInternal,
		Message: message,
		Err:     err,
	}
}

// IsValidation checks if an error is a validation error
func IsValidation(err error) bool {
	return isType(err, ErrTypeValidation)
}

// IsNetwork checks if an error is a network error
func IsNetwork(err error) bool {
	return isType(err, ErrTypeNetwork)
}

// IsConfig checks if an error is a configuration error
func IsConfig(err error) bool {
	return isType(err, ErrTypeConfig)
}

// IsResource checks if an error is a resource error
func IsResource(err error) bool {
	return isType(err, ErrTypeResource)
}

// IsInternal checks if an error is an internal error
func IsInternal(err error) bool {
	return isType(err, ErrTypeInternal)
}

// isType checks if an error is of a specific type
func isType(err error, errType ErrorType) bool {
	if err == nil {
		return false
	}
	var e *Error
	if errors.As(err, &e) {
		return e.Type == errType
	}
	return false
}

// GetType returns the error type for an error
func GetType(err error) ErrorType {
	if err == nil {
		return ErrTypeUnknown
	}
	var e *Error
	if errors.As(err, &e) {
		return e.Type
	}
	return ErrTypeUnknown
}

// HTTPStatus returns the appropriate HTTP status code for an error
func HTTPStatus(err error) int {
	switch GetType(err) {
	case ErrTypeValidation:
		return http.StatusBadRequest
	case ErrTypeNetwork:
		return http.StatusBadGateway
	case ErrTypeResource:
		return http.StatusServiceUnavailable
	case ErrTypeConfig, ErrTypeInternal:
		return http.StatusInternalServerError
	default:
		return http.StatusInternalServerError
	}
}

// RetryableError wraps an error with retry information
type RetryableError struct {
	Err         error
	Attempt     int
	MaxAttempts int
}

// Error implements the error interface
func (r *RetryableError) Error() string {
	base := r.Err.Error()
	return fmt.Sprintf("%s (attempt %d/%d)", base, r.Attempt, r.MaxAttempts)
}

// Unwrap allows errors.Is and errors.As to work
func (r *RetryableError) Unwrap() error {
	return r.Err
}

// WithRetry wraps an error with retry information
func WithRetry(err error, attempt, maxAttempts int) error {
	return &RetryableError{
		Err:         err,
		Attempt:     attempt,
		MaxAttempts: maxAttempts,
	}
}

// IsRetryable checks if an error is marked as retryable
func IsRetryable(err error) bool {
	if err == nil {
		return false
	}
	var r *RetryableError
	return errors.As(err, &r)
}

// GetRetryInfo extracts retry information from an error
func GetRetryInfo(err error) (attempt, maxAttempts int, ok bool) {
	var r *RetryableError
	if errors.As(err, &r) {
		return r.Attempt, r.MaxAttempts, true
	}
	return 0, 0, false
}

// ServiceStartupError represents the result of attempting to start multiple services
type ServiceStartupError struct {
	Total      int              // Total number of services attempted
	Successful int              // Number of services that started successfully
	Failed     int              // Number of services that failed to start
	Failures   map[string]error // Map of service name to error for failed services
}

// Error implements the error interface
func (e *ServiceStartupError) Error() string {
	if e.Failed == e.Total {
		// All services failed
		msg := fmt.Sprintf("all %d services failed to start:", e.Total)
		for service, err := range e.Failures {
			msg += fmt.Sprintf("\n  - %s: %v", service, err)
		}
		return msg
	}

	// Partial failure
	msg := fmt.Sprintf("%d of %d services failed to start:", e.Failed, e.Total)
	for service, err := range e.Failures {
		msg += fmt.Sprintf("\n  - %s: %v", service, err)
	}
	return msg
}

// AllFailed returns true if all services failed to start
func (e *ServiceStartupError) AllFailed() bool {
	return e.Failed == e.Total && e.Total > 0
}

// NewServiceStartupError creates a new service startup error if there were any failures
func NewServiceStartupError(total, successful, failed int, failures map[string]error) error {
	if failed == 0 || len(failures) == 0 {
		return nil
	}

	return &Error{
		Type:    ErrTypeInternal,
		Message: "service startup",
		Err: &ServiceStartupError{
			Total:      total,
			Successful: successful,
			Failed:     failed,
			Failures:   failures,
		},
	}
}

// AsServiceStartupError checks if an error is a ServiceStartupError and returns it
func AsServiceStartupError(err error) (*ServiceStartupError, bool) {
	if err == nil {
		return nil, false
	}

	// First unwrap the Error wrapper if present
	var e *Error
	if errors.As(err, &e) && e.Err != nil {
		err = e.Err
	}

	var startupErr *ServiceStartupError
	if errors.As(err, &startupErr) {
		return startupErr, true
	}
	return nil, false
}

// ProviderError represents an error from a configuration provider.
// It includes the provider name for context in error messages.
type ProviderError struct {
	Provider string
	Type     ErrorType
	Message  string
	Cause    error
}

// Error implements the error interface
func (e *ProviderError) Error() string {
	if e.Cause != nil {
		return e.Provider + " provider: " + e.Message + ": " + e.Cause.Error()
	}
	return e.Provider + " provider: " + e.Message
}

// Unwrap returns the underlying error
func (e *ProviderError) Unwrap() error {
	return e.Cause
}

// NewProviderError creates a new provider error without a cause
func NewProviderError(provider string, errType ErrorType, message string) error {
	return &Error{
		Type: errType,
		Err: &ProviderError{
			Provider: provider,
			Type:     errType,
			Message:  message,
		},
	}
}

// WrapProviderError wraps an error with provider context
func WrapProviderError(err error, provider string, errType ErrorType, operation string) error {
	if err == nil {
		return nil
	}
	return &Error{
		Type: errType,
		Err: &ProviderError{
			Provider: provider,
			Type:     errType,
			Message:  operation,
			Cause:    err,
		},
	}
}
