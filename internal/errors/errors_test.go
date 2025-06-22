package errors

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestErrorTypes(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantType ErrorType
		wantMsg  string
	}{
		{
			name:     "validation error",
			err:      NewValidationError("invalid configuration"),
			wantType: ErrTypeValidation,
			wantMsg:  "validation error: invalid configuration",
		},
		{
			name:     "network error",
			err:      NewNetworkError("connection refused"),
			wantType: ErrTypeNetwork,
			wantMsg:  "network error: connection refused",
		},
		{
			name:     "configuration error",
			err:      NewConfigError("missing required field"),
			wantType: ErrTypeConfig,
			wantMsg:  "configuration error: missing required field",
		},
		{
			name:     "resource error",
			err:      NewResourceError("port already in use"),
			wantType: ErrTypeResource,
			wantMsg:  "resource error: port already in use",
		},
		{
			name:     "internal error",
			err:      NewInternalError("unexpected state"),
			wantType: ErrTypeInternal,
			wantMsg:  "internal error: unexpected state",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Check error message
			if got := tt.err.Error(); got != tt.wantMsg {
				t.Errorf("Error() = %q, want %q", got, tt.wantMsg)
			}

			// Check error type
			var typed *Error
			if !errors.As(tt.err, &typed) {
				t.Fatal("error should be of type *Error")
			}
			if typed.Type != tt.wantType {
				t.Errorf("Type = %v, want %v", typed.Type, tt.wantType)
			}
		})
	}
}

func TestWrap(t *testing.T) {
	baseErr := errors.New("base error")

	tests := []struct {
		name     string
		err      error
		wantType ErrorType
		wantMsg  string
	}{
		{
			name:     "wrap as validation error",
			err:      WrapValidation(baseErr, "invalid input"),
			wantType: ErrTypeValidation,
			wantMsg:  "validation error: invalid input: base error",
		},
		{
			name:     "wrap as network error",
			err:      WrapNetwork(baseErr, "connection failed"),
			wantType: ErrTypeNetwork,
			wantMsg:  "network error: connection failed: base error",
		},
		{
			name:     "wrap as config error",
			err:      WrapConfig(baseErr, "config parse failed"),
			wantType: ErrTypeConfig,
			wantMsg:  "configuration error: config parse failed: base error",
		},
		{
			name:     "wrap as resource error",
			err:      WrapResource(baseErr, "resource unavailable"),
			wantType: ErrTypeResource,
			wantMsg:  "resource error: resource unavailable: base error",
		},
		{
			name:     "wrap as internal error",
			err:      WrapInternal(baseErr, "unexpected failure"),
			wantType: ErrTypeInternal,
			wantMsg:  "internal error: unexpected failure: base error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Check error message
			if got := tt.err.Error(); got != tt.wantMsg {
				t.Errorf("Error() = %q, want %q", got, tt.wantMsg)
			}

			// Check error type
			var typed *Error
			if !errors.As(tt.err, &typed) {
				t.Fatal("error should be of type *Error")
			}
			if typed.Type != tt.wantType {
				t.Errorf("Type = %v, want %v", typed.Type, tt.wantType)
			}

			// Check that original error is preserved
			if !errors.Is(tt.err, baseErr) {
				t.Error("wrapped error should preserve original error")
			}
		})
	}
}

