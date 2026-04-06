package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStripCommentsOutsideStrings(t *testing.T) {
	in := `{
  "url": "https://example.com/a//b#c",
  // line comment
  "v": "x", /* block */
  # hash comment
  "n": 1,
}`
	out := string(sanitizeHJSONLike([]byte(in)))
	if !strings.Contains(out, `"https://example.com/a//b#c"`) {
		t.Fatal("string content was unexpectedly modified")
	}
	if strings.Contains(out, "line comment") || strings.Contains(out, "hash comment") || strings.Contains(out, "block") {
		t.Fatal("comments should be removed")
	}
}

func TestMustInt(t *testing.T) {
	if got := mustInt("42", 7); got != 42 {
		t.Fatalf("expected 42, got %d", got)
	}
	if got := mustInt("x", 7); got != 7 {
		t.Fatalf("expected fallback 7, got %d", got)
	}
}

func TestSaveAndLoadConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	cfg := guiConfig{Token: "tok-test"}
	cfg.Monitor.PollIntervalSec = 15
	cfg.Monitor.LogDir = "/tmp"
	cfg.Monitor.FileGlob = "output_log_*.txt"
	cfg.Observability.LogLevel = "info"
	if err := saveConfig(path, cfg); err != nil {
		t.Fatal(err)
	}
	got, err := loadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Token != "tok-test" || got.Monitor.PollIntervalSec != 15 {
		t.Fatalf("unexpected loaded config: %+v", got)
	}
}

func TestSaveConfigValidation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	cfg := guiConfig{}
	cfg.Monitor.PollIntervalSec = 15
	cfg.Monitor.LogDir = "/tmp"
	cfg.Monitor.FileGlob = "output_log_*.txt"
	if err := saveConfig(path, cfg); err != nil {
		t.Fatalf("unexpected validation error without token: %v", err)
	}

	cfg.Monitor.PollIntervalSec = 0
	if err := saveConfig(path, cfg); err == nil {
		t.Fatal("expected poll interval validation error")
	}
}

func TestDefaultIPCPath(t *testing.T) {
	p := defaultIPCPath()
	if strings.TrimSpace(p) == "" {
		t.Fatal("default ipc path should not be empty")
	}
}

func TestLoadConfigNotFound(t *testing.T) {
	_, err := loadConfig(filepath.Join(t.TempDir(), "missing.json"))
	if err == nil {
		t.Fatal("expected error for missing config")
	}
	if !os.IsNotExist(err) {
		t.Fatalf("expected os.IsNotExist error, got: %v", err)
	}
}

func TestResolveIPCTokenPrefersRuntimeFile(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.hjson")
	if err := os.WriteFile(runtimeTokenPath(cfgPath), []byte("tok-runtime\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := resolveIPCToken(cfgPath, "tok-fallback")
	if got != "tok-runtime" {
		t.Fatalf("expected runtime token, got %q", got)
	}
	otherDir := filepath.Join(dir, "other")
	if err := os.MkdirAll(otherDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if got2 := resolveIPCToken(filepath.Join(otherDir, "missing.hjson"), "tok-fallback"); got2 != "tok-fallback" {
		t.Fatalf("expected fallback token, got %q", got2)
	}
}
