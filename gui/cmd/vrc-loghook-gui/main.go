package main

import (
	"encoding/json"
	"errors"
	"flag"
	"html/template"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type request struct {
	Token  string `json:"token"`
	Method string `json:"method"`
}

type response struct {
	OK    bool           `json:"ok"`
	Error string         `json:"error,omitempty"`
	Body  map[string]any `json:"body,omitempty"`
}

type guiConfig struct {
	Token   string `json:"token"`
	Monitor struct {
		PollIntervalSec int    `json:"poll_interval_sec"`
		LogDir          string `json:"log_dir"`
		FileGlob        string `json:"file_glob"`
	} `json:"monitor"`
	Notify struct {
		Discord struct {
			Enabled    bool   `json:"enabled"`
			WebhookURL string `json:"webhook_url"`
		} `json:"discord"`
	} `json:"notify"`
	Hooks struct {
		Enabled       bool `json:"enabled"`
		UnsafeConsent bool `json:"unsafe_consent"`
	} `json:"hooks"`
	Runtime struct {
		DryRun bool `json:"dry_run"`
	} `json:"runtime"`
	Observability struct {
		LogLevel string `json:"log_level"`
		Stdout   bool   `json:"stdout"`
	} `json:"observability"`
}

type pageData struct {
	Now         string
	Status      map[string]any
	StatusError string
	ConfigPath  string
	IPCPath     string
	Config      guiConfig
	Message     string
}

func main() {
	configPath := flag.String("config", defaultConfigPath(), "config path")
	ipcPath := flag.String("ipc", defaultIPCPath(), "ipc path")
	listen := flag.String("listen", "127.0.0.1:18419", "gui http listen addr")
	open := flag.Bool("open-browser", true, "open browser automatically")
	flag.Parse()

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		renderPage(w, *configPath, *ipcPath, "")
	})
	mux.HandleFunc("/save", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		msg := handleSave(r, *configPath, *ipcPath)
		renderPage(w, *configPath, *ipcPath, msg)
	})
	mux.HandleFunc("/action", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		msg := handleAction(r, *configPath, *ipcPath)
		renderPage(w, *configPath, *ipcPath, msg)
	})

	url := "http://" + *listen
	if *open {
		go func() {
			time.Sleep(300 * time.Millisecond)
			_ = openBrowser(url)
		}()
	}
	log.Printf("gui listening on %s", url)
	if err := http.ListenAndServe(*listen, mux); err != nil {
		log.Fatal(err)
	}
}

func handleSave(r *http.Request, configPath, ipcPath string) string {
	if err := r.ParseForm(); err != nil {
		return "save failed: " + err.Error()
	}
	cfg, err := loadConfig(configPath)
	if err != nil {
		return "save failed: " + err.Error()
	}

	cfg.Monitor.PollIntervalSec = mustInt(r.FormValue("monitor.poll_interval_sec"), cfg.Monitor.PollIntervalSec)
	cfg.Monitor.LogDir = strings.TrimSpace(r.FormValue("monitor.log_dir"))
	cfg.Monitor.FileGlob = strings.TrimSpace(r.FormValue("monitor.file_glob"))
	cfg.Notify.Discord.Enabled = r.FormValue("notify.discord.enabled") == "on"
	cfg.Notify.Discord.WebhookURL = strings.TrimSpace(r.FormValue("notify.discord.webhook_url"))
	cfg.Runtime.DryRun = r.FormValue("runtime.dry_run") == "on"
	cfg.Observability.LogLevel = strings.TrimSpace(r.FormValue("observability.log_level"))
	cfg.Observability.Stdout = r.FormValue("observability.stdout") == "on"
	// Advanced hooks section (hidden by default in UI).
	cfg.Hooks.Enabled = r.FormValue("hooks.enabled") == "on"
	cfg.Hooks.UnsafeConsent = r.FormValue("hooks.unsafe_consent") == "on"

	if err := saveConfig(configPath, cfg); err != nil {
		return "save failed: " + err.Error()
	}
	if _, err := callIPC(ipcPath, cfg.Token, "config.reload"); err != nil {
		return "saved, but reload failed: " + err.Error()
	}
	return "saved and reloaded"
}

func handleAction(r *http.Request, configPath, ipcPath string) string {
	cfg, err := loadConfig(configPath)
	if err != nil {
		return "action failed: " + err.Error()
	}
	switch r.FormValue("action") {
	case "reload":
		_, err = callIPC(ipcPath, cfg.Token, "config.reload")
	case "stop":
		_, err = callIPC(ipcPath, cfg.Token, "stop")
	default:
		return "unknown action"
	}
	if err != nil {
		return "action failed: " + err.Error()
	}
	return "action success: " + r.FormValue("action")
}