func TestIsType(t *testing.T) {
	validationErr := NewValidationError("test")
	networkErr := NewNetworkError("test")
	configErr := NewConfigError("test")
	resourceErr := NewResourceError("test")
	internalErr := NewInternalError("test")
	wrappedValidation := fmt.Errorf("wrapped: %w", validationErr)
	wrappedConfig := fmt.Errorf("wrapped: %w", configErr)
	wrappedResource := fmt.Errorf("wrapped: %w", resourceErr)
	wrappedInternal := fmt.Errorf("wrapped: %w", internalErr)

	tests := []struct {
		name    string
		err     error
		checkFn func(error) bool
		want    bool
	}{
		{
			name:    "direct validation error",
			err:     validationErr,
			checkFn: IsValidation,
			want:    true,
		},
		{
			name:    "wrapped validation error",
			err:     wrappedValidation,
			checkFn: IsValidation,
			want:    true,
		},
		{
			name:    "network error is not validation",
			err:     networkErr,
			checkFn: IsValidation,
			want:    false,
		},
		{
			name:    "direct network error",
			err:     networkErr,
			checkFn: IsNetwork,
			want:    true,
		},
		{
			name:    "direct config error",
			err:     configErr,
			checkFn: IsConfig,
			want:    true,
		},
		{
			name:    "wrapped config error",
			err:     wrappedConfig,
			checkFn: IsConfig,
			want:    true,
		},
		{
			name:    "validation error is not config",
			err:     validationErr,
			checkFn: IsConfig,
			want:    false,
		},
		{
			name:    "direct resource error",
			err:     resourceErr,
			checkFn: IsResource,
			want:    true,
		},
		{
			name:    "wrapped resource error",
			err:     wrappedResource,
			checkFn: IsResource,
			want:    true,
		},
		{
			name:    "network error is not resource",
			err:     networkErr,
			checkFn: IsResource,
			want:    false,
		},
		{
			name:    "direct internal error",
			err:     internalErr,
			checkFn: IsInternal,
			want:    true,
		},
		{
			name:    "wrapped internal error",
			err:     wrappedInternal,
			checkFn: IsInternal,
			want:    true,
		},
		{
			name:    "config error is not internal",
			err:     configErr,
			checkFn: IsInternal,
			want:    false,
		},
		{
			name:    "nil error",
			err:     nil,
			checkFn: IsValidation,
			want:    false,
		},
		{
			name:    "nil error for config check",
			err:     nil,
			checkFn: IsConfig,
			want:    false,
		},
		{
			name:    "nil error for resource check",
			err:     nil,
			checkFn: IsResource,
			want:    false,
		},
		{
			name:    "nil error for internal check",
			err:     nil,
			checkFn: IsInternal,
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.checkFn(tt.err); got != tt.want {
				t.Errorf("checkFn() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetType(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantType ErrorType
	}{
		{
			name:     "validation error",
			err:      NewValidationError("test"),
			wantType: ErrTypeValidation,
		},
		{
			name:     "wrapped validation error",
			err:      fmt.Errorf("context: %w", NewValidationError("test")),
			wantType: ErrTypeValidation,
		},
		{
			name:     "standard error",
			err:      errors.New("standard error"),
			wantType: ErrTypeUnknown,
		},
		{
			name:     "nil error",
			err:      nil,
			wantType: ErrTypeUnknown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := GetType(tt.err); got != tt.wantType {
				t.Errorf("GetType() = %v, want %v", got, tt.wantType)
			}
		})
	}
}

func TestHTTPStatus(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{
			name:       "validation error returns bad request",
			err:        NewValidationError("invalid input"),
			wantStatus: 400,
		},
		{
			name:       "network error returns bad gateway",
			err:        NewNetworkError("connection failed"),
			wantStatus: 502,
		},
		{
			name:       "resource error returns service unavailable",
			err:        NewResourceError("no resources"),
			wantStatus: 503,
		},
		{
			name:       "config error returns internal server error",
			err:        NewConfigError("bad config"),
			wantStatus: 500,
		},
		{
			name:       "internal error returns internal server error",
			err:        NewInternalError("internal failure"),
			wantStatus: 500,
		},
		{
			name:       "unknown error returns internal server error",
			err:        errors.New("unknown"),
			wantStatus: 500,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HTTPStatus(tt.err); got != tt.wantStatus {
				t.Errorf("HTTPStatus() = %v, want %v", got, tt.wantStatus)
			}
		})
	}
}

func TestErrorWithRetry(t *testing.T) {
	err := NewNetworkError("connection failed")
	retryable := WithRetry(err, 3, 5)

	// Check error message includes retry info
	want := "network error: connection failed (attempt 3/5)"
	if got := retryable.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}

	// Check we can extract retry info
	var retryErr *RetryableError
	if !errors.As(retryable, &retryErr) {
		t.Fatal("error should be of type *RetryableError")
	}

	if retryErr.Attempt != 3 {
		t.Errorf("Attempt = %d, want 3", retryErr.Attempt)
	}
	if retryErr.MaxAttempts != 5 {
		t.Errorf("MaxAttempts = %d, want 5", retryErr.MaxAttempts)
	}

	// Check IsRetryable
	if !IsRetryable(retryable) {
		t.Error("WithRetry error should be retryable")
	}

	// Check original error is preserved
	if !errors.Is(retryable, err) {
		t.Error("retryable error should preserve original error")
	}
}

