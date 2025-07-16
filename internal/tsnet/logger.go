package tsnet

import (
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"tailscale.com/types/logger"
)

// Pre-compiled regex patterns for performance
var (
	hostnameRegex               = regexp.MustCompile(`hostname\s+%q`)
	statePathRegex              = regexp.MustCompile(`state path\s+%s`)
	varRootRegex                = regexp.MustCompile(`varRoot\s+%q`)
	errorRegex                  = regexp.MustCompile(`(.*?):\s*%v`)
	formatSpecifierCleanupRegex = regexp.MustCompile(`%[vsdqx]`)
	authURLRegex                = regexp.MustCompile(`To authenticate, visit:\s*(.+)`)
)

// slogAdapter converts tsnet's printf-style logging to structured slog logging.
// This is used for backend/debugging logs (tsnet.Server.Logf).
func slogAdapter(serviceName string) logger.Logf {
	return createAdapter(serviceName, "tsnet")
}

// userSlogAdapter converts tsnet's printf-style user-facing logs to structured slog logging.
// This is used for user-facing logs like AuthURL (tsnet.Server.UserLogf).
func userSlogAdapter(serviceName string) logger.Logf {
	return createAdapter(serviceName, "tsnet-user")
}

// createAdapter creates a logger adapter with the specified service name and component.
func createAdapter(serviceName, component string) logger.Logf {
	return func(format string, args ...any) {
		// Detect log level from message content
		level := detectLogLevel(format)

		// Parse the message to extract structured data
		msg, attrs, _ := parseMessage(format, args)

		// Create base attributes
		baseAttrs := []any{
			slog.String("service", serviceName),
			slog.String("component", component),
		}

		// Add parsed attributes
		for key, value := range attrs {
			baseAttrs = append(baseAttrs, slog.Any(key, value))
		}

		// Log with appropriate level
		switch level {
		case slog.LevelDebug:
			slog.Debug(msg, baseAttrs...)
		case slog.LevelInfo:
			slog.Info(msg, baseAttrs...)
		case slog.LevelWarn:
			slog.Warn(msg, baseAttrs...)
		case slog.LevelError:
			slog.Error(msg, baseAttrs...)
		default:
			slog.Info(msg, baseAttrs...)
		}
	}
}

// detectLogLevel determines the appropriate log level based on message content.
func detectLogLevel(format string) slog.Level {
	lowerFormat := strings.ToLower(format)

	// Error level indicators - includes AuthURL since it indicates misconfiguration
	if strings.Contains(lowerFormat, "error") ||
		strings.Contains(lowerFormat, "failed") ||
		strings.Contains(lowerFormat, "timeout") ||
		strings.Contains(lowerFormat, "panic") ||
		strings.Contains(lowerFormat, "fatal") ||
		strings.Contains(lowerFormat, "to authenticate") {
		return slog.LevelError
	}

	// Warning level indicators
	if strings.Contains(lowerFormat, "warning") ||
		strings.Contains(lowerFormat, "warn") ||
		strings.Contains(lowerFormat, "retrying") ||
		strings.Contains(lowerFormat, "retry") {
		return slog.LevelWarn
	}

	// Debug level indicators
	if strings.Contains(lowerFormat, "debug") ||
		strings.Contains(lowerFormat, "trace") {
		return slog.LevelDebug
	}

	// Default to info level
	return slog.LevelInfo
}

// parseMessage extracts structured data from tsnet log messages.
func parseMessage(format string, args []any) (msg string, attrs map[string]any, parsed bool) {
	attrs = make(map[string]any)
	msg = format
	argIndex := 0

	// Define pattern handlers using pre-compiled regexes
	type patternHandler func(currentMsg string, currentArgs []any, currentArgIndex *int, currentAttrs map[string]any) (string, bool)

	handlers := []patternHandler{
		// Hostname pattern handler
		func(currentMsg string, currentArgs []any, currentArgIndex *int, currentAttrs map[string]any) (string, bool) {
			if hostnameRegex.MatchString(currentMsg) {
				if *currentArgIndex < len(currentArgs) {
					currentAttrs["hostname"] = fmt.Sprintf("%v", currentArgs[*currentArgIndex])
					*currentArgIndex++
				}
				return hostnameRegex.ReplaceAllString(currentMsg, "hostname"), true
			}
			return currentMsg, false
		},

		// State path pattern handler
		func(currentMsg string, currentArgs []any, currentArgIndex *int, currentAttrs map[string]any) (string, bool) {
			if statePathRegex.MatchString(currentMsg) {
				if *currentArgIndex < len(currentArgs) {
					currentAttrs["state_path"] = fmt.Sprintf("%v", currentArgs[*currentArgIndex])
					*currentArgIndex++
				}
				return statePathRegex.ReplaceAllString(currentMsg, "state path"), true
			}
			return currentMsg, false
		},

		// VarRoot pattern handler
		func(currentMsg string, currentArgs []any, currentArgIndex *int, currentAttrs map[string]any) (string, bool) {
			if varRootRegex.MatchString(currentMsg) {
				if *currentArgIndex < len(currentArgs) {
					currentAttrs["var_root"] = fmt.Sprintf("%v", currentArgs[*currentArgIndex])
					*currentArgIndex++
				}
				return varRootRegex.ReplaceAllString(currentMsg, "varRoot"), true
			}
			return currentMsg, false
		},

		// Error pattern handler
		func(currentMsg string, currentArgs []any, currentArgIndex *int, currentAttrs map[string]any) (string, bool) {
			matches := errorRegex.FindStringSubmatch(currentMsg)
			if len(matches) > 0 {
				if *currentArgIndex < len(currentArgs) {
					currentAttrs["error"] = fmt.Sprintf("%v", currentArgs[*currentArgIndex])
					*currentArgIndex++
				}
				// If there's a capture group for the message part, use it
				if len(matches) > 1 {
					return matches[1], true
				}
				return errorRegex.ReplaceAllString(currentMsg, ""), true
			}
			return currentMsg, false
		},

		// AuthURL pattern handler - indicates configuration problem
		func(currentMsg string, currentArgs []any, currentArgIndex *int, currentAttrs map[string]any) (string, bool) {
			matches := authURLRegex.FindStringSubmatch(currentMsg)
			if len(matches) > 1 {
				currentAttrs["auth_url"] = matches[1]
				currentAttrs["config_issue"] = "auth_key_missing"
				return "Authentication required - check auth key configuration", true
			}
			return currentMsg, false
		},
	}

	// Apply all patterns sequentially to extract all possible structured data
	for _, handler := range handlers {
		var changed bool
		msg, changed = handler(msg, args, &argIndex, attrs)
		if changed {
			parsed = true // At least one pattern was applied
		}
	}

	// Clean up any remaining format specifiers from the message
	msg = formatSpecifierCleanupRegex.ReplaceAllString(msg, "")
	msg = strings.TrimSpace(msg)

	return msg, attrs, parsed
}
