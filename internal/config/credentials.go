package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jtdowney/tsbridge/internal/errors"
)

// validateFilePath validates that a file path is safe to use and doesn't contain
// directory traversal attempts or other security issues
func validateFilePath(path string) error {
	if path == "" {
		return errors.NewValidationError("empty file path")
	}

	// Path must be absolute
	if !filepath.IsAbs(path) {
		return errors.NewValidationError("file path must be absolute")
	}

	// Check for null bytes
	if strings.Contains(path, "\x00") {
		return errors.NewValidationError("invalid file path: contains null bytes")
	}

	// Check for directory traversal attempts BEFORE cleaning
	// This is important because filepath.Clean would resolve .. components
	if strings.Contains(path, "..") {
		return errors.NewValidationError("invalid file path: contains directory traversal")
	}

	// Additional safety: ensure no path components are . or ..
	parts := strings.Split(path, string(filepath.Separator))
	for _, part := range parts {
		if part == ".." || part == "." {
			return errors.NewValidationError("invalid file path: contains directory traversal")
		}
	}

	return nil
}

// ResolveSecret resolves a secret value from either an environment variable or file.
// It supports values in the format:
// - "env:VAR_NAME" - reads from environment variable VAR_NAME
// - "file:/path/to/file" - reads from the specified file
// - Any other value is returned as-is
func ResolveSecret(value, envVar, filePath string) (string, error) {
	// Priority 1: Direct value
	if value != "" {
		return value, nil
	}

	// Priority 2: Environment variable
	if envVar != "" {
		return os.Getenv(envVar), nil
	}

	// Priority 3: File
	if filePath != "" {
		// Validate the file path for security
		if err := validateFilePath(filePath); err != nil {
			return "", err
		}

		data, err := os.ReadFile(filePath)
		if err != nil {
			return "", fmt.Errorf("reading secret file: %w", err)
		}
		return strings.TrimSpace(string(data)), nil
	}

	// No sources configured
	return "", nil
}

// ResolveSecretWithFallback resolves a secret value with an additional fallback environment variable.
// Priority order:
// 1. Direct value (if not empty)
// 2. Environment variable (if envVar specified)
// 3. File content (if filePath specified)
// 4. Fallback environment variable (if fallbackEnv specified)
// Returns empty string if no sources are configured or have values.
func ResolveSecretWithFallback(value, envVar, filePath, fallbackEnv string) (string, error) {
	// Try the primary sources first
	result, err := ResolveSecret(value, envVar, filePath)
	if err != nil {
		return "", err
	}
	if result != "" {
		return result, nil
	}

	// Try fallback environment variable
	if fallbackEnv != "" {
		return os.Getenv(fallbackEnv), nil
	}

	return "", nil
}

// ValidateSecret checks if a secret can be resolved without actually loading it.
// This is useful for pre-flight validation to ensure secrets are available
// before starting the application.
func ValidateSecret(value, envVar, filePath string) error {
	// Priority 1: Direct value
	if value != "" {
		return nil
	}

	// Priority 2: Environment variable
	if envVar != "" {
		if os.Getenv(envVar) == "" {
			return fmt.Errorf("environment variable %s is not set", envVar)
		}
		return nil
	}

	// Priority 3: File
	if filePath != "" {
		// Validate the file path for security
		if err := validateFilePath(filePath); err != nil {
			return fmt.Errorf("invalid file path: %w", err)
		}

		info, err := os.Stat(filePath)
		if err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("file %s does not exist", filePath)
			}
			return fmt.Errorf("checking file %s: %w", filePath, err)
		}

		// Check if file is readable
		file, err := os.Open(filePath)
		if err != nil {
			return fmt.Errorf("cannot read file %s: %w", filePath, err)
		}
		file.Close()

		// Warn if file is empty
		if info.Size() == 0 {
			return fmt.Errorf("file %s is empty", filePath)
		}

		return nil
	}

	// No sources configured
	return fmt.Errorf("no secret source configured")
}

// ValidateSecretWithFallback validates that a secret can be resolved with fallback support.
// It checks all configured sources without actually loading the secret values.
func ValidateSecretWithFallback(value, envVar, filePath, fallbackEnv string) error {
	// Try the primary sources first
	err := ValidateSecret(value, envVar, filePath)
	if err == nil {
		return nil
	}

	// If primary validation failed but we have a fallback, check the fallback
	if fallbackEnv != "" {
		if os.Getenv(fallbackEnv) != "" {
			return nil
		}
		// If we get here, primary sources failed and fallback is not set
		// Return the original error from primary validation
		return err
	}

	// No fallback configured, return the primary error
	return err
}
