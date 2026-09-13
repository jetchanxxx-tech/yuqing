package observ

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"
)

func TestNewLogger_writesJSON(t *testing.T) {
	var buf bytes.Buffer
	logger := New(&buf, LevelInfo)
	logger.Info("hello", slog.String("key", "val"))

	var m map[string]any
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
		t.Fatalf("logger output is not valid JSON: %v\n%s", err, buf.String())
	}
	if m["msg"] != "hello" {
		t.Errorf("msg = %q, want hello", m["msg"])
	}
	if m["key"] != "val" {
		t.Errorf("key = %q, want val", m["key"])
	}
	if m["level"] != "INFO" {
		t.Errorf("level = %q, want INFO", m["level"])
	}
}

func TestNewLogger_respectsLevel(t *testing.T) {
	var buf bytes.Buffer
	logger := New(&buf, LevelWarn)
	logger.Debug("debug msg")
	logger.Info("info msg")
	logger.Warn("warn msg")

	output := buf.String()
	if output == "" {
		t.Fatal("expected warn message but buffer is empty")
	}
	// Debug and Info should be filtered at WARN level.
	if bytes.Contains([]byte(output), []byte("debug msg")) {
		t.Error("DEBUG message leaked at WARN level")
	}
	if bytes.Contains([]byte(output), []byte("info msg")) {
		t.Error("INFO message leaked at WARN level")
	}
}

func TestWithRequestID(t *testing.T) {
	var buf bytes.Buffer
	logger := New(&buf, LevelInfo)
	ctx := context.Background()
	ctx = WithRequestID(ctx, "req-abc")
	logger.InfoContext(ctx, "processing")

	var m map[string]any
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if m[RequestIDKey] != "req-abc" {
		t.Errorf("request_id = %q, want req-abc", m[RequestIDKey])
	}
}

func TestRequestIDFromContext(t *testing.T) {
	ctx := context.Background()
	if id := RequestIDFromContext(ctx); id != "" {
		t.Errorf("empty context should return empty, got %q", id)
	}
	ctx = WithRequestID(ctx, "xyz")
	if id := RequestIDFromContext(ctx); id != "xyz" {
		t.Errorf("got %q, want xyz", id)
	}
}
