package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResolveSecret(t *testing.T) {
	tests := []struct {
		name      string
		value     string
		envVar    string
		filePath  string
		envValue  string
		fileValue string
		want      string
		wantErr   bool
	}{
		{
			name:      "direct value takes precedence",
			value:     "direct-secret",
			envVar:    "TEST_ENV",
			filePath:  "/tmp/test.txt",
			envValue:  "env-secret",
			fileValue: "file-secret",
			want:      "direct-secret",
			wantErr:   false,
		},
		{
			name:      "env var when no direct value",
			value:     "",
			envVar:    "TEST_ENV",
			filePath:  "/tmp/test.txt",
			envValue:  "env-secret",
			fileValue: "file-secret",
			want:      "env-secret",
			wantErr:   false,
		},
		{
			name:      "file when no direct or env value",
			value:     "",
			envVar:    "",
			filePath:  "", // will be set to temp file
			envValue:  "",
			fileValue: "file-secret",
			want:      "file-secret",
			wantErr:   false,
		},
		{
			name:      "empty string when all sources empty",
			value:     "",
			envVar:    "",
			filePath:  "",
			envValue:  "",
			fileValue: "",
			want:      "",
			wantErr:   false,
		},
		{
			name:      "returns empty string when env var is empty",
			value:     "",
			envVar:    "TEST_ENV_EMPTY",
			filePath:  "",
			envValue:  "",
			fileValue: "",
			want:      "",
			wantErr:   false,
		},
		{
			name:      "error when file path specified but doesn't exist",
			value:     "",
			envVar:    "",
			filePath:  "/non/existent/file.txt",
			envValue:  "",
			fileValue: "",
			want:      "",
			wantErr:   true,
		},
		{
			name:      "trims whitespace from file content",
			value:     "",
			envVar:    "",
			filePath:  "", // will be set to temp file
			envValue:  "",
			fileValue: "  file-secret-with-spaces\n\t",
			want:      "file-secret-with-spaces",
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set up environment
			if tt.envVar != "" {
				os.Setenv(tt.envVar, tt.envValue)
				defer os.Unsetenv(tt.envVar)
			}

			// Create temp file if needed
			if tt.filePath == "" && tt.fileValue != "" {
				tmpFile := filepath.Join(t.TempDir(), "secret.txt")
				if err := os.WriteFile(tmpFile, []byte(tt.fileValue), 0600); err != nil {
					t.Fatalf("failed to create temp file: %v", err)
				}
				tt.filePath = tmpFile
			}

			// Call the function
			got, err := ResolveSecret(tt.value, tt.envVar, tt.filePath)

			// Check error
			if (err != nil) != tt.wantErr {
				t.Errorf("ResolveSecret() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			// Check result
			if got != tt.want {
				t.Errorf("ResolveSecret() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestResolveSecretWithFallback(t *testing.T) {
	tests := []struct {
		name          string
		value         string
		envVar        string
		filePath      string
		fallbackEnv   string
		envValue      string
		fileValue     string
		fallbackValue string
		want          string
		wantErr       bool
	}{
		{
			name:          "uses fallback env when no other sources",
			value:         "",
			envVar:        "",
			filePath:      "",
			fallbackEnv:   "FALLBACK_ENV",
			envValue:      "",
			fileValue:     "",
			fallbackValue: "fallback-secret",
			want:          "fallback-secret",
			wantErr:       false,
		},
		{
			name:          "direct value takes precedence over fallback",
			value:         "direct-secret",
			envVar:        "",
			filePath:      "",
			fallbackEnv:   "FALLBACK_ENV",
			envValue:      "",
			fileValue:     "",
			fallbackValue: "fallback-secret",
			want:          "direct-secret",
			wantErr:       false,
		},
		{
			name:          "env var takes precedence over fallback",
			value:         "",
			envVar:        "PRIMARY_ENV",
			filePath:      "",
			fallbackEnv:   "FALLBACK_ENV",
			envValue:      "primary-secret",
			fileValue:     "",
			fallbackValue: "fallback-secret",
			want:          "primary-secret",
			wantErr:       false,
		},
		{
			name:          "file takes precedence over fallback",
			value:         "",
			envVar:        "",
			filePath:      "", // Will be set to actual temp file path
			fallbackEnv:   "FALLBACK_ENV",
			envValue:      "",
			fileValue:     "file-secret",
			fallbackValue: "fallback-secret",
			want:          "file-secret",
			wantErr:       false,
		},
		{
			name:          "error reading file doesn't fall back to fallback",
			value:         "",
			envVar:        "",
			filePath:      "/nonexistent/file",
			fallbackEnv:   "FALLBACK_ENV",
			envValue:      "",
			fileValue:     "",
			fallbackValue: "fallback-secret",
			want:          "",
			wantErr:       true,
		},
		{
			name:          "empty when all sources including fallback are empty",
			value:         "",
			envVar:        "",
			filePath:      "",
			fallbackEnv:   "FALLBACK_ENV",
			envValue:      "",
			fileValue:     "",
			fallbackValue: "",
			want:          "",
			wantErr:       false,
		},
		{
			name:          "fallback returns empty when env var is set but empty",
			value:         "",
			envVar:        "",
			filePath:      "",
			fallbackEnv:   "EMPTY_FALLBACK_ENV",
			envValue:      "",
			fileValue:     "",
			fallbackValue: "",
			want:          "",
			wantErr:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set up environment
			if tt.envVar != "" {
				os.Setenv(tt.envVar, tt.envValue)
				defer os.Unsetenv(tt.envVar)
			}
			if tt.fallbackEnv != "" {
				os.Setenv(tt.fallbackEnv, tt.fallbackValue)
				defer os.Unsetenv(tt.fallbackEnv)
			}

			// Create temp file if needed
			filePath := tt.filePath
			if filePath == "" && tt.fileValue != "" {
				tmpFile := filepath.Join(t.TempDir(), "secret.txt")
				if err := os.WriteFile(tmpFile, []byte(tt.fileValue), 0600); err != nil {
					t.Fatalf("failed to create temp file: %v", err)
				}
				filePath = tmpFile
			}

			// Call the function
			got, err := ResolveSecretWithFallback(tt.value, tt.envVar, filePath, tt.fallbackEnv)

			// Check error
			if (err != nil) != tt.wantErr {
				t.Errorf("ResolveSecretWithFallback() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			// Check result
			if got != tt.want {
				t.Errorf("ResolveSecretWithFallback() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValidateSecret(t *testing.T) {
	tests := []struct {
		name      string
		value     string
		envVar    string
		filePath  string
		envValue  string
		setupFile func() string
		wantErr   bool
		wantMsg   string
	}{
		{
			name:    "direct value is valid",
			value:   "secret",
			wantErr: false,
		},
		{
			name:     "env var exists",
			envVar:   "TEST_SECRET",
			envValue: "secret",
			wantErr:  false,
		},
		{
			name:    "env var not set",
			envVar:  "NONEXISTENT_VAR",
			wantErr: true,
			wantMsg: "environment variable NONEXISTENT_VAR is not set",
		},
		{
			name: "file exists and readable",
			setupFile: func() string {
				tmpFile := filepath.Join(t.TempDir(), "secret.txt")
				if err := os.WriteFile(tmpFile, []byte("secret"), 0600); err != nil {
					t.Fatalf("failed to create test file: %v", err)
				}
				return tmpFile
			},
			wantErr: false,
		},
		{
			name:     "file does not exist",
			filePath: "/nonexistent/file.txt",
			wantErr:  true,
			wantMsg:  "file /nonexistent/file.txt does not exist",
		},
		{
			name: "file exists but not readable",
			setupFile: func() string {
				tmpFile := filepath.Join(t.TempDir(), "secret.txt")
				if err := os.WriteFile(tmpFile, []byte("secret"), 0000); err != nil {
					t.Fatalf("failed to create test file: %v", err)
				}
				return tmpFile
			},
			wantErr: true,
			wantMsg: "cannot read file",
		},
		{
			name:    "no source configured",
			wantErr: true,
			wantMsg: "no secret source configured",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup
			if tt.envValue != "" {
				os.Setenv(tt.envVar, tt.envValue)
				defer os.Unsetenv(tt.envVar)
			}
			if tt.setupFile != nil {
				tt.filePath = tt.setupFile()
			}

			// Execute
			err := ValidateSecret(tt.value, tt.envVar, tt.filePath)

			// Check error
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateSecret() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			// Check error message if expected
			if err != nil && tt.wantMsg != "" && !strings.Contains(err.Error(), tt.wantMsg) {
				t.Errorf("ValidateSecret() error = %v, want error containing %v", err, tt.wantMsg)
			}
		})
	}
}

func TestValidateSecretWithFallback(t *testing.T) {
	tests := []struct {
		name          string
		value         string
		envVar        string
		filePath      string
		fallbackEnv   string
		envValue      string
		fallbackValue string
		setupFile     func() string
		wantErr       bool
		wantMsg       string
	}{
		{
			name:    "direct value is valid",
			value:   "secret",
			wantErr: false,
		},
		{
			name:        "primary env var exists",
			envVar:      "TEST_SECRET",
			envValue:    "secret",
			fallbackEnv: "FALLBACK_SECRET",
			wantErr:     false,
		},
		{
			name:          "fallback env var exists",
			envVar:        "NONEXISTENT_VAR",
			fallbackEnv:   "FALLBACK_SECRET",
			fallbackValue: "fallback",
			wantErr:       false,
		},
		{
			name:        "neither env var exists",
			envVar:      "NONEXISTENT_VAR",
			fallbackEnv: "ALSO_NONEXISTENT",
			wantErr:     true,
			wantMsg:     "environment variable NONEXISTENT_VAR is not set",
		},
		{
			name: "primary file exists",
			setupFile: func() string {
				tmpFile := filepath.Join(t.TempDir(), "secret.txt")
				if err := os.WriteFile(tmpFile, []byte("secret"), 0600); err != nil {
					t.Fatalf("failed to create test file: %v", err)
				}
				return tmpFile
			},
			fallbackEnv: "FALLBACK_SECRET",
			wantErr:     false,
		},
		{
			name:          "primary file missing but fallback exists",
			filePath:      "/nonexistent/file.txt",
			fallbackEnv:   "FALLBACK_SECRET",
			fallbackValue: "fallback",
			wantErr:       false,
		},
		{
			name:        "no source configured",
			fallbackEnv: "NONEXISTENT_FALLBACK",
			wantErr:     true,
			wantMsg:     "no secret source configured",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup
			if tt.envValue != "" {
				os.Setenv(tt.envVar, tt.envValue)
				defer os.Unsetenv(tt.envVar)
			}
			if tt.fallbackValue != "" {
				os.Setenv(tt.fallbackEnv, tt.fallbackValue)
				defer os.Unsetenv(tt.fallbackEnv)
			}
			if tt.setupFile != nil {
				tt.filePath = tt.setupFile()
			}

			// Execute
			err := ValidateSecretWithFallback(tt.value, tt.envVar, tt.filePath, tt.fallbackEnv)

			// Check error
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateSecretWithFallback() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			// Check error message if expected
			if err != nil && tt.wantMsg != "" && !strings.Contains(err.Error(), tt.wantMsg) {
				t.Errorf("ValidateSecretWithFallback() error = %v, want error containing %v", err, tt.wantMsg)
			}
		})
	}
}

// TestDirectoryTraversalVulnerability tests for directory traversal security issues
func TestDirectoryTraversalVulnerability(t *testing.T) {
	// Create a test file in a temporary directory
	tmpDir := t.TempDir()
	secretFile := filepath.Join(tmpDir, "secret.txt")
	secretContent := "test-secret-value"
	require.NoError(t, os.WriteFile(secretFile, []byte(secretContent), 0600))

	// Create a sensitive file that should not be accessible
	sensitiveFile := filepath.Join(tmpDir, "sensitive.txt")
	sensitiveContent := "sensitive-data"
	require.NoError(t, os.WriteFile(sensitiveFile, []byte(sensitiveContent), 0600))

	tests := []struct {
		name        string
		filePath    string
		shouldFail  bool
		expectError string
	}{
		{
			name:        "directory traversal with ..",
			filePath:    tmpDir + "/subdir/../sensitive.txt",
			shouldFail:  true,
			expectError: "invalid file path",
		},
		{
			name:        "directory traversal with multiple ..",
			filePath:    tmpDir + "/a/b/../../sensitive.txt",
			shouldFail:  true,
			expectError: "invalid file path",
		},
		{
			name:        "path with .. in the middle",
			filePath:    tmpDir + "/subdir/../sensitive.txt",
			shouldFail:  true,
			expectError: "invalid file path",
		},
		{
			name:       "valid absolute path",
			filePath:   secretFile,
			shouldFail: false,
		},
		{
			name:        "relative path",
			filePath:    "secret.txt",
			shouldFail:  true,
			expectError: "must be absolute",
		},
		{
			name:       "empty path",
			filePath:   "",
			shouldFail: false, // Empty path is valid - it means no file configured
		},
		{
			name:        "path with null bytes",
			filePath:    "/tmp/secret\x00.txt",
			shouldFail:  true,
			expectError: "invalid file path",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ResolveSecret("", "", tt.filePath)

			if tt.shouldFail {
				if err == nil {
					t.Errorf("Expected error for path %q but got none, result: %q", tt.filePath, result)
				} else if tt.expectError != "" && !strings.Contains(err.Error(), tt.expectError) {
					t.Errorf("Expected error containing %q but got %q", tt.expectError, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error for path %q: %v", tt.filePath, err)
				}
			}
		})
	}
}

// TestValidateFilePath tests the file path validation function
func TestValidateFilePath(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		isValid bool
	}{
		// Valid paths
		{"/absolute/path/to/file", "/absolute/path/to/file", true},
		{"/var/lib/tsbridge/secret.txt", "/var/lib/tsbridge/secret.txt", true},
		{"/etc/tsbridge/config.toml", "/etc/tsbridge/config.toml", true},

		// Invalid paths
		{"relative path", "relative/path", false},
		{"path with ..", "/path/../etc/passwd", false},
		{"path with .. at start", "../etc/passwd", false},
		{"path with .. in middle", "/var/lib/../../../etc/passwd", false},
		{"empty path", "", false},
		{"path with null byte", "/path/to\x00/file", false},
		{"just ..", "..", false},
		{"just .", ".", false},
		{"path ending with ..", "/path/to/..", false},
		{"complex traversal", "/var/lib/tsbridge/../../../../../../etc/passwd", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateFilePath(tt.path)
			if tt.isValid && err != nil {
				t.Errorf("Expected valid path %q but got error: %v", tt.path, err)
			} else if !tt.isValid && err == nil {
				t.Errorf("Expected invalid path %q but no error returned", tt.path)
			}
		})
	}
}