func renderPage(w http.ResponseWriter, configPath, ipcPath, message string) {
	cfg, cfgErr := loadConfig(configPath)
	status := map[string]any{}
	statusErr := ""
	if cfgErr == nil {
		resp, err := callIPC(ipcPath, cfg.Token, "status")
		if err != nil {
			statusErr = err.Error()
		} else if !resp.OK {
			statusErr = resp.Error
		} else {
			status = resp.Body
		}
	} else {
		statusErr = cfgErr.Error()
	}

	data := pageData{
		Now:         time.Now().Format(time.RFC3339),
		Status:      status,
		StatusError: statusErr,
		ConfigPath:  configPath,
		IPCPath:     ipcPath,
		Config:      cfg,
		Message:     message,
	}
	tpl := template.Must(template.New("page").Parse(pageHTML))
	_ = tpl.Execute(w, data)
}

func callIPC(ipcPath, token, method string) (response, error) {
	conn, err := dialIPC(ipcPath)
	if err != nil {
		return response{}, err
	}
	defer conn.Close()
	if err := json.NewEncoder(conn).Encode(request{Token: token, Method: method}); err != nil {
		return response{}, err
	}
	var resp response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return response{}, err
	}
	return resp, nil
}

func loadConfig(path string) (guiConfig, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return guiConfig{}, err
	}
	b = sanitizeHJSONLike(b)
	var cfg guiConfig
	if err := json.Unmarshal(b, &cfg); err != nil {
		return guiConfig{}, err
	}
	if cfg.Observability.LogLevel == "" {
		cfg.Observability.LogLevel = "info"
	}
	return cfg, nil
}

func saveConfig(path string, cfg guiConfig) error {
	if cfg.Token == "" {
		return errors.New("token is empty")
	}
	if cfg.Monitor.PollIntervalSec < 1 || cfg.Monitor.PollIntervalSec > 60 {
		return errors.New("monitor.poll_interval_sec must be 1..60")
	}
	if cfg.Monitor.LogDir == "" || cfg.Monitor.FileGlob == "" {
		return errors.New("monitor.log_dir/file_glob is required")
	}
	if cfg.Observability.LogLevel == "" {
		cfg.Observability.LogLevel = "info"
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o600)
}

func sanitizeHJSONLike(b []byte) []byte {
	clean := stripCommentsOutsideStrings(string(b))
	trailingComma := regexp.MustCompile(`,\s*([}\]])`)
	return trailingComma.ReplaceAll([]byte(clean), []byte(`$1`))
}

func stripCommentsOutsideStrings(src string) string {
	var out strings.Builder
	out.Grow(len(src))
	inString := false
	escape := false
	for i := 0; i < len(src); i++ {
		c := src[i]
		if inString {
			out.WriteByte(c)
			if escape {
				escape = false
				continue
			}
			if c == '\\' {
				escape = true
				continue
			}
			if c == '"' {
				inString = false
			}
			continue
		}
		if c == '"' {
			inString = true
			out.WriteByte(c)
			continue
		}
		if c == '/' && i+1 < len(src) && src[i+1] == '/' {
			i += 2
			for i < len(src) && src[i] != '\n' {
				i++
			}
			if i < len(src) {
				out.WriteByte('\n')
			}
			continue
		}
		if c == '#' {
			i++
			for i < len(src) && src[i] != '\n' {
				i++
			}
			if i < len(src) {
				out.WriteByte('\n')
			}
			continue
		}
		if c == '/' && i+1 < len(src) && src[i+1] == '*' {
			i += 2
			for i+1 < len(src) && !(src[i] == '*' && src[i+1] == '/') {
				i++
			}
			i++
			continue
		}
		out.WriteByte(c)
	}
	return out.String()
}

func defaultConfigPath() string {
	home, _ := os.UserHomeDir()
	if home == "" {
		home = "."
	}
	switch runtime.GOOS {
	case "windows":
		localApp := os.Getenv("LOCALAPPDATA")
		if localApp != "" {
			return filepath.Join(localApp, "VRCLogHook", "config.hjson")
		}
		return filepath.Join(home, "AppData", "Local", "VRCLogHook", "config.hjson")
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "VRCLogHook", "config.hjson")
	default:
		return filepath.Join(home, ".config", "vrc-loghook", "config.hjson")
	}
}

func defaultIPCPath() string {
	if runtime.GOOS == "windows" {
		return `\\.\pipe\vrc-loghook`
	}
	return filepath.Join(os.TempDir(), "vrc-loghook.sock")
}

func mustInt(raw string, fallback int) int {
	v, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return fallback
	}
	return v
}

func openBrowser(url string) error {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		return exec.Command("open", url).Start()
	default:
		return exec.Command("xdg-open", url).Start()
	}
}

