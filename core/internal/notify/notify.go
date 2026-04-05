package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/hatolife/VRCLogHook/core/internal/config"
	"github.com/hatolife/VRCLogHook/core/internal/monitor"
)

type Dispatcher struct {
	httpClient *http.Client
}

type Payload struct {
	Rule   string `json:"rule"`
	File   string `json:"file"`
	Line   string `json:"line"`
	At     string `json:"at"`
	DryRun bool   `json:"dry_run"`
}

func New() *Dispatcher {
	return &Dispatcher{httpClient: &http.Client{Timeout: 10 * time.Second}}
}

func (d *Dispatcher) Send(ctx context.Context, cfg config.Config, ruleName string, ev monitor.Event) error {
	p := Payload{
		Rule:   ruleName,
		File:   ev.File,
		Line:   ev.Line,
		At:     ev.At.Format(time.RFC3339),
		DryRun: cfg.Runtime.DryRun,
	}
	if err := appendLocal(cfg.Notify.Local.Path, p); err != nil {
		return err
	}
	if cfg.Runtime.DryRun || !cfg.Notify.Discord.Enabled {
		return nil
	}
	if cfg.Notify.Discord.WebhookURL == "" {
		return errors.New("discord webhook is empty")
	}

	msg := fmt.Sprintf("[%s] %s", ruleName, ev.Line)
	runes := []rune(msg)
	if len(runes) > cfg.Notify.Discord.MaxContentRune {
		msg = string(runes[:cfg.Notify.Discord.MaxContentRune])
	}
	body, _ := json.Marshal(map[string]string{
		"username": cfg.Notify.Discord.Username,
		"content":  msg,
	})

	attempts := cfg.Notify.Retry.MaxAttempts
	if attempts < 1 {
		attempts = 1
	}
	backoff := time.Duration(cfg.Notify.Retry.InitialBackoffMs) * time.Millisecond
	maxBackoff := time.Duration(cfg.Notify.Retry.MaxBackoffMs) * time.Millisecond
	var lastErr error

	for i := 0; i < attempts; i++ {
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, cfg.Notify.Discord.WebhookURL, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")

		resp, err := d.httpClient.Do(req)
		if err == nil && resp != nil {
			resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return nil
			}
			err = fmt.Errorf("discord response status: %d", resp.StatusCode)
		}
		lastErr = err

		if i < attempts-1 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		}
	}
	return lastErr
}

func appendLocal(path string, p Payload) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()

	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	_, err = f.Write(append(b, '\n'))
	return err
}
