//go:build !windows

package main

import (
	"bytes"
	"context"
	"fmt"
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
	cfgPath := filepath.Join(dir, "config.json")
	if err := config.Save(cfgPath, cfg); err != nil {
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
	srv := ipc.NewServer(socket, cfg.Token, ipc.Handlers{
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