var pageHTML = `
<!doctype html>
<html lang="ja">
<head>
  <meta charset="utf-8"/>
  <meta name="viewport" content="width=device-width,initial-scale=1"/>
  <title>VRC LogHook GUI</title>
  <style>
    :root { --bg:#f6f7fb; --card:#fff; --text:#172033; --muted:#65708a; --line:#d7dbea; --accent:#1663d6; }
    body { margin:0; font-family: "Yu Gothic UI","Hiragino Sans",sans-serif; background:linear-gradient(180deg,#eef3ff,#f9fbff); color:var(--text); }
    .wrap { max-width:1100px; margin:20px auto; padding:0 14px; }
    .card { background:var(--card); border:1px solid var(--line); border-radius:12px; padding:14px; margin-bottom:12px; box-shadow:0 4px 18px rgba(13,34,77,.06);}
    h1,h2 { margin:0 0 12px 0; }
    .grid { display:grid; grid-template-columns: 1fr 1fr; gap:12px; }
    .muted { color:var(--muted); font-size:13px; }
    label { display:block; margin:8px 0 4px; font-size:13px; }
    input[type=text], input[type=number], select { width:100%; box-sizing:border-box; padding:8px; border:1px solid var(--line); border-radius:8px; }
    .inline { display:flex; gap:8px; align-items:center; flex-wrap:wrap;}
    button { border:none; border-radius:8px; padding:8px 12px; background:var(--accent); color:white; cursor:pointer; }
    button.secondary { background:#55617e; }
    code { font-size:12px; background:#f1f4fb; padding:2px 4px; border-radius:4px;}
    .star::after { content:"*"; color:#c53939; margin-left:4px; }
    details { margin-top:10px; padding:8px; border:1px dashed var(--line); border-radius:8px; }
    .warn { color:#a54700; }
    @media (max-width: 840px){ .grid { grid-template-columns: 1fr; } }
  </style>
</head>
<body>
  <div class="wrap">
    <h1>VRC LogHook GUI</h1>
    <div class="muted">now: {{.Now}} | config: <code>{{.ConfigPath}}</code> | ipc: <code>{{.IPCPath}}</code></div>
    {{if .Message}}<div class="card"><strong>{{.Message}}</strong></div>{{end}}

    <div class="card">
      <h2>状態</h2>
      {{if .StatusError}}
      <div class="warn">status error: {{.StatusError}}</div>
      {{else}}
      <div class="inline">
        <div>running: <strong>{{index .Status "running"}}</strong></div>
        <div>file: <code>{{index .Status "current_log_file"}}</code></div>
        <div>offset: <code>{{index .Status "current_offset"}}</code></div>
        <div>last_event: <code>{{index .Status "last_event_at_rfc3339"}}</code></div>
      </div>
      {{end}}
      <form method="post" action="/action" class="inline" style="margin-top:10px;">
        <button type="submit" name="action" value="reload">設定再読込</button>
        <button type="submit" class="secondary" name="action" value="stop">監視停止</button>
      </form>
    </div>

    <form method="post" action="/save" class="card">
      <h2>設定編集</h2>
      <div class="grid">
        <div>
          <label>monitor.poll_interval_sec</label>
          <input type="number" min="1" max="60" name="monitor.poll_interval_sec" value="{{.Config.Monitor.PollIntervalSec}}">
          <label>monitor.log_dir</label>
          <input type="text" name="monitor.log_dir" value="{{.Config.Monitor.LogDir}}">
          <label>monitor.file_glob</label>
          <input type="text" name="monitor.file_glob" value="{{.Config.Monitor.FileGlob}}">
        </div>
        <div>
          <label>notify.discord.webhook_url</label>
          <input type="text" name="notify.discord.webhook_url" value="{{.Config.Notify.Discord.WebhookURL}}">
          <label>observability.log_level</label>
          <select name="observability.log_level">
            <option value="debug" {{if eq .Config.Observability.LogLevel "debug"}}selected{{end}}>debug</option>
            <option value="info" {{if eq .Config.Observability.LogLevel "info"}}selected{{end}}>info</option>
            <option value="warn" {{if eq .Config.Observability.LogLevel "warn"}}selected{{end}}>warn</option>
            <option value="error" {{if eq .Config.Observability.LogLevel "error"}}selected{{end}}>error</option>
          </select>
          <div class="inline">
            <label><input type="checkbox" name="notify.discord.enabled" {{if .Config.Notify.Discord.Enabled}}checked{{end}}> notify.discord.enabled</label>
            <label><input type="checkbox" name="runtime.dry_run" {{if .Config.Runtime.DryRun}}checked{{end}}> runtime.dry_run</label>
            <label><input type="checkbox" name="observability.stdout" {{if .Config.Observability.Stdout}}checked{{end}}> observability.stdout</label>
          </div>
          <div class="muted"><span class="star"></span> は再起動推奨項目（例: token / 自己ログ保存先の変更）</div>
        </div>
      </div>
      <details>
        <summary>上級者向け: Hook設定（危険性あり）</summary>
        <p class="warn">Hookは任意コマンドを実行できます。危険性を理解した場合のみ有効化してください。</p>
        <div class="inline">
          <label><input type="checkbox" name="hooks.enabled" {{if .Config.Hooks.Enabled}}checked{{end}}> hooks.enabled</label>
          <label><input type="checkbox" name="hooks.unsafe_consent" {{if .Config.Hooks.UnsafeConsent}}checked{{end}}> hooks.unsafe_consent</label>
        </div>
      </details>
      <div style="margin-top:10px;">
        <button type="submit">保存して再読込</button>
      </div>
    </form>
  </div>
</body>
</html>
`
