package config

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func TestLoadHJSONLike(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.hjson")
	raw := `{
  // comment
  "version": "1",
  "token": "tok-test",
  "monitor": {
    "poll_interval_sec": 10,
    "log_dir": "/tmp",
    "file_glob": "output_log_*.txt",
    "check_existing_on_first_run": true,
  },
  "state": {"path": "/tmp/state.json", "save_interval_sec": 10},
  "notify": {
    "discord": {"enabled": false, "webhook_url": "https://discord.example/webhook/abc", "username": "x", "max_content_rune": 1000},
    "local": {"path": "/tmp/events.log"},
    "retry": {"max_attempts": 2, "initial_backoff_ms": 100, "max_backoff_ms": 500}
  },
  "match": {"rules": [{"name":"r1","contains":"Joined","regex":"","case_sensitive":false}], "dedupe_window_sec": 0},
  "hooks": {"enabled": false, "unsafe_consent": false, "max_concurrency": 1, "timeout_sec": 5, "commands": []},
  "runtime": {"dry_run": true, "hot_reload": true, "config_reload_sec": 2},
  "observability": {"self_log_path": "/tmp/self.log", "status_log_sec": 10},
}`
	if err := os.WriteFile(p, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Monitor.PollIntervalSec != 10 {
		t.Fatalf("unexpected poll interval: %d", cfg.Monitor.PollIntervalSec)
	}
	if cfg.Notify.Discord.WebhookURL == "" || !strings.HasPrefix(cfg.Notify.Discord.WebhookURL, "https://") {
		t.Fatalf("webhook URL parsing failed: %q", cfg.Notify.Discord.WebhookURL)
	}
}

func TestDefaultsValidate(t *testing.T) {
	cfg := Defaults()
	if err := Validate(cfg); err != nil {
		t.Fatal(err)
	}
}

func TestLoadRejectsUnknownKey(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.json")
	raw := `{
  "version":"1",
  "token":"tok-test",
  "monitor":{"poll_interval_sec":10,"log_dir":"/tmp","file_glob":"output_log_*.txt","check_existing_on_first_run":true},
  "state":{"path":"/tmp/state.json","save_interval_sec":10},
  "notify":{"discord":{"enabled":false,"webhook_url":"","username":"x","max_content_rune":1000},"local":{"path":"/tmp/events.log"},"retry":{"max_attempts":2,"initial_backoff_ms":100,"max_backoff_ms":500}},
  "match":{"rules":[{"name":"r1","contains":"Joined","regex":"","case_sensitive":false}],"dedupe_window_sec":0},
  "hooks":{"enabled":false,"unsafe_consent":false,"max_concurrency":1,"timeout_sec":5,"commands":[]},
  "runtime":{"dry_run":true,"hot_reload":true,"config_reload_sec":2},
  "observability":{"self_log_path":"/tmp/self.log","status_log_sec":10},
  "unknown_key": true
}`
	if err := os.WriteFile(p, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p); err == nil {
		t.Fatal("expected unknown key error")
	}
}

func TestLoadRejectsOpenPermission(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission check is disabled on windows")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "config.json")
	cfg := Defaults()
	cfg.Monitor.LogDir = "/tmp"
	cfg.State.Path = filepath.Join(dir, "state.json")
	cfg.Notify.Local.Path = filepath.Join(dir, "events.log")
	cfg.Observability.SelfLogPath = filepath.Join(dir, "self.log")
	if err := Save(p, cfg); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() == 0o777 {
		t.Skip("filesystem does not enforce POSIX permission bits")
	}
	if _, err := Load(p); err == nil {
		t.Fatal("expected permission error")
	}
}

func TestValidateBoundary(t *testing.T) {
	cfg := Defaults()
	cfg.Monitor.PollIntervalSec = 0
	if err := Validate(cfg); err == nil {
		t.Fatal("expected poll interval validation error")
	}
	cfg = Defaults()
	cfg.Notify.Discord.MaxContentRune = 2000
	if err := Validate(cfg); err == nil {
		t.Fatal("expected max_content_rune validation error")
	}
	cfg = Defaults()
	cfg.Match.Rules[0].Regex = "["
	if err := Validate(cfg); err == nil {
		t.Fatal("expected invalid regex validation error")
	}
}

func TestMaskedWebhookURL(t *testing.T) {
	got := MaskedWebhookURL("https://discord.com/api/webhooks/abc/defghijk")
	if got == "" || strings.Contains(got, "defghijk") {
		t.Fatalf("webhook should be masked: %q", got)
	}
	if MaskedWebhookURL("") != "" {
		t.Fatal("empty webhook should stay empty")
	}
}

func TestRandomTokenQuality(t *testing.T) {
	cfg := Defaults()
	if !regexp.MustCompile(`^tok-[a-f0-9]{32}$`).MatchString(cfg.Token) {
		t.Fatalf("unexpected token format: %q", cfg.Token)
	}
}

func TestMaskedToken(t *testing.T) {
	got := MaskedToken("tok-1234567890abcdef1234567890abcdef")
	if got == "" || strings.Contains(got, "abcdef1234") {
		t.Fatalf("token should be masked: %q", got)
	}
	if MaskedToken("") != "" {
		t.Fatal("empty token should stay empty")
	}
}