func TestGetRetryInfo(t *testing.T) {
	tests := []struct {
		name            string
		err             error
		wantAttempt     int
		wantMaxAttempts int
		wantOk          bool
	}{
		{
			name:            "retryable error returns info",
			err:             WithRetry(NewNetworkError("test"), 2, 5),
			wantAttempt:     2,
			wantMaxAttempts: 5,
			wantOk:          true,
		},
		{
			name:            "wrapped retryable error returns info",
			err:             fmt.Errorf("context: %w", WithRetry(NewNetworkError("test"), 3, 10)),
			wantAttempt:     3,
			wantMaxAttempts: 10,
			wantOk:          true,
		},
		{
			name:            "non-retryable error returns false",
			err:             NewNetworkError("test"),
			wantAttempt:     0,
			wantMaxAttempts: 0,
			wantOk:          false,
		},
		{
			name:            "nil error returns false",
			err:             nil,
			wantAttempt:     0,
			wantMaxAttempts: 0,
			wantOk:          false,
		},
		{
			name:            "standard error returns false",
			err:             errors.New("standard error"),
			wantAttempt:     0,
			wantMaxAttempts: 0,
			wantOk:          false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotAttempt, gotMaxAttempts, gotOk := GetRetryInfo(tt.err)

			if gotAttempt != tt.wantAttempt {
				t.Errorf("GetRetryInfo() attempt = %v, want %v", gotAttempt, tt.wantAttempt)
			}
			if gotMaxAttempts != tt.wantMaxAttempts {
				t.Errorf("GetRetryInfo() maxAttempts = %v, want %v", gotMaxAttempts, tt.wantMaxAttempts)
			}
			if gotOk != tt.wantOk {
				t.Errorf("GetRetryInfo() ok = %v, want %v", gotOk, tt.wantOk)
			}
		})
	}
}

