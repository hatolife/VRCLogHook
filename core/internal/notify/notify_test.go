package notify

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
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
	d.lookPath = func(string) (string, error) { return "/usr/bin/curl", nil }
	d.runCurl = func(_ context.Context, args []string) (string, error) {
		hit++
		i := indexOf(args, "-d")
		if i < 0 || i+1 >= len(args) {
			t.Fatal("curl args missing -d payload")
		}
		var body map[string]string
		if err := json.Unmarshal([]byte(args[i+1]), &body); err != nil {
			t.Fatal(err)
		}
		content := body["content"]
		if len([]rune(content)) > 20 {
			t.Fatalf("content should be trimmed, got len=%d", len([]rune(content)))
		}
		if hit < 3 {
			return "failed", errors.New("exit status 22")
		}
		return "", nil
	}

	cfg := config.Defaults()
	cfg.Notify.Local.Path = filepath.Join(t.TempDir(), "events.log")
	cfg.Notify.Discord.Enabled = true
	cfg.Notify.Discord.WebhookURL = "http://unit-test.local/webhook"
	cfg.Notify.Discord.MaxContentRune = 20
	cfg.Notify.Retry.MaxAttempts = 3
	cfg.Notify.Retry.InitialBackoffMs = 1
	cfg.Notify.Retry.MaxBackoffMs = 2

	err := d.SendWithRule(context.Background(), cfg, config.Rule{
		Name:            "rule",
		MessageTemplate: "custom [{rule}] {line}",
	}, monitor.Event{
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

func TestRenderMessageTemplate(t *testing.T) {
	ev := monitor.Event{
		File: "x.log",
		Line: "joined",
		At:   time.Date(2026, 4, 6, 0, 0, 0, 0, time.UTC),
	}
	msg := renderMessage(config.Rule{
		Name:            "player-joined",
		MessageTemplate: "[{rule}] {line} ({file}) {at}",
	}, ev)
	if !strings.Contains(msg, "[player-joined] joined (x.log)") {
		t.Fatalf("unexpected rendered message: %q", msg)
	}
}

func TestSendDiscordFailsAfterMaxAttempts(t *testing.T) {
	d := New()
	d.lookPath = func(string) (string, error) { return "/usr/bin/curl", nil }
	d.runCurl = func(_ context.Context, _ []string) (string, error) {
		return "bad gateway", errors.New("exit status 22")
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

func TestSendCurlNotFoundGuidance(t *testing.T) {
	d := New()
	d.lookPath = func(string) (string, error) { return "", errors.New("not found") }
	cfg := config.Defaults()
	cfg.Notify.Local.Path = filepath.Join(t.TempDir(), "events.log")
	cfg.Notify.Discord.Enabled = true
	cfg.Notify.Discord.WebhookURL = "https://discord.example/webhook/test"
	err := d.Send(context.Background(), cfg, "rule", monitor.Event{File: "f", Line: "x", At: time.Now()})
	if err == nil {
		t.Fatal("expected curl not found error")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "install curl") {
		t.Fatalf("expected install guidance, got: %v", err)
	}
}

func indexOf(ss []string, needle string) int {
	for i, s := range ss {
		if s == needle {
			return i
		}
	}
	return -1
}

func TestCurlCommandName(t *testing.T) {
	name := curlCommandName()
	if runtime.GOOS == "windows" && name != "curl.exe" {
		t.Fatalf("expected curl.exe on windows, got %s", name)
	}
	if runtime.GOOS != "windows" && name != "curl" {
		t.Fatalf("expected curl on non-windows, got %s", name)
	}
}
