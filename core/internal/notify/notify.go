package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/hatolife/VRCLogHook/core/internal/config"
	"github.com/hatolife/VRCLogHook/core/internal/monitor"
)

var ErrCurlNotFound = errors.New("curl command was not found")

type Dispatcher struct {
	lookPath func(string) (string, error)
	runCurl  func(context.Context, []string) (string, error)
	curlCmd  string
}

type Payload struct {
	Rule   string `json:"rule"`
	File   string `json:"file"`
	Line   string `json:"line"`
	At     string `json:"at"`
	DryRun bool   `json:"dry_run"`
}

func New() *Dispatcher {
	cmd := curlCommandName()
	return &Dispatcher{
		lookPath: exec.LookPath,
		runCurl: func(ctx context.Context, args []string) (string, error) {
			c := exec.CommandContext(ctx, cmd, args...)
			b, err := c.CombinedOutput()
			return string(b), err
		},
		curlCmd: cmd,
	}
}

func (d *Dispatcher) Send(ctx context.Context, cfg config.Config, ruleName string, ev monitor.Event) error {
	return d.SendWithRule(ctx, cfg, config.Rule{Name: ruleName}, ev)
}

func (d *Dispatcher) SendWithRule(ctx context.Context, cfg config.Config, rule config.Rule, ev monitor.Event) error {
	ruleName := rule.Name
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
	if _, err := d.lookPath(d.curlCmd); err != nil {
		return fmt.Errorf("%w. %s", ErrCurlNotFound, curlInstallGuide())
	}

	msg := renderMessage(rule, ev)
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
		args := []string{
			"-sS", "-f",
			"-X", "POST", cfg.Notify.Discord.WebhookURL,
			"-H", "Content-Type: application/json",
			"-d", string(body),
			"--max-time", "10",
		}
		out, err := d.runCurl(ctx, args)
		if err == nil {
			return nil
		}
		msg := strings.TrimSpace(out)
		if msg == "" {
			lastErr = fmt.Errorf("curl failed: %w", err)
		} else {
			lastErr = fmt.Errorf("curl failed: %w: %s", err, msg)
		}

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

func renderMessage(rule config.Rule, ev monitor.Event) string {
	tmpl := strings.TrimSpace(rule.MessageTemplate)
	if tmpl == "" {
		tmpl = "[{rule}] {line}"
	}
	repl := strings.NewReplacer(
		"{rule}", rule.Name,
		"{line}", ev.Line,
		"{file}", ev.File,
		"{at}", ev.At.Format(time.RFC3339),
	)
	return repl.Replace(tmpl)
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

func curlInstallGuide() string {
	switch runtime.GOOS {
	case "windows":
		return "Install curl: winget install cURL.cURL (or choco install curl)"
	case "darwin":
		return "Install curl: brew install curl"
	default:
		return "Install curl using your package manager (e.g. apt install curl / dnf install curl / pacman -S curl)"
	}
}

func CurlPreflight(cfg config.Config) error {
	if cfg.Runtime.DryRun || !cfg.Notify.Discord.Enabled {
		return nil
	}
	if cfg.Notify.Discord.WebhookURL == "" {
		return errors.New("discord webhook is empty")
	}
	if _, err := exec.LookPath(curlCommandName()); err != nil {
		return fmt.Errorf("%w. %s", ErrCurlNotFound, curlInstallGuide())
	}
	return nil
}

func curlCommandName() string {
	if runtime.GOOS == "windows" {
		return "curl.exe"
	}
	return "curl"
}
