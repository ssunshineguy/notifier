package logging

import "log/slog"

// Redacted replaces a sensitive log field's value while retaining its name.
func Redacted(key string) slog.Attr {
	return slog.String(key, "[redacted]")
}
