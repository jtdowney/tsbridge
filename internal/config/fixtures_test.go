package config

import (
	"os"
	"testing"
)

// TestFixtures tests that our test fixtures are valid
func TestFixtures(t *testing.T) {
	fixtures := GetTestFixtures()

	if len(fixtures) == 0 {
		t.Fatal("No test fixtures defined")
	}

	for _, fixture := range fixtures {
		t.Run(fixture.Name, func(t *testing.T) {
			// Validate fixture has required fields
			if fixture.Name == "" {
				t.Error("Fixture missing name")
			}
			if fixture.Description == "" {
				t.Error("Fixture missing description")
			}
			if fixture.Content == "" {
				t.Error("Fixture missing content")
			}

			// For valid fixtures, check they have expected config
			if fixture.ExpectError == "" {
				if fixture.ExpectedConfig == nil {
					t.Error("Valid fixture missing expected config")
				}
			}
		})
	}
}

// TestFixtureValidation ensures all valid fixtures produce valid configs
func TestFixtureValidation(t *testing.T) {
	fixtures := GetTestFixtures()

	for _, fixture := range fixtures {
		if fixture.ExpectError != "" {
			continue // Skip error fixtures
		}

		t.Run(fixture.Name, func(t *testing.T) {
			// Set up environment for env test
			if fixture.Name == "env_secret_resolution" {
				os.Setenv("TEST_OAUTH_SECRET", "secret-from-env")
				defer os.Unsetenv("TEST_OAUTH_SECRET")
			}
			// Parse the TOML content
			cfg, err := ParseConfigFromString(fixture.Content)
			if err != nil {
				t.Fatalf("Failed to parse valid fixture: %v", err)
			}

			// Process the config
			if err := ProcessLoadedConfig(cfg); err != nil {
				t.Fatalf("Failed to process valid fixture: %v", err)
			}

			// Verify essential fields match expected
			if fixture.ExpectedConfig != nil {
				if cfg.Tailscale.OAuthClientID != fixture.ExpectedConfig.Tailscale.OAuthClientID {
					t.Errorf("OAuth client ID mismatch: got %q, want %q",
						cfg.Tailscale.OAuthClientID,
						fixture.ExpectedConfig.Tailscale.OAuthClientID)
				}

				if len(cfg.Services) != len(fixture.ExpectedConfig.Services) {
					t.Errorf("Service count mismatch: got %d, want %d",
						len(cfg.Services),
						len(fixture.ExpectedConfig.Services))
				}
			}
		})
	}
}
