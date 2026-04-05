package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/hatolife/VRCLogHook/core/internal/app"
	"github.com/hatolife/VRCLogHook/core/internal/config"
	"github.com/hatolife/VRCLogHook/core/internal/ipc"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("vrc-loghook", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", config.DefaultPath(), "config path (json/hjson)")
	ipcPath := fs.String("ipc", ipc.DefaultPath(), "ipc socket/pipe path")
	dryRun := fs.Bool("dry-run", false, "force dry-run mode")
	statusOnly := fs.Bool("status", false, "query daemon status over IPC")
	reloadOnly := fs.Bool("reload", false, "reload config over IPC")
	stopOnly := fs.Bool("stop", false, "stop daemon over IPC")
	printConfig := fs.Bool("print-config", false, "print masked config and exit")
	openGUI := fs.Bool("open-gui", false, "launch vrc-loghook-gui and exit")
	guiBin := fs.String("gui-bin", "", "path to vrc-loghook-gui binary (optional)")
	if err := fs.Parse(args); err != nil {
		return fail(stderr, err)
	}

	if *openGUI {
		if err := startGUI(*guiBin, *configPath, *ipcPath); err != nil {
			return fail(stderr, err)
		}
		_, _ = fmt.Fprintln(stdout, "gui launched")
		return 0
	}

	if *statusOnly || *reloadOnly || *stopOnly {
		cfg, err := config.LoadOrCreate(*configPath)
		if err != nil {
			return fail(stderr, err)
		}
		method := "status"
		if *reloadOnly {
			method = "config.reload"
		}
		if *stopOnly {
			method = "stop"
		}
		resp, err := ipc.Call(*ipcPath, ipc.Request{Token: cfg.Token, Method: method})
		if err != nil {
			return fail(stderr, err)
		}
		out, _ := json.MarshalIndent(resp, "", "  ")
		_, _ = fmt.Fprintln(stdout, string(out))
		if !resp.OK {
			return 1
		}
		return 0
	}

	if *printConfig {
		cfg, loadErr := config.LoadOrCreate(*configPath)
		if loadErr != nil {
			return fail(stderr, loadErr)
		}
		cfg.Token = config.MaskedToken(cfg.Token)
		cfg.Notify.Discord.WebhookURL = config.MaskedWebhookURL(cfg.Notify.Discord.WebhookURL)
		out, _ := json.MarshalIndent(cfg, "", "  ")
		_, _ = fmt.Fprintln(stdout, string(out))
		return 0
	}

	svc, err := app.New(*configPath, *ipcPath)
	if err != nil {
		return fail(stderr, err)
	}
	if *dryRun {
		svc.SetDryRun(true)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := svc.Run(ctx); err != nil {
		return fail(stderr, err)
	}
	return 0
}

func fail(stderr io.Writer, err error) int {
	_, _ = fmt.Fprintln(stderr, "error:", err)
	return 1
}

var startGUI = func(guiBin, configPath, ipcPath string) error {
	bin := guiBin
	if strings.TrimSpace(bin) == "" {
		bin = defaultGUIBinaryName()
	}
	cmd := exec.Command(bin, "--config", configPath, "--ipc", ipcPath)
	applyGUIProcessAttrs(cmd)
	return cmd.Start()
}

func defaultGUIBinaryName() string {
	if runtime.GOOS == "windows" {
		return "vrc-loghook-gui.exe"
	}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		return filepath.Join(dir, "vrc-loghook-gui")
	}
	return "vrc-loghook-gui"
}
