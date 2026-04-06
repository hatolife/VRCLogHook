package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/hatolife/VRCLogHook/core/internal/app"
	"github.com/hatolife/VRCLogHook/core/internal/config"
	"github.com/hatolife/VRCLogHook/core/internal/ipc"
)

var (
	version   = "dev"
	revision  = "unknown"
	buildTime = "unknown"
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
	guiHashWarn := fs.Bool("gui-hash-warn", true, "warn when GUI hash mismatches embedded expected hash")
	guiBin := fs.String("gui-bin", "", "path to vrc-loghook-gui binary (optional)")
	if err := fs.Parse(args); err != nil {
		return fail(stderr, err)
	}

	if *openGUI {
		if err := startGUI(*guiBin, *configPath, *ipcPath, *guiHashWarn, stderr); err != nil {
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
		token := config.ResolveIPCToken(*configPath, cfg.Token)
		method := "status"
		if *reloadOnly {
			method = "config.reload"
		}
		if *stopOnly {
			method = "stop"
		}
		resp, err := ipc.Call(*ipcPath, ipc.Request{Token: token, Method: method})
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

	app.SetBuildInfo(version, revision, buildTime)
	svc, err := app.New(*configPath, *ipcPath)
	if err != nil {
		return fail(stderr, err)
	}
	if shouldAutoLaunchGUI(args, *openGUI, *statusOnly, *reloadOnly, *stopOnly, *printConfig) {
		if err := startGUI(*guiBin, *configPath, *ipcPath, *guiHashWarn, stderr); err != nil {
			_, _ = fmt.Fprintf(stderr, "warn: auto GUI launch failed: %v\n", err)
		} else {
			_, _ = fmt.Fprintln(stdout, "gui launched")
		}
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

func shouldAutoLaunchGUI(rawArgs []string, openGUI, statusOnly, reloadOnly, stopOnly, printConfig bool) bool {
	if openGUI || statusOnly || reloadOnly || stopOnly || printConfig {
		return false
	}
	return len(rawArgs) == 0
}

var startGUI = func(guiBin, configPath, ipcPath string, guiHashWarn bool, stderr io.Writer) error {
	bin := guiBin
	if strings.TrimSpace(bin) == "" {
		bin = defaultGUIBinaryName()
	}
	securityWarn, err := verifyGUIBinary(bin, guiHashWarn, stderr)
	if err != nil {
		return err
	}
	cmd := exec.Command(bin, "--config", configPath, "--ipc", ipcPath)
	if strings.TrimSpace(securityWarn) != "" {
		cmd.Env = append(os.Environ(), "VRC_LOGHOOK_SECURITY_WARN="+securityWarn)
		sendSecurityToastBestEffort(securityWarn)
	}
	applyGUIProcessAttrs(cmd)
	return cmd.Start()
}

func defaultGUIBinaryName() string {
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		if found, ok := chooseGUIBinaryInDir(dir, runtime.GOOS); ok {
			return found
		}
		if runtime.GOOS == "windows" {
			return filepath.Join(dir, "vrc-loghook-gui.exe")
		}
		return filepath.Join(dir, "vrc-loghook-gui")
	}
	if runtime.GOOS == "windows" {
		return "vrc-loghook-gui.exe"
	}
	return "vrc-loghook-gui"
}

func chooseGUIBinaryInDir(dir, goos string) (string, bool) {
	var primaryCandidates []string
	var globPattern string
	switch goos {
	case "windows":
		primaryCandidates = []string{
			"vrc-loghook-gui.exe",
			"vrc-loghook-gui-windows-amd64.exe",
		}
		globPattern = "vrc-loghook-gui*.exe"
	case "darwin":
		primaryCandidates = []string{
			"vrc-loghook-gui",
			"vrc-loghook-gui-darwin-amd64",
		}
		globPattern = "vrc-loghook-gui*"
	default:
		primaryCandidates = []string{
			"vrc-loghook-gui",
			"vrc-loghook-gui-linux-amd64",
		}
		globPattern = "vrc-loghook-gui*"
	}

	for _, name := range primaryCandidates {
		p := filepath.Join(dir, name)
		if isRunnableGUIBinary(p, goos) {
			return p, true
		}
	}

	matches, _ := filepath.Glob(filepath.Join(dir, globPattern))
	sort.Strings(matches)
	for _, p := range matches {
		if isRunnableGUIBinary(p, goos) {
			return p, true
		}
	}
	return "", false
}

func isRunnableGUIBinary(path, goos string) bool {
	st, err := os.Stat(path)
	if err != nil || st.IsDir() {
		return false
	}
	if goos == "windows" {
		return strings.HasSuffix(strings.ToLower(path), ".exe")
	}
	return st.Mode().Perm()&0o111 != 0
}

var expectedGUIHash string

const expectedGUIIdentityPrefix = "vrc-loghook-gui/"

func verifyGUIBinary(path string, guiHashWarn bool, stderr io.Writer) (string, error) {
	if err := verifyGUIIdentity(path); err != nil {
		return "", fmt.Errorf("gui identity check failed: %w", err)
	}
	if strings.TrimSpace(expectedGUIHash) == "" {
		return "", nil
	}
	got, err := fileSHA256(path)
	if err != nil {
		return "", fmt.Errorf("gui hash check failed: %w", err)
	}
	warnText := ""
	if !strings.EqualFold(strings.TrimSpace(expectedGUIHash), got) && guiHashWarn {
		warnText = fmt.Sprintf("GUI hash mismatch: expected=%s actual=%s path=%s", maskHash(expectedGUIHash), maskHash(got), path)
		_, _ = fmt.Fprintf(stderr, "warn: %s\n", warnText)
	}
	return warnText, nil
}

func verifyGUIIdentity(path string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "--identity").Output()
	if err != nil {
		return err
	}
	identity := strings.TrimSpace(string(out))
	if !strings.HasPrefix(identity, expectedGUIIdentityPrefix) {
		return fmt.Errorf("unexpected identity: %q", identity)
	}
	return nil
}

func fileSHA256(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

func maskHash(h string) string {
	s := strings.TrimSpace(h)
	if len(s) <= 12 {
		return s
	}
	return s[:8] + "..." + s[len(s)-4:]
}

func sendSecurityToastBestEffort(message string) {
	msg := strings.TrimSpace(message)
	if msg == "" {
		return
	}
	switch runtime.GOOS {
	case "windows":
		// Best-effort Windows toast via PowerShell Windows Runtime APIs.
		escaped := xmlEscape(msg)
		toastScript := "$ErrorActionPreference='Stop'; " +
			"[Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime] > $null; " +
			"[Windows.Data.Xml.Dom.XmlDocument, Windows.Data.Xml.Dom.XmlDocument, ContentType = WindowsRuntime] > $null; " +
			"$xml = New-Object Windows.Data.Xml.Dom.XmlDocument; " +
			"$xml.LoadXml(\"<toast><visual><binding template='ToastGeneric'><text>VRC LogHook</text><text>" + escaped + "</text></binding></visual></toast>\"); " +
			"$toast = [Windows.UI.Notifications.ToastNotification]::new($xml); " +
			"[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier('VRC LogHook').Show($toast)"
		go func() {
			out, err := exec.Command("powershell.exe", "-NoProfile", "-Command", toastScript).CombinedOutput()
			if err == nil {
				return
			}
			// Fallback: visible popup if toast cannot be shown in this environment.
			popup := "$ws=New-Object -ComObject WScript.Shell; " +
				"$null=$ws.Popup('" + psSingleQuote(msg) + "', 8, 'VRC LogHook Security Warning', 48)"
			_, popupErr := exec.Command("powershell.exe", "-NoProfile", "-Command", popup).CombinedOutput()
			if popupErr != nil {
				fmt.Fprintf(os.Stderr, "warn: security notification failed (toast and popup): %v toast_out=%s\n", err, strings.TrimSpace(string(out)))
			} else {
				fmt.Fprintf(os.Stderr, "warn: toast notification failed; fallback popup was shown: %v\n", err)
			}
		}()
	case "darwin":
		_ = exec.Command("osascript", "-e", `display notification "`+escapeForDoubleQuote(msg)+`" with title "VRC LogHook"`).Start()
	default:
		if _, err := exec.LookPath("notify-send"); err == nil {
			_ = exec.Command("notify-send", "VRC LogHook", msg).Start()
		}
	}
}

func escapeForDoubleQuote(s string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s)
}

func xmlEscape(s string) string {
	return strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&apos;",
	).Replace(s)
}

func psSingleQuote(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}
