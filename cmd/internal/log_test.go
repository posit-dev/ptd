package internal

import (
	"bytes"
	"context"
	"log/slog"
	"testing"
	"time"
)

func TestCliOutputHandler(t *testing.T) {
	// Create a buffer to capture the handler's output
	var buf bytes.Buffer

	// Create handler with debug level
	opts := &slog.HandlerOptions{Level: slog.LevelDebug}
	handler := NewCliOutputHandler(&buf, opts)

	// Test the Enabled method
	ctx := context.Background()
	if !handler.Enabled(ctx, slog.LevelInfo) {
		t.Error("Expected handler to be enabled for info level")
	}
	if !handler.Enabled(ctx, slog.LevelDebug) {
		t.Error("Expected handler to be enabled for debug level")
	}
	if handler.Enabled(ctx, slog.LevelDebug-1) {
		t.Error("Expected handler to be disabled for levels below debug")
	}

	// Test the Handle method
	record := slog.Record{
		Time:    time.Now(),
		Message: "test message",
		Level:   slog.LevelInfo,
	}

	err := handler.Handle(ctx, record)
	if err != nil {
		t.Errorf("Handler.Handle returned error: %v", err)
	}

	output := buf.String()
	if output == "" {
		t.Error("Expected handler to write output to the buffer")
	}
	if !bytes.Contains(buf.Bytes(), []byte("test message")) {
		t.Errorf("Expected output to contain 'test message', got '%s'", output)
	}
}

func TestNewCliOutputHandlerNilOpts(t *testing.T) {
	// Test with nil options
	var buf bytes.Buffer
	handler := NewCliOutputHandler(&buf, nil)

	// Should still create a valid handler
	if handler == nil {
		t.Fatal("Expected handler to be created with nil options")
	}

	// Test default behavior
	ctx := context.Background()
	if !handler.Enabled(ctx, slog.LevelInfo) {
		t.Error("Expected default handler to be enabled for info level")
	}
}
