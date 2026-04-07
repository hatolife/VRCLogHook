package notify

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
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
	cfg.Notify.Discord.MinIntervalSec = 0
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
	cfg.Notify.Discord.MinIntervalSec = 0
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

func TestSendDiscordBatchesWithinMinInterval(t *testing.T) {
	d := New()
	d.lookPath = func(string) (string, error) { return "/usr/bin/curl", nil }

	var mu sync.Mutex
	hit := 0
	lastContent := ""
	done := make(chan struct{}, 1)
	d.runCurl = func(_ context.Context, args []string) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		hit++
		i := indexOf(args, "-d")
		if i < 0 || i+1 >= len(args) {
			t.Fatal("curl args missing -d payload")
		}
		var body map[string]string
		if err := json.Unmarshal([]byte(args[i+1]), &body); err != nil {
			t.Fatal(err)
		}
		lastContent = body["content"]
		select {
		case done <- struct{}{}:
		default:
		}
		return "", nil
	}

	cfg := config.Defaults()
	cfg.Notify.Local.Path = filepath.Join(t.TempDir(), "events.log")
	cfg.Notify.Discord.Enabled = true
	cfg.Notify.Discord.WebhookURL = "http://unit-test.local/webhook"
	cfg.Notify.Discord.MinIntervalSec = 1
	cfg.Notify.Discord.MaxContentRune = 200

	ev1 := monitor.Event{File: "f", Line: "line-1", At: time.Now()}
	ev2 := monitor.Event{File: "f", Line: "line-2", At: time.Now()}
	if err := d.SendWithRule(context.Background(), cfg, config.Rule{Name: "r1", MessageTemplate: "{line}"}, ev1); err != nil {
		t.Fatal(err)
	}
	if err := d.SendWithRule(context.Background(), cfg, config.Rule{Name: "r2", MessageTemplate: "{line}"}, ev2); err != nil {
		t.Fatal(err)
	}

	select {
	case <-done:
	case <-time.After(2500 * time.Millisecond):
		t.Fatal("timed out waiting for batched send")
	}

	mu.Lock()
	defer mu.Unlock()
	if hit != 1 {
		t.Fatalf("expected 1 batched webhook call, got %d", hit)
	}
	if !strings.Contains(lastContent, "line-1") || !strings.Contains(lastContent, "line-2") {
		t.Fatalf("batched content missing lines: %q", lastContent)
	}
}

func TestSendWithRuleUsesGroupWebhookOverride(t *testing.T) {
	d := New()
	d.lookPath = func(string) (string, error) { return "/usr/bin/curl", nil }
	var usedURL string
	d.runCurl = func(_ context.Context, args []string) (string, error) {
		for _, a := range args {
			if strings.HasPrefix(a, "http://") || strings.HasPrefix(a, "https://") {
				usedURL = a
				break
			}
		}
		return "", nil
	}
	cfg := config.Defaults()
	cfg.Notify.Local.Path = filepath.Join(t.TempDir(), "events.log")
	cfg.Notify.Discord.Enabled = true
	cfg.Notify.Discord.WebhookURL = "https://default.invalid/webhook"
	cfg.Notify.Discord.GroupWebhooks = map[string]string{
		"error": "https://error.invalid/webhook",
	}
	cfg.Notify.Discord.MinIntervalSec = 0
	if err := d.SendWithRule(context.Background(), cfg, config.Rule{
		Name:            "runtime-exception",
		Group:           "error",
		MessageTemplate: "{line}",
	}, monitor.Event{File: "f", Line: "x", At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if usedURL != "https://error.invalid/webhook" {
		t.Fatalf("expected group webhook URL, got %q", usedURL)
	}
}

func TestSendWithRuleFallsBackToDefaultWebhook(t *testing.T) {
	d := New()
	d.lookPath = func(string) (string, error) { return "/usr/bin/curl", nil }
	var usedURL string
	d.runCurl = func(_ context.Context, args []string) (string, error) {
		for _, a := range args {
			if strings.HasPrefix(a, "http://") || strings.HasPrefix(a, "https://") {
				usedURL = a
				break
			}
		}
		return "", nil
	}
	cfg := config.Defaults()
	cfg.Notify.Local.Path = filepath.Join(t.TempDir(), "events.log")
	cfg.Notify.Discord.Enabled = true
	cfg.Notify.Discord.WebhookURL = "https://default.invalid/webhook"
	cfg.Notify.Discord.GroupWebhooks = map[string]string{}
	cfg.Notify.Discord.MinIntervalSec = 0
	if err := d.SendWithRule(context.Background(), cfg, config.Rule{
		Name:            "player-joined",
		Group:           "info",
		MessageTemplate: "{line}",
	}, monitor.Event{File: "f", Line: "x", At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if usedURL != "https://default.invalid/webhook" {
		t.Fatalf("expected default webhook URL, got %q", usedURL)
	}
}
