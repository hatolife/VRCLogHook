package hook

import (
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hatolife/VRCLogHook/core/internal/config"
	"github.com/hatolife/VRCLogHook/core/internal/monitor"
)

func TestRunAsyncReportsError(t *testing.T) {
	var mu sync.Mutex
	var errs []error
	r := New(1, func(err error) {
		mu.Lock()
		defer mu.Unlock()
		errs = append(errs, err)
	})

	cfg := config.Defaults()
	cfg.Hooks.Enabled = true
	cfg.Hooks.UnsafeConsent = true
	cfg.Hooks.TimeoutSec = 1
	cfg.Hooks.Commands = []config.HookCommand{
		{Name: "bad", Enabled: true, Program: "/path/does/not/exist"},
	}
	_ = r.RunAsync(cfg, "rule", monitor.Event{File: "x", Line: "y", At: time.Now()})
	r.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(errs) == 0 {
		t.Fatal("expected hook error callback")
	}
}

func TestRunAsyncRejectsWithoutConsent(t *testing.T) {
	r := New(1, nil)
	cfg := config.Defaults()
	cfg.Hooks.Enabled = true
	cfg.Hooks.UnsafeConsent = false
	err := r.RunAsync(cfg, "rule", monitor.Event{File: "x", Line: "y", At: time.Now()})
	if err == nil {
		t.Fatal("expected consent error")
	}
}

func TestRunAsyncTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh for timeout test")
	}
	var mu sync.Mutex
	var errs []error
	r := New(1, func(err error) {
		mu.Lock()
		defer mu.Unlock()
		errs = append(errs, err)
	})
	cfg := config.Defaults()
	cfg.Hooks.Enabled = true
	cfg.Hooks.UnsafeConsent = true
	cfg.Hooks.TimeoutSec = 1
	cfg.Hooks.Commands = []config.HookCommand{
		{Name: "sleep", Enabled: true, Program: "sh", Args: []string{"-c", "sleep 2"}},
	}
	_ = r.RunAsync(cfg, "rule", monitor.Event{File: "x", Line: "y", At: time.Now()})
	r.Wait()
	mu.Lock()
	defer mu.Unlock()
	if len(errs) == 0 || !strings.Contains(errs[0].Error(), "timeout") {
		t.Fatalf("expected timeout error, got %+v", errs)
	}
}

func TestRunAsyncMaxConcurrency(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh for timing test")
	}
	r := New(1, nil)
	cfg := config.Defaults()
	cfg.Hooks.Enabled = true
	cfg.Hooks.UnsafeConsent = true
	cfg.Hooks.TimeoutSec = 5
	cfg.Hooks.Commands = []config.HookCommand{
		{Name: "sleep", Enabled: true, Program: "sh", Args: []string{"-c", "sleep 0.2"}},
	}
	start := time.Now()
	for i := 0; i < 3; i++ {
		_ = r.RunAsync(cfg, "rule", monitor.Event{File: "x", Line: "y", At: time.Now()})
	}
	r.Wait()
	elapsed := time.Since(start)
	if elapsed < 450*time.Millisecond {
		t.Fatalf("max_concurrency might be broken, elapsed=%s", elapsed)
	}
}
