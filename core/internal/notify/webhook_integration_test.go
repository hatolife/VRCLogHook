//go:build integration

package notify

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hatolife/VRCLogHook/core/internal/config"
	"github.com/hatolife/VRCLogHook/core/internal/monitor"
)

func TestDiscordWebhookIntegration(t *testing.T) {
	if os.Getenv("GITHUB_ACTIONS") == "true" {
		t.Skip("skip integration webhook test on GitHub Actions")
	}
	webhook := os.Getenv("DISCORD_WEBHOOK_URL")
	if webhook == "" {
		t.Skip("DISCORD_WEBHOOK_URL is not set")
	}

	cfg := config.Defaults()
	cfg.Runtime.DryRun = false
	cfg.Notify.Local.Path = filepath.Join(t.TempDir(), "events.log")
	cfg.Notify.Discord.Enabled = true
	cfg.Notify.Discord.WebhookURL = webhook
	cfg.Notify.Retry.MaxAttempts = 2
	cfg.Notify.Retry.InitialBackoffMs = 200
	cfg.Notify.Retry.MaxBackoffMs = 800

	d := New()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	err := d.Send(ctx, cfg, "integration-webhook", monitor.Event{
		File: "integration.log",
		Line: "VRC LogHook integration test message",
		At:   time.Now(),
	})
	if err != nil {
		t.Fatalf("webhook integration send failed: %v", err)
	}
}
