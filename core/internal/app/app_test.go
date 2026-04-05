//go:build !windows

package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hatolife/VRCLogHook/core/internal/config"
	"github.com/hatolife/VRCLogHook/core/internal/matcher"
)

func TestServiceWritesLocalEventLog(t *testing.T) {
	dir := t.TempDir()
	logDir := filepath.Join(dir, "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(logDir, "output_log_2026-04-05_00-00-00.txt")
	if err := os.WriteFile(logPath, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := config.Defaults()
	cfg.Token = "tok-test"
	cfg.Monitor.LogDir = logDir
	cfg.Monitor.PollIntervalSec = 1
	cfg.Notify.Local.Path = filepath.Join(dir, "events.log")
	cfg.Notify.Discord.Enabled = false
	cfg.Match.Rules = []config.Rule{{Name: "joined", Contains: "Joined room", CaseSensitive: false}}
	cfg.Runtime.DryRun = true
	cfg.Observability.SelfLogPath = filepath.Join(dir, "self.log")
	cfg.State.Path = filepath.Join(dir, "state.json")

	cfgPath := filepath.Join(dir, "config.json")
	if err := config.Save(cfgPath, cfg); err != nil {
		t.Fatal(err)
	}

	svc, err := New(cfgPath, filepath.Join(dir, "sock"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- svc.Run(ctx) }()

	time.Sleep(1500 * time.Millisecond)
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("2026.04.05 19:09:42 Log      - Joined room\n")
	_ = f.Close()

	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		b, _ := os.ReadFile(cfg.Notify.Local.Path)
		if len(b) > 0 {
			cancel()
			<-done
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	cancel()
	<-done
	t.Fatal("event log was not written")
}

func TestServiceStartupExistingLineCheck(t *testing.T) {
	for _, tc := range []struct {
		name          string
		checkExisting bool
		expectEvent   bool
	}{
		{name: "enabled", checkExisting: true, expectEvent: true},
		{name: "disabled", checkExisting: false, expectEvent: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			logDir := filepath.Join(dir, "logs")
			if err := os.MkdirAll(logDir, 0o755); err != nil {
				t.Fatal(err)
			}
			logPath := filepath.Join(logDir, "output_log_2026-04-05_00-00-00.txt")
			if err := os.WriteFile(logPath, []byte("2026.04.05 19:09:42 Log      - Joined room\n"), 0o600); err != nil {
				t.Fatal(err)
			}

			cfg := config.Defaults()
			cfg.Token = "tok-test"
			cfg.Monitor.LogDir = logDir
			cfg.Monitor.PollIntervalSec = 1
			cfg.Monitor.CheckExistingOnFirstRun = tc.checkExisting
			cfg.Notify.Local.Path = filepath.Join(dir, "events.log")
			cfg.Notify.Discord.Enabled = false
			cfg.Match.Rules = []config.Rule{{Name: "joined", Contains: "Joined room", CaseSensitive: false}}
			cfg.Runtime.DryRun = true
			cfg.Observability.SelfLogPath = filepath.Join(dir, "self.log")
			cfg.State.Path = filepath.Join(dir, "state.json")

			cfgPath := filepath.Join(dir, "config.json")
			if err := config.Save(cfgPath, cfg); err != nil {
				t.Fatal(err)
			}

			svc, err := New(cfgPath, filepath.Join(dir, "sock"))
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- svc.Run(ctx) }()

			time.Sleep(2400 * time.Millisecond)
			cancel()
			<-done

			b, _ := os.ReadFile(cfg.Notify.Local.Path)
			if tc.expectEvent && len(b) == 0 {
				t.Fatal("expected startup existing line event")
			}
			if !tc.expectEvent && len(b) > 0 {
				t.Fatal("did not expect startup existing line event")
			}
		})
	}
}

func TestServiceHotReloadRule(t *testing.T) {
	dir := t.TempDir()
	logDir := filepath.Join(dir, "logs")
	_ = os.MkdirAll(logDir, 0o755)
	cfg := config.Defaults()
	cfg.Token = "tok-test"
	cfg.Monitor.LogDir = logDir
	cfg.Monitor.PollIntervalSec = 1
	cfg.Notify.Local.Path = filepath.Join(dir, "events.log")
	cfg.Notify.Discord.Enabled = false
	cfg.Match.Rules = []config.Rule{{Name: "r1", Contains: "alpha", CaseSensitive: false}}
	cfg.Runtime.DryRun = true
	cfg.Runtime.HotReload = true
	cfg.Runtime.ConfigReloadSec = 1
	cfg.Observability.SelfLogPath = filepath.Join(dir, "self.log")
	cfg.State.Path = filepath.Join(dir, "state.json")

	cfgPath := filepath.Join(dir, "config.json")
	if err := config.Save(cfgPath, cfg); err != nil {
		t.Fatal(err)
	}

	svc, err := New(cfgPath, filepath.Join(dir, "sock"))
	if err != nil {
		t.Fatal(err)
	}

	cfg.Match.Rules = []config.Rule{{Name: "r2", Contains: "beta", CaseSensitive: false}}
	if err := config.Save(cfgPath, cfg); err != nil {
		t.Fatal(err)
	}
	if err := svc.reload(); err != nil {
		t.Fatal(err)
	}

	line := "2026.04.05 19:09:44 Log      - beta event after reload"
	got := matcher.MatchLine(line, svc.rules)
	if len(got) != 1 || got[0].Name != "r2" {
		t.Fatalf("reload did not apply new rules: %+v", got)
	}
}

func TestServiceSetDryRun(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Defaults()
	cfg.Runtime.DryRun = false
	cfg.Monitor.LogDir = dir
	cfg.Notify.Local.Path = filepath.Join(dir, "events.log")
	cfg.Observability.SelfLogPath = filepath.Join(dir, "self.log")
	cfg.State.Path = filepath.Join(dir, "state.json")
	cfgPath := filepath.Join(dir, "config.json")
	if err := config.Save(cfgPath, cfg); err != nil {
		t.Fatal(err)
	}
	svc, err := New(cfgPath, filepath.Join(dir, "sock"))
	if err != nil {
		t.Fatal(err)
	}
	svc.SetDryRun(true)
	svc.mu.RLock()
	defer svc.mu.RUnlock()
	if !svc.cfg.Runtime.DryRun {
		t.Fatal("dry-run flag should be true after SetDryRun(true)")
	}
}

func TestServicePollNotStarvedByFrequentReload(t *testing.T) {
	dir := t.TempDir()
	logDir := filepath.Join(dir, "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(logDir, "output_log_2026-04-06_00-00-00.txt")
	if err := os.WriteFile(logPath, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := config.Defaults()
	cfg.Token = "tok-test"
	cfg.Monitor.LogDir = logDir
	cfg.Monitor.PollIntervalSec = 3
	cfg.Notify.Local.Path = filepath.Join(dir, "events.log")
	cfg.Notify.Discord.Enabled = false
	cfg.Match.Rules = []config.Rule{{Name: "joined", Contains: "OnPlayerEnteredRoom", CaseSensitive: false}}
	cfg.Runtime.DryRun = true
	cfg.Runtime.HotReload = true
	cfg.Runtime.ConfigReloadSec = 1
	cfg.Observability.SelfLogPath = filepath.Join(dir, "self.log")
	cfg.State.Path = filepath.Join(dir, "state.json")

	cfgPath := filepath.Join(dir, "config.json")
	if err := config.Save(cfgPath, cfg); err != nil {
		t.Fatal(err)
	}

	svc, err := New(cfgPath, filepath.Join(dir, "sock"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- svc.Run(ctx) }()

	time.Sleep(1200 * time.Millisecond)
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("2026.04.06 00:00:10 Debug      -  [Behaviour] OnPlayerEnteredRoom\n"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		b, _ := os.ReadFile(cfg.Notify.Local.Path)
		if len(b) > 0 {
			cancel()
			<-done
			return
		}
		time.Sleep(200 * time.Millisecond)
	}

	cancel()
	<-done
	t.Fatal("poll appears starved by frequent reload; event log remained empty")
}

func TestPreviewLine(t *testing.T) {
	got := previewLine("  abcdef  ", 5)
	if got != "ab..." {
		t.Fatalf("unexpected preview: %q", got)
	}

	short := previewLine("  abc  ", 10)
	if short != "abc" {
		t.Fatalf("unexpected short preview: %q", short)
	}

	if previewLine("abc", 0) != "" {
		t.Fatal("maxRune<=0 should return empty string")
	}

	long := previewLine(strings.Repeat("x", 200), 180)
	if len([]rune(long)) != 180 {
		t.Fatalf("unexpected preview length: %d", len([]rune(long)))
	}
}

func TestWindowsLogDirFallback(t *testing.T) {
	got, ok := windowsLogDirFallback(`C:\Users\user\AppData\Local\Low\VRChat\VRChat`)
	if !ok {
		t.Fatal("expected fallback for Local\\Low path")
	}
	want := `C:\Users\user\AppData\LocalLow\VRChat\VRChat`
	if got != want {
		t.Fatalf("unexpected fallback path: got=%q want=%q", got, want)
	}

	got2, ok2 := windowsLogDirFallback(`C:\Users\user\AppData\LocalLow\VRChat\VRChat`)
	if !ok2 {
		t.Fatal("expected reverse fallback for LocalLow path")
	}
	want2 := `C:\Users\user\AppData\Local\Low\VRChat\VRChat`
	if got2 != want2 {
		t.Fatalf("unexpected reverse fallback path: got=%q want=%q", got2, want2)
	}
}

func TestResolveMonitorLogDir(t *testing.T) {
	exists := func(path string) bool {
		return path == `C:\Users\user\AppData\LocalLow\VRChat\VRChat`
	}
	got, changed := resolveMonitorLogDir("windows", `C:\Users\user\AppData\Local\Low\VRChat\VRChat`, exists)
	if !changed {
		t.Fatal("expected resolved log dir to change")
	}
	if got != `C:\Users\user\AppData\LocalLow\VRChat\VRChat` {
		t.Fatalf("unexpected resolved log dir: %q", got)
	}
}