func TestIsRetryableEdgeCases(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "nil error is not retryable",
			err:  nil,
			want: false,
		},
		{
			name: "standard error is not retryable",
			err:  errors.New("standard error"),
			want: false,
		},
		{
			name: "typed error without retry is not retryable",
			err:  NewNetworkError("not retryable"),
			want: false,
		},
		{
			name: "deeply wrapped retryable error is retryable",
			err:  fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", WithRetry(NewNetworkError("test"), 1, 3))),
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsRetryable(tt.err); got != tt.want {
				t.Errorf("IsRetryable() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestServiceStartupError(t *testing.T) {
	t.Run("all services failed", func(t *testing.T) {
		failures := map[string]error{
			"service1": errors.New("connection refused"),
			"service2": errors.New("port already in use"),
			"service3": errors.New("invalid config"),
		}

		err := &ServiceStartupError{
			Total:      3,
			Successful: 0,
			Failed:     3,
			Failures:   failures,
		}

		// Verify error message
		msg := err.Error()
		if !strings.Contains(msg, "all 3 services failed") {
			t.Errorf("expected error message to indicate all services failed, got: %s", msg)
		}

		// Should include details about each failure
		for service, failure := range failures {
			if !strings.Contains(msg, service) {
				t.Errorf("expected error message to include service name %s, got: %s", service, msg)
			}
			if !strings.Contains(msg, failure.Error()) {
				t.Errorf("expected error message to include failure reason %s, got: %s", failure.Error(), msg)
			}
		}

		// Should be considered a total failure
		if !err.AllFailed() {
			t.Error("expected AllFailed() to return true when all services failed")
		}
	})

	t.Run("partial failure", func(t *testing.T) {
		failures := map[string]error{
			"service2": errors.New("backend unreachable"),
		}

		err := &ServiceStartupError{
			Total:      3,
			Successful: 2,
			Failed:     1,
			Failures:   failures,
		}

		// Verify error message
		msg := err.Error()
		if !strings.Contains(msg, "1 of 3 services failed") {
			t.Errorf("expected error message to indicate partial failure, got: %s", msg)
		}

		// Should include the failed service
		if !strings.Contains(msg, "service2") {
			t.Errorf("expected error message to include failed service name, got: %s", msg)
		}

		// Should NOT be considered a total failure
		if err.AllFailed() {
			t.Error("expected AllFailed() to return false when some services succeeded")
		}
	})

	t.Run("no failures returns nil", func(t *testing.T) {
		// When creating with no failures, should return nil
		err := NewServiceStartupError(5, 5, 0, nil)
		if err != nil {
			t.Errorf("expected nil when no services failed, got: %v", err)
		}
	})

	t.Run("empty failures map", func(t *testing.T) {
		err := &ServiceStartupError{
			Total:      2,
			Successful: 2,
			Failed:     0,
			Failures:   map[string]error{},
		}

		// Even with empty map, should indicate success
		if err.AllFailed() {
			t.Error("expected AllFailed() to return false with empty failures map")
		}
	})

	t.Run("error type checking", func(t *testing.T) {
		failures := map[string]error{
			"service1": errors.New("failed"),
		}

		err := NewServiceStartupError(1, 0, 1, failures)

		// Should be internal error type
		if !IsInternal(err) {
			t.Error("expected ServiceStartupError to be classified as internal error")
		}

		// Should be unwrappable to get the actual error
		var startupErr *ServiceStartupError
		if !errors.As(err, &startupErr) {
			t.Error("expected to be able to unwrap to ServiceStartupError")
		}
	})

	t.Run("constructor validation", func(t *testing.T) {
		// Test with valid partial failure
		err := NewServiceStartupError(3, 2, 1, map[string]error{
			"failed": errors.New("error"),
		})
		if err == nil {
			t.Error("expected error for partial failure")
		}

		// Test with all failed
		err = NewServiceStartupError(2, 0, 2, map[string]error{
			"svc1": errors.New("error1"),
			"svc2": errors.New("error2"),
		})
		if err == nil {
			t.Error("expected error when all services failed")
		}
	})

	t.Run("AsServiceStartupError helper", func(t *testing.T) {
		// Test with actual ServiceStartupError
		err := NewServiceStartupError(3, 1, 2, map[string]error{
			"svc1": errors.New("failed1"),
			"svc2": errors.New("failed2"),
		})

		startupErr, ok := AsServiceStartupError(err)
		if !ok {
			t.Error("expected AsServiceStartupError to return true for ServiceStartupError")
			return
		}
		if startupErr == nil {
			t.Error("expected non-nil ServiceStartupError")
			return
		}
		if startupErr.Total != 3 || startupErr.Failed != 2 {
			t.Errorf("unexpected values: total=%d, failed=%d", startupErr.Total, startupErr.Failed)
		}

		// Test with non-ServiceStartupError
		regularErr := errors.New("regular error")
		_, ok = AsServiceStartupError(regularErr)
		if ok {
			t.Error("expected AsServiceStartupError to return false for regular error")
		}

		// Test with nil
		_, ok = AsServiceStartupError(nil)
		if ok {
			t.Error("expected AsServiceStartupError to return false for nil")
		}
	})
}

func TestProviderErrorWrapping(t *testing.T) {
	tests := []struct {
		name         string
		provider     string
		operation    string
		baseErr      error
		wrapFunc     func(error, string, string) error
		wantType     ErrorType
		wantContains []string
	}{
		{
			name:      "file provider config error",
			provider:  "file",
			operation: "loading config",
			baseErr:   errors.New("file not found"),
			wrapFunc: func(err error, provider, operation string) error {
				return WrapProviderError(err, provider, ErrTypeConfig, operation)
			},
			wantType:     ErrTypeConfig,
			wantContains: []string{"file provider", "loading config", "file not found"},
		},
		{
			name:      "docker provider resource error",
			provider:  "docker",
			operation: "connecting to Docker",
			baseErr:   errors.New("connection refused"),
			wrapFunc: func(err error, provider, operation string) error {
				return WrapProviderError(err, provider, ErrTypeResource, operation)
			},
			wantType:     ErrTypeResource,
			wantContains: []string{"docker provider", "connecting to Docker", "connection refused"},
		},
		{
			name:      "validation error with provider context",
			provider:  "docker",
			operation: "parsing service config",
			baseErr:   errors.New("invalid backend address"),
			wrapFunc: func(err error, provider, operation string) error {
				return WrapProviderError(err, provider, ErrTypeValidation, operation)
			},
			wantType:     ErrTypeValidation,
			wantContains: []string{"docker provider", "parsing service config", "invalid backend address"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.wrapFunc(tt.baseErr, tt.provider, tt.operation)

			// Check error type
			if gotType := GetType(err); gotType != tt.wantType {
				t.Errorf("GetType() = %v, want %v", gotType, tt.wantType)
			}

			// Check error message contains expected strings
			errMsg := err.Error()
			for _, want := range tt.wantContains {
				if !contains(errMsg, want) {
					t.Errorf("error message %q does not contain %q", errMsg, want)
				}
			}
		})
	}
}

func TestNewProviderError(t *testing.T) {
	tests := []struct {
		name      string
		provider  string
		errType   ErrorType
		message   string
		wantError string
	}{
		{
			name:      "file provider config error",
			provider:  "file",
			errType:   ErrTypeConfig,
			message:   "invalid TOML syntax",
			wantError: "configuration error: : file provider: invalid TOML syntax",
		},
		{
			name:      "docker provider validation error",
			provider:  "docker",
			errType:   ErrTypeValidation,
			message:   "missing required label",
			wantError: "validation error: : docker provider: missing required label",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := NewProviderError(tt.provider, tt.errType, tt.message)

			if err.Error() != tt.wantError {
				t.Errorf("error = %q, want %q", err.Error(), tt.wantError)
			}

			if GetType(err) != tt.errType {
				t.Errorf("GetType() = %v, want %v", GetType(err), tt.errType)
			}
		})
	}
}

// Helper function for string contains check
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && containsSubstring(s, substr))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
