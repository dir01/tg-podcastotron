package telemetry

import (
	"context"
	"fmt"
	"log/slog"
)

// LogError logs an error with context and additional attributes, then returns a wrapped error.
// This is a replacement for zaperr.Wrap that works with slog.
func LogError(logger *slog.Logger, ctx context.Context, err error, msg string, attrs ...any) error {
	if err == nil {
		return nil
	}

	// Combine error attribute with any additional attributes
	allAttrs := append([]any{slog.Any("error", err)}, attrs...)
	logger.ErrorContext(ctx, msg, allAttrs...)

	return fmt.Errorf("%s: %w", msg, err)
}

// LogErrorNoWrap logs an error without wrapping it, useful when you want to log but return the original error.
func LogErrorNoWrap(logger *slog.Logger, ctx context.Context, err error, msg string, attrs ...any) {
	if err == nil {
		return
	}

	allAttrs := append([]any{slog.Any("error", err)}, attrs...)
	logger.ErrorContext(ctx, msg, allAttrs...)
}
