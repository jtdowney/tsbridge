package tsnet

import (
	"context"
	"fmt"
	"log/slog"

	"tailscale.com/types/logger"
)

// slogAdapter converts tsnet's printf-style logging to structured slog logging.
// This is used for backend/debugging logs (tsnet.Server.Logf).
// All TSNet internal logs are treated as debug level to reduce log chattiness.
func slogAdapter(serviceName string) logger.Logf {
	return createAdapter(serviceName, "tsnet", slog.LevelDebug)
}

// userSlogAdapter converts tsnet's printf-style user-facing logs to structured slog logging.
// This is used for user-facing logs like AuthURL (tsnet.Server.UserLogf).
// All user-facing logs are treated as info level.
func userSlogAdapter(serviceName string) logger.Logf {
	return createAdapter(serviceName, "tsnet-user", slog.LevelInfo)
}

// createAdapter creates a logger adapter with the specified service name, component, and log level.
func createAdapter(serviceName, component string, level slog.Level) logger.Logf {
	return func(format string, args ...any) {
		// Simply format the message using standard printf formatting
		msg := fmt.Sprintf(format, args...)

		// Log with service and component context
		slog.Log(context.TODO(), level, msg,
			slog.String("service", serviceName),
			slog.String("component", component),
		)
	}
}
