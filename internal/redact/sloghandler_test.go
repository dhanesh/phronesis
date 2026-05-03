package redact

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

func TestSlogHandlerRedactsMessageAndAttrs(t *testing.T) {
	var buf bytes.Buffer
	h := NewSlogHandler(slog.NewTextHandler(&buf, nil))
	logger := slog.New(h)

	logger.Info("user authenticated",
		slog.String("authorization", "Bearer phr_live_supersecrettoken123456"),
		slog.String("path", "/api/pages/foo"),
		slog.Int("workspace_id", 7),
	)

	out := buf.String()
	if strings.Contains(out, "phr_live_supersecrettoken123456") {
		t.Fatalf("token leaked into log output:\n%s", out)
	}
	if !strings.Contains(out, Redacted) {
		t.Fatalf("redaction marker missing:\n%s", out)
	}
	if !strings.Contains(out, "/api/pages/foo") {
		t.Fatalf("non-secret attr lost:\n%s", out)
	}
	if !strings.Contains(out, "workspace_id=7") {
		t.Fatalf("non-string attr mutated:\n%s", out)
	}
}

func TestSlogHandlerRedactsErrorAttrs(t *testing.T) {
	var buf bytes.Buffer
	h := NewSlogHandler(slog.NewTextHandler(&buf, nil))
	logger := slog.New(h)

	err := errors.New("verification failed for code=oauth_code_secret_xyz")
	logger.Error("oauth flow failed", slog.Any("err", err))

	out := buf.String()
	if strings.Contains(out, "oauth_code_secret_xyz") {
		t.Fatalf("error message leaked secret:\n%s", out)
	}
}

func TestSlogHandlerWithAttrsRedactsAtBindTime(t *testing.T) {
	var buf bytes.Buffer
	h := NewSlogHandler(slog.NewTextHandler(&buf, nil))
	logger := slog.New(h).With(
		slog.String("auth", "Authorization: Bearer phr_test_aaaaaaaaaaaaaaaa"),
		slog.String("component", "test"),
	)
	logger.Info("hello")

	out := buf.String()
	if strings.Contains(out, "phr_test_aaaaaaaaaaaaaaaa") {
		t.Fatalf("With-bound attr leaked secret:\n%s", out)
	}
	if !strings.Contains(out, "component=test") {
		t.Fatalf("non-secret With-bound attr lost:\n%s", out)
	}
}

func TestSlogHandlerRedactsMessageBody(t *testing.T) {
	var buf bytes.Buffer
	h := NewSlogHandler(slog.NewTextHandler(&buf, nil))
	logger := slog.New(h)

	logger.Info("got request with Authorization: Bearer phr_live_xxxxxxxxxxxxxxxxxxxx")

	out := buf.String()
	if strings.Contains(out, "phr_live_xxxxxxxxxxxxxxxxxxxx") {
		t.Fatalf("message text leaked secret:\n%s", out)
	}
}

func TestSlogHandlerRedactsGroupedAttrs(t *testing.T) {
	var buf bytes.Buffer
	h := NewSlogHandler(slog.NewTextHandler(&buf, nil))
	logger := slog.New(h)

	logger.Info("nested",
		slog.Group("req",
			slog.String("auth", "Authorization: Bearer phr_live_groupedsecret123456"),
			slog.String("path", "/x"),
		),
	)

	out := buf.String()
	if strings.Contains(out, "phr_live_groupedsecret123456") {
		t.Fatalf("grouped attr leaked secret:\n%s", out)
	}
	if !strings.Contains(out, "req.path=/x") {
		t.Fatalf("non-secret grouped attr lost:\n%s", out)
	}
}

func TestSlogHandlerEnabledDelegates(t *testing.T) {
	inner := slog.NewTextHandler(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelWarn})
	h := NewSlogHandler(inner)

	if h.Enabled(context.Background(), slog.LevelDebug) {
		t.Fatal("debug should be disabled (inner=Warn)")
	}
	if !h.Enabled(context.Background(), slog.LevelError) {
		t.Fatal("error should be enabled")
	}
}
