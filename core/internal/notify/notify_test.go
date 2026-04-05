package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hatolife/VRCLogHook/core/internal/config"
	"github.com/hatolife/VRCLogHook/core/internal/monitor"
)

func TestSendDryRunWritesLocalOnly(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Defaults()
	cfg.Runtime.DryRun = true
	cfg.Notify.Local.Path = filepath.Join(dir, "events.log")
	cfg.Notify.Discord.Enabled = true
	cfg.Notify.Discord.WebhookURL = "https://example.invalid/webhook"

	d := New()
	err := d.Send(context.Background(), cfg, "rule1", monitor.Event{File: "x", Line: "hello", At: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(cfg.Notify.Local.Path)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) == 0 {
		t.Fatal("expected local event log")
	}
}

func TestSendDiscordRetryAndTrim(t *testing.T) {
	hit := 0
	d := New()
	d.httpClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			hit++
			var body map[string]any
			_ = json.NewDecoder(req.Body).Decode(&body)
			content, _ := body["content"].(string)
			if len([]rune(content)) > 20 {
				t.Fatalf("content should be trimmed, got len=%d", len([]rune(content)))
			}
			if hit < 3 {
				return &http.Response{
					StatusCode: http.StatusInternalServerError,
					Body:       io.NopCloser(strings.NewReader("retry")),
					Header:     make(http.Header),
				}, nil
			}
			return &http.Response{
				StatusCode: http.StatusNoContent,
				Body:       io.NopCloser(strings.NewReader("ok")),
				Header:     make(http.Header),
			}, nil
		}),
	}

	cfg := config.Defaults()
	cfg.Notify.Local.Path = filepath.Join(t.TempDir(), "events.log")
	cfg.Notify.Discord.Enabled = true
	cfg.Notify.Discord.WebhookURL = "http://unit-test.local/webhook"
	cfg.Notify.Discord.MaxContentRune = 20
	cfg.Notify.Retry.MaxAttempts = 3
	cfg.Notify.Retry.InitialBackoffMs = 1
	cfg.Notify.Retry.MaxBackoffMs = 2

	err := d.Send(context.Background(), cfg, "rule", monitor.Event{
		File: "f",
		Line: "this message should be cut because it is too long",
		At:   time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := hit; got != 3 {
		t.Fatalf("expected 3 attempts, got %d", got)
	}
}

func TestSendDiscordFailsAfterMaxAttempts(t *testing.T) {
	d := New()
	d.httpClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusBadGateway,
				Body:       io.NopCloser(strings.NewReader("bad gateway")),
				Header:     make(http.Header),
			}, nil
		}),
	}
	cfg := config.Defaults()
	cfg.Notify.Local.Path = filepath.Join(t.TempDir(), "events.log")
	cfg.Notify.Discord.Enabled = true
	cfg.Notify.Discord.WebhookURL = "http://unit-test.local/webhook"
	cfg.Notify.Retry.MaxAttempts = 2
	cfg.Notify.Retry.InitialBackoffMs = 1
	cfg.Notify.Retry.MaxBackoffMs = 1

	err := d.Send(context.Background(), cfg, "rule", monitor.Event{File: "f", Line: "x", At: time.Now()})
	if err == nil {
		t.Fatal("expected final send error after max attempts")
	}
}

type roundTripFunc func(req *http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }
