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

func TestDefaultsIncludeVRChatJoinLeavePatterns(t *testing.T) {
	cfg := Defaults()
	rules := cfg.Match.Rules
	if rules[0].MessageTemplate == "" || rules[1].MessageTemplate == "" {
		t.Fatal("default message_template should not be empty")
	}
	compiled, err := regexp.Compile(rules[0].Regex)
	if err != nil {
		t.Fatalf("join rule regex should compile: %v", err)
	}
	if !compiled.MatchString("2026.04.05 23:12:10 Debug      -  [Behaviour] OnPlayerEnteredRoom") {
		t.Fatal("join rule should match OnPlayerEnteredRoom")
	}
	if !compiled.MatchString("2026.04.05 23:12:10 Debug      -  [Behaviour] OnPlayerJoined") {
		t.Fatal("join rule should match OnPlayerJoined")
	}

	leftCompiled, err := regexp.Compile(rules[1].Regex)
	if err != nil {
		t.Fatalf("left rule regex should compile: %v", err)
	}
	if leftCompiled.MatchString("2026.04.05 23:12:20 Debug      -  [Behaviour] OnPlayerLeftRoom") {
		t.Fatal("left rule should not match OnPlayerLeftRoom")
	}
	if !leftCompiled.MatchString("2026.04.05 23:12:20 Debug      -  [Behaviour] OnPlayerLeft Alice (usr_xxx)") {
		t.Fatal("left rule should match OnPlayerLeft")
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

func TestSaveWritesCommentHeader(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.hjson")
	cfg := Defaults()
	cfg.Monitor.LogDir = "/tmp"
	cfg.State.Path = filepath.Join(dir, "state.json")
	cfg.Notify.Local.Path = filepath.Join(dir, "events.log")
	cfg.Observability.SelfLogPath = filepath.Join(dir, "self.log")
	if err := Save(p, cfg); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "VRC LogHook configuration") {
		t.Fatal("expected config header comments")
	}
}

func TestLoadUpgradesLegacyJoinLeaveRules(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.json")
	raw := `{
  "version": "1",
  "token": "tok-test",
  "monitor": {"poll_interval_sec": 10, "log_dir": "/tmp", "file_glob": "output_log_*.txt", "check_existing_on_first_run": true},
  "state": {"path": "/tmp/state.json", "save_interval_sec": 10},
  "notify": {
    "discord": {"enabled": false, "webhook_url": "", "username": "x", "max_content_rune": 1000},
    "local": {"path": "/tmp/events.log"},
    "retry": {"max_attempts": 2, "initial_backoff_ms": 100, "max_backoff_ms": 500}
  },
  "match": {
    "rules": [
      {"name":"player-joined","contains":"OnPlayerJoined","regex":"","case_sensitive":false},
      {"name":"player-left","contains":"OnPlayerLeft","regex":"","case_sensitive":false}
    ],
    "dedupe_window_sec": 30
  },
  "hooks": {"enabled": false, "unsafe_consent": false, "max_concurrency": 1, "timeout_sec": 5, "commands": []},
  "runtime": {"dry_run": false, "hot_reload": true, "config_reload_sec": 2},
  "observability": {"self_log_path": "/tmp/self.log", "status_log_sec": 10, "log_level": "info", "stdout": true}
}`
	if err := os.WriteFile(p, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Match.Rules[0].Regex == "" || cfg.Match.Rules[0].Contains != "" {
		t.Fatalf("join rule was not upgraded: %+v", cfg.Match.Rules[0])
	}
	if cfg.Match.Rules[1].Regex == "" || cfg.Match.Rules[1].Contains != "" {
		t.Fatalf("left rule was not upgraded: %+v", cfg.Match.Rules[1])
	}
	if cfg.Match.Rules[1].Regex != `(?i)OnPlayerLeft\s` {
		t.Fatalf("left rule regex should be upgraded to strict left line: %q", cfg.Match.Rules[1].Regex)
	}
	if cfg.Match.Rules[0].MessageTemplate == "" || cfg.Match.Rules[1].MessageTemplate == "" {
		t.Fatal("legacy rules should receive default message_template")
	}
}

func TestNormalizeLegacyWindowsLogDir(t *testing.T) {
	in := `C:\Users\user\AppData\Local\Low\VRChat\VRChat`
	got := normalizeLegacyWindowsLogDir(in)
	want := `C:\Users\user\AppData\LocalLow\VRChat\VRChat`
	if got != want {
		t.Fatalf("unexpected normalized path: got=%q want=%q", got, want)
	}

	in2 := `C:/Users/user/AppData/Local/Low/VRChat/VRChat`
	got2 := normalizeLegacyWindowsLogDir(in2)
	if got2 != want {
		t.Fatalf("slash normalized path mismatch: got=%q want=%q", got2, want)
	}
}

func TestRuntimeTokenReadWriteAndResolve(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.hjson")
	if err := os.WriteFile(cfgPath, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := RuntimeTokenPath(cfgPath); got != filepath.Join(dir, "ipc.token") {
		t.Fatalf("unexpected runtime token path: %s", got)
	}
	if err := WriteRuntimeToken(cfgPath, "tok-runtime-test"); err != nil {
		t.Fatal(err)
	}
	got, err := ReadRuntimeToken(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if got != "tok-runtime-test" {
		t.Fatalf("unexpected runtime token: %q", got)
	}
	if resolved := ResolveIPCToken(cfgPath, "tok-fallback"); resolved != "tok-runtime-test" {
		t.Fatalf("resolve should prefer runtime token, got=%q", resolved)
	}
	otherDir := filepath.Join(dir, "other")
	if err := os.MkdirAll(otherDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if resolved := ResolveIPCToken(filepath.Join(otherDir, "missing-config.hjson"), "tok-fallback"); resolved != "tok-fallback" {
		t.Fatalf("resolve should fallback when token file is missing, got=%q", resolved)
	}
}

func TestLoadOrCreateRecoversInvalidConfig(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.hjson")
	invalid := `{
  "version": "1",
  "token": "tok-test",
  "match": {
    "rules": [
      {"name":"bad","contains":"","regex":"","case_sensitive":false}
    ],
    "dedupe_window_sec": 30
  }
}`
	if err := os.WriteFile(p, []byte(invalid), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadOrCreate(p)
	if err != nil {
		t.Fatalf("LoadOrCreate should recover invalid config: %v", err)
	}
	if len(cfg.Match.Rules) == 0 {
		t.Fatal("expected default rules after recovery")
	}
	if strings.TrimSpace(cfg.Token) != "" {
		t.Fatalf("runtime token should not be persisted in regenerated config: %q", cfg.Token)
	}
	if _, err := Load(p); err != nil {
		t.Fatalf("regenerated config should be loadable: %v", err)
	}

	matches, err := filepath.Glob(p + ".broken-*")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected exactly one backup file, got %d", len(matches))
	}
	b, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"name":"bad"`) {
		t.Fatal("backup should contain original invalid config")
	}
}

func TestLoadDefaultsRuleEnabledWhenFieldMissing(t *testing.T) {
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
  "observability":{"self_log_path":"/tmp/self.log","status_log_sec":10,"log_level":"info","stdout":true}
}`
	if err := os.WriteFile(p, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Match.Rules) == 0 {
		t.Fatal("rules should not be empty")
	}
	if !cfg.Match.Rules[0].Enabled {
		t.Fatal("missing enabled field should default to true")
	}
}
