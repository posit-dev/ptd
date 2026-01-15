package internal

import (
	"context"
	"io"
	"log/slog"
)

// CliOutputHandler extends slog.TextHandler to provide customized CLI output
type CliOutputHandler struct {
	handler slog.Handler
}

// NewCliOutputHandler creates a new CliOutputHandler with the given options
func NewCliOutputHandler(w io.Writer, opts *slog.HandlerOptions) *CliOutputHandler {
	if opts == nil {
		opts = &slog.HandlerOptions{}
	}

	return &CliOutputHandler{
		handler: slog.NewTextHandler(w, opts),
	}
}

// Enabled implements slog.Handler.Enabled
func (h *CliOutputHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.handler.Enabled(ctx, level)
}

// Handle implements slog.Handler.Handle
func (h *CliOutputHandler) Handle(ctx context.Context, r slog.Record) error {
	return h.handler.Handle(ctx, r)
}

// WithAttrs implements slog.Handler.WithAttrs
func (h *CliOutputHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &CliOutputHandler{
		handler: h.handler.WithAttrs(attrs),
	}
}

// WithGroup implements slog.Handler.WithGroup
func (h *CliOutputHandler) WithGroup(name string) slog.Handler {
	return &CliOutputHandler{
		handler: h.handler.WithGroup(name),
	}
}
