//go:build !windows

package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hatolife/VRCLogHook/core/internal/config"
	"github.com/hatolife/VRCLogHook/core/internal/ipc"
)

func TestRunPrintConfigMasksWebhook(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Defaults()
	cfg.Notify.Discord.WebhookURL = "https://discord.example/webhook/secret-token"
	cfgPath := filepath.Join(dir, "config.json")
	if err := config.Save(cfgPath, cfg); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	code := run([]string{"--config", cfgPath, "--print-config"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("unexpected exit code=%d err=%s", code, errOut.String())
	}
	if strings.Contains(out.String(), "secret-token") {
		t.Fatal("webhook secret must be masked")
	}
	if strings.Contains(out.String(), cfg.Token) {
		t.Fatal("token must be masked")
	}
}

func TestRunIPCStatusReloadStop(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Defaults()
	cfg.Token = "tok-test-cli"
	runtimeToken := "tok-runtime-cli"
	cfgPath := filepath.Join(dir, "config.json")
	if err := config.Save(cfgPath, cfg); err != nil {
		t.Fatal(err)
	}
	if err := config.WriteRuntimeToken(cfgPath, runtimeToken); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join("/tmp", fmt.Sprintf("vrc-loghook-cli-test-%d.sock", time.Now().UnixNano()))
	_ = os.Remove(socket)
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Skipf("unix socket unavailable in this environment: %v", err)
	}
	_ = ln.Close()
	_ = os.Remove(socket)

	reloaded := false
	stopped := false
	srv := ipc.NewServer(socket, runtimeToken, ipc.Handlers{
		GetStatus: func() any { return map[string]any{"running": true} },
		GetConfig: func() any { return map[string]any{"version": "1"} },
		Reload:    func() error { reloaded = true; return nil },
		Stop:      func() { stopped = true },
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Start(ctx) }()
	time.Sleep(80 * time.Millisecond)
	defer srv.Close()

	var out, errOut bytes.Buffer
	if code := run([]string{"--config", cfgPath, "--ipc", socket, "--status"}, &out, &errOut); code != 0 {
		if strings.Contains(errOut.String(), "operation not permitted") {
			t.Skipf("ipc unavailable in this environment: %s", errOut.String())
		}
		t.Fatalf("status failed: code=%d err=%s", code, errOut.String())
	}
	out.Reset()
	errOut.Reset()
	if code := run([]string{"--config", cfgPath, "--ipc", socket, "--reload"}, &out, &errOut); code != 0 || !reloaded {
		t.Fatalf("reload failed: code=%d err=%s", code, errOut.String())
	}
	out.Reset()
	errOut.Reset()
	if code := run([]string{"--config", cfgPath, "--ipc", socket, "--stop"}, &out, &errOut); code != 0 || !stopped {
		t.Fatalf("stop failed: code=%d err=%s", code, errOut.String())
	}
}

func TestShouldAutoLaunchGUI(t *testing.T) {
	if !shouldAutoLaunchGUI(nil, false, false, false, false, false) {
		t.Fatal("expected auto GUI launch on no-arg run")
	}
	if shouldAutoLaunchGUI([]string{"--dry-run"}, false, false, false, false, false) {
		t.Fatal("did not expect auto GUI launch when args are provided")
	}
	if shouldAutoLaunchGUI(nil, true, false, false, false, false) {
		t.Fatal("did not expect auto GUI launch when --open-gui is set")
	}
}

func TestChooseGUIBinaryInDir(t *testing.T) {
	dir := t.TempDir()
	makeExec := func(name string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("x"), 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}
	makeFile := func(name string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}

	linuxGUI := makeExec("vrc-loghook-gui-linux-amd64")
	if got, ok := chooseGUIBinaryInDir(dir, "linux"); !ok || got != linuxGUI {
		t.Fatalf("linux candidate mismatch: ok=%v got=%s want=%s", ok, got, linuxGUI)
	}

	_ = os.Remove(linuxGUI)
	linuxFallback := makeExec("vrc-loghook-gui-custom")
	if got, ok := chooseGUIBinaryInDir(dir, "linux"); !ok || got != linuxFallback {
		t.Fatalf("linux fallback mismatch: ok=%v got=%s want=%s", ok, got, linuxFallback)
	}

	winGUI := makeFile("vrc-loghook-gui-windows-amd64.exe")
	if got, ok := chooseGUIBinaryInDir(dir, "windows"); !ok || got != winGUI {
		t.Fatalf("windows candidate mismatch: ok=%v got=%s want=%s", ok, got, winGUI)
	}
}

func TestRunOpenGUI(t *testing.T) {
	called := false
	old := startGUI
	startGUI = func(guiBin, configPath, ipcPath string, guiHashWarn bool, stderr io.Writer) error {
		called = true
		if guiBin != "/tmp/custom-gui" {
			t.Fatalf("unexpected gui bin: %s", guiBin)
		}
		if configPath != "/tmp/config.json" {
			t.Fatalf("unexpected config path: %s", configPath)
		}
		if ipcPath != "/tmp/test.sock" {
			t.Fatalf("unexpected ipc path: %s", ipcPath)
		}
		if !guiHashWarn {
			t.Fatal("guiHashWarn should be true by default")
		}
		if stderr == nil {
			t.Fatal("stderr should be provided")
		}
		return nil
	}
	defer func() { startGUI = old }()

	var out, errOut bytes.Buffer
	code := run([]string{
		"--open-gui",
		"--gui-bin", "/tmp/custom-gui",
		"--config", "/tmp/config.json",
		"--ipc", "/tmp/test.sock",
	}, &out, &errOut)
	if code != 0 {
		t.Fatalf("expected success, got code=%d err=%s", code, errOut.String())
	}
	if !called {
		t.Fatal("expected GUI launcher to be called")
	}
	if !strings.Contains(out.String(), "gui launched") {
		t.Fatalf("unexpected output: %q", out.String())
	}
}

func TestVerifyGUIIdentity(t *testing.T) {
	dir := t.TempDir()
	guiPath := filepath.Join(dir, "vrc-loghook-gui")
	script := "#!/bin/sh\necho vrc-loghook-gui/1\n"
	if err := os.WriteFile(guiPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := verifyGUIIdentity(guiPath); err != nil {
		t.Fatalf("identity check should pass: %v", err)
	}
}

func TestVerifyGUIIdentityRejectsUnexpectedOutput(t *testing.T) {
	dir := t.TempDir()
	guiPath := filepath.Join(dir, "vrc-loghook-gui")
	script := "#!/bin/sh\necho not-vrc\n"
	if err := os.WriteFile(guiPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := verifyGUIIdentity(guiPath); err == nil {
		t.Fatal("identity check should fail")
	}
}

func TestVerifyGUIBinaryHashMismatchWarnsButPasses(t *testing.T) {
	dir := t.TempDir()
	guiPath := filepath.Join(dir, "vrc-loghook-gui")
	script := "#!/bin/sh\necho vrc-loghook-gui/1\n"
	if err := os.WriteFile(guiPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	oldHash := expectedGUIHash
	expectedGUIHash = strings.Repeat("0", 64)
	defer func() { expectedGUIHash = oldHash }()

	var errOut bytes.Buffer
	warnText, err := verifyGUIBinary(guiPath, true, &errOut)
	if err != nil {
		t.Fatalf("verify should not fail on hash mismatch warning mode: %v", err)
	}
	if !strings.Contains(warnText, "GUI hash mismatch") {
		t.Fatalf("expected warning text, got=%q", warnText)
	}
	if !strings.Contains(errOut.String(), "GUI hash mismatch") {
		t.Fatalf("expected hash mismatch warning, got=%q", errOut.String())
	}
}
