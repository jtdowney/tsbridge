package tailscale

import (
	"log/slog"
	"os"
	"path/filepath"
)

// hasExistingState checks if a service has existing tsnet state
func hasExistingState(stateDir string, serviceName string) bool {
	// Each service has its own subdirectory under the state directory
	serviceStateDir := filepath.Join(stateDir, serviceName)

	slog.Debug("checking for existing tsnet state",
		"service", serviceName,
		"state_dir", stateDir,
		"service_state_dir", serviceStateDir,
	)

	// Check if the directory exists
	info, err := os.Stat(serviceStateDir)
	if err != nil {
		if os.IsNotExist(err) {
			slog.Debug("state directory does not exist",
				"service", serviceName,
				"service_state_dir", serviceStateDir,
			)
		} else {
			slog.Debug("error checking state directory",
				"service", serviceName,
				"service_state_dir", serviceStateDir,
				"error", err,
			)
		}
		return false
	}

	if !info.IsDir() {
		slog.Debug("state path exists but is not a directory",
			"service", serviceName,
			"service_state_dir", serviceStateDir,
		)
		return false
	}

	slog.Debug("state directory exists",
		"service", serviceName,
		"service_state_dir", serviceStateDir,
	)

	// Check for key state files that indicate an initialized node
	// tsnet stores various state files, but the key ones are:
	// - tailscaled.state: The main state file
	// - tailscaled.log.conf: Logging configuration
	stateFile := filepath.Join(serviceStateDir, "tailscaled.state")

	slog.Debug("checking for state file",
		"service", serviceName,
		"state_file", stateFile,
	)

	if _, err := os.Stat(stateFile); err == nil {
		slog.Debug("existing state file found",
			"service", serviceName,
			"state_file", stateFile,
		)
		return true
	} else if os.IsNotExist(err) {
		slog.Debug("state file does not exist",
			"service", serviceName,
			"state_file", stateFile,
		)
	} else {
		slog.Debug("error checking state file",
			"service", serviceName,
			"state_file", stateFile,
			"error", err,
		)
	}

	return false
}
