package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

var (
	version   = "dev"
	revision  = "unknown"
	buildTime = "unknown"
)

type request struct {
	Token  string `json:"token"`
	Method string `json:"method"`
	Body   any    `json:"body,omitempty"`
}

type response struct {
	OK    bool           `json:"ok"`
	Error string         `json:"error,omitempty"`
	Body  map[string]any `json:"body,omitempty"`
}

type guiConfig struct {
	Version string `json:"version"`
	Token   string `json:"token"`
	Monitor struct {
		PollIntervalSec int    `json:"poll_interval_sec"`
		LogDir          string `json:"log_dir"`
		FileGlob        string `json:"file_glob"`
		CheckExisting   bool   `json:"check_existing_on_first_run"`
	} `json:"monitor"`
	State struct {
		Path            string `json:"path"`
		SaveIntervalSec int    `json:"save_interval_sec"`
	} `json:"state"`
	Notify struct {
		Discord struct {
			Enabled        bool              `json:"enabled"`
			WebhookURL     string            `json:"webhook_url"`
			GroupWebhooks  map[string]string `json:"group_webhooks"`
			Groups         []string          `json:"groups"`
			Username       string            `json:"username"`
			MaxContentRune int               `json:"max_content_rune"`
			MinIntervalSec int               `json:"min_interval_sec"`
		} `json:"discord"`
		Local struct {
			Path string `json:"path"`
		} `json:"local"`
		Retry struct {
			MaxAttempts      int `json:"max_attempts"`
			InitialBackoffMs int `json:"initial_backoff_ms"`
			MaxBackoffMs     int `json:"max_backoff_ms"`
		} `json:"retry"`
	} `json:"notify"`
	Match struct {
		Rules           []guiRule `json:"rules"`
		DedupeWindowSec int       `json:"dedupe_window_sec"`
	} `json:"match"`
	Hooks struct {
		Enabled        bool             `json:"enabled"`
		UnsafeConsent  bool             `json:"unsafe_consent"`
		MaxConcurrency int              `json:"max_concurrency"`
		TimeoutSec     int              `json:"timeout_sec"`
		Commands       []guiHookCommand `json:"commands"`
	} `json:"hooks"`
	Runtime struct {
		DryRun          bool `json:"dry_run"`
		HotReload       bool `json:"hot_reload"`
		ConfigReloadSec int  `json:"config_reload_sec"`
	} `json:"runtime"`
	Observability struct {
		SelfLogPath  string `json:"self_log_path"`
		StatusLogSec int    `json:"status_log_sec"`
		LogLevel     string `json:"log_level"`
		Stdout       bool   `json:"stdout"`
	} `json:"observability"`
}

type guiRule struct {
	Enabled         bool   `json:"enabled"`
	Name            string `json:"name"`
	Group           string `json:"group"`
	Contains        string `json:"contains"`
	Regex           string `json:"regex"`
	CaseSensitive   bool   `json:"case_sensitive"`
	MessageTemplate string `json:"message_template"`
}

type guiHookCommand struct {
	Name    string   `json:"name"`
	Enabled bool     `json:"enabled"`
	Program string   `json:"program"`
	Args    []string `json:"args"`
}

type pageData struct {
	Now               string
	Status            map[string]any
	StatusError       string
	ConfigPath        string
	IPCPath           string
	Config            guiConfig
	RulesJSON         string
	GroupsJSON        string
	GroupWebhooksJSON string
	HooksJSON         string
	DefaultRules      template.JS
	CurrentRules      template.JS
	CurrentGroups     template.JS
	CurrentGroupHooks template.JS
	DefaultHooks      template.JS
	CurrentHooks      template.JS
	Message           string
}

func main() {
	configPath := flag.String("config", defaultConfigPath(), "config path")
	ipcPath := flag.String("ipc", defaultIPCPath(), "ipc path")
	listen := flag.String("listen", "127.0.0.1:18419", "gui http listen addr")
	open := flag.Bool("open-browser", true, "open browser automatically")
	identity := flag.Bool("identity", false, "print GUI identity and exit")
	flag.Parse()

	if *identity {
		_, _ = os.Stdout.WriteString(guiIdentity() + "\n")
		return
	}

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
	mux.HandleFunc("/gui-log", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		_ = handleGUILog(r, *configPath, *ipcPath)
		w.WriteHeader(http.StatusNoContent)
	})

	url := "http://" + *listen
	if *open {
		go func() {
			time.Sleep(300 * time.Millisecond)
			_ = openBrowser(url)
		}()
	}
	log.Printf("gui listening on %s", url)
	log.Printf("build: version=%s revision=%s built_at=%s", version, revision, buildTime)
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
	cfg.Monitor.CheckExisting = r.FormValue("monitor.check_existing_on_first_run") == "on"
	cfg.State.Path = strings.TrimSpace(r.FormValue("state.path"))
	cfg.State.SaveIntervalSec = mustInt(r.FormValue("state.save_interval_sec"), cfg.State.SaveIntervalSec)
	cfg.Notify.Discord.Enabled = r.FormValue("notify.discord.enabled") == "on"
	cfg.Notify.Discord.WebhookURL = strings.TrimSpace(r.FormValue("notify.discord.webhook_url"))
	cfg.Notify.Discord.Username = strings.TrimSpace(r.FormValue("notify.discord.username"))
	if parsed, err := parseStringArrayJSON(r.FormValue("notify.discord.groups_json")); err != nil {
		return "save failed: invalid notify.discord.groups_json: " + err.Error()
	} else {
		cfg.Notify.Discord.Groups = parsed
	}
	if parsed, err := parseStringMapJSON(r.FormValue("notify.discord.group_webhooks_json")); err != nil {
		return "save failed: invalid notify.discord.group_webhooks_json: " + err.Error()
	} else {
		cfg.Notify.Discord.GroupWebhooks = parsed
	}
	cfg.Notify.Discord.MaxContentRune = mustInt(r.FormValue("notify.discord.max_content_rune"), cfg.Notify.Discord.MaxContentRune)
	cfg.Notify.Discord.MinIntervalSec = mustInt(r.FormValue("notify.discord.min_interval_sec"), cfg.Notify.Discord.MinIntervalSec)
	cfg.Notify.Local.Path = strings.TrimSpace(r.FormValue("notify.local.path"))
	cfg.Notify.Retry.MaxAttempts = mustInt(r.FormValue("notify.retry.max_attempts"), cfg.Notify.Retry.MaxAttempts)
	cfg.Notify.Retry.InitialBackoffMs = mustInt(r.FormValue("notify.retry.initial_backoff_ms"), cfg.Notify.Retry.InitialBackoffMs)
	cfg.Notify.Retry.MaxBackoffMs = mustInt(r.FormValue("notify.retry.max_backoff_ms"), cfg.Notify.Retry.MaxBackoffMs)
	cfg.Match.DedupeWindowSec = mustInt(r.FormValue("match.dedupe_window_sec"), cfg.Match.DedupeWindowSec)
	cfg.Runtime.HotReload = r.FormValue("runtime.hot_reload") == "on"
	cfg.Runtime.ConfigReloadSec = mustInt(r.FormValue("runtime.config_reload_sec"), cfg.Runtime.ConfigReloadSec)
	cfg.Runtime.DryRun = r.FormValue("runtime.dry_run") == "on"
	cfg.Observability.SelfLogPath = strings.TrimSpace(r.FormValue("observability.self_log_path"))
	cfg.Observability.StatusLogSec = mustInt(r.FormValue("observability.status_log_sec"), cfg.Observability.StatusLogSec)
	cfg.Observability.LogLevel = strings.TrimSpace(r.FormValue("observability.log_level"))
	cfg.Observability.Stdout = r.FormValue("observability.stdout") == "on"
	// Advanced hooks section (hidden by default in UI).
	cfg.Hooks.Enabled = r.FormValue("hooks.enabled") == "on"
	cfg.Hooks.UnsafeConsent = r.FormValue("hooks.unsafe_consent") == "on"
	cfg.Hooks.MaxConcurrency = mustInt(r.FormValue("hooks.max_concurrency"), cfg.Hooks.MaxConcurrency)
	cfg.Hooks.TimeoutSec = mustInt(r.FormValue("hooks.timeout_sec"), cfg.Hooks.TimeoutSec)

	rawRules := strings.TrimSpace(r.FormValue("match.rules_json"))
	if rawRules != "" {
		if parsed, err := parseRulesJSON(rawRules); err != nil {
			return "save failed: invalid match.rules_json: " + err.Error()
		} else {
			cfg.Match.Rules = parsed
		}
	} else {
		if parsed, ok := parseRulesFromForm(r.Form); ok {
			cfg.Match.Rules = parsed
		}
	}
	if parsed, err := parseHookCommandsJSON(r.FormValue("hooks.commands_json")); err != nil {
		return "save failed: invalid hooks.commands_json: " + err.Error()
	} else {
		cfg.Hooks.Commands = parsed
	}
	normalizeGroupsAndRuleGroups(&cfg)

	if err := saveConfig(configPath, cfg); err != nil {
		return "save failed: " + err.Error()
	}
	if _, err := callIPC(ipcPath, resolveIPCToken(configPath, cfg.Token), "config.reload"); err != nil {
		return "saved, but reload failed: " + err.Error()
	}
	return "saved and reloaded"
}

func handleAction(r *http.Request, configPath, ipcPath string) string {
	cfg, err := loadConfig(configPath)
	if err != nil {
		cfg = defaultGUIConfig(configPath)
	}
	switch r.FormValue("action") {
	case "reload":
		_, err = callIPC(ipcPath, resolveIPCToken(configPath, cfg.Token), "config.reload")
	case "stop":
		_, err = callIPC(ipcPath, resolveIPCToken(configPath, cfg.Token), "stop")
	case "open-config-dir":
		if openErr := openDirectory(filepath.Dir(configPath)); openErr != nil {
			return "open config dir failed: " + openErr.Error()
		}
		return "opened config directory"
	case "reset-defaults":
		def := defaultGUIConfig(configPath)
		def.Token = cfg.Token
		if saveErr := saveConfig(configPath, def); saveErr != nil {
			return "reset failed: " + saveErr.Error()
		}
		_, err = callIPC(ipcPath, resolveIPCToken(configPath, cfg.Token), "config.reload")
		if err != nil {
			return "reset saved, but reload failed: " + err.Error()
		}
		return "reset to defaults and reloaded"
	case "add-rule":
		normalizeGroupsAndRuleGroups(&cfg)
		group := "info"
		if len(cfg.Notify.Discord.Groups) > 0 {
			group = cfg.Notify.Discord.Groups[0]
		}
		cfg.Match.Rules = append(cfg.Match.Rules, guiRule{
			Enabled:         true,
			Name:            "new-rule",
			Group:           group,
			Contains:        "__edit_me__",
			MessageTemplate: "[{rule}] {line}",
		})
		if saveErr := saveConfig(configPath, cfg); saveErr != nil {
			return "add rule failed: " + saveErr.Error()
		}
		_, err = callIPC(ipcPath, resolveIPCToken(configPath, cfg.Token), "config.reload")
		if err != nil {
			return "rule added, but reload failed: " + err.Error()
		}
		return "rule added and reloaded"
	case "reset-rules-defaults":
		cfg.Match.Rules = defaultRules()
		normalizeGroupsAndRuleGroups(&cfg)
		if saveErr := saveConfig(configPath, cfg); saveErr != nil {
			return "reset rules failed: " + saveErr.Error()
		}
		_, err = callIPC(ipcPath, resolveIPCToken(configPath, cfg.Token), "config.reload")
		if err != nil {
			return "rules reset saved, but reload failed: " + err.Error()
		}
		return "rules reset to defaults and reloaded"
	case "delete-rule":
		idx := mustInt(r.FormValue("rule_delete_index"), -1)
		if idx < 0 || idx >= len(cfg.Match.Rules) {
			return "delete rule failed: invalid index"
		}
		cfg.Match.Rules = append(cfg.Match.Rules[:idx], cfg.Match.Rules[idx+1:]...)
		if len(cfg.Match.Rules) == 0 {
			cfg.Match.Rules = defaultRules()
		}
		normalizeGroupsAndRuleGroups(&cfg)
		if saveErr := saveConfig(configPath, cfg); saveErr != nil {
			return "delete rule failed: " + saveErr.Error()
		}
		_, err = callIPC(ipcPath, resolveIPCToken(configPath, cfg.Token), "config.reload")
		if err != nil {
			return "rule deleted, but reload failed: " + err.Error()
		}
		return "rule deleted and reloaded"
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
	if cfgErr != nil {
		cfg = defaultGUIConfig(configPath)
	}
	if len(cfg.Match.Rules) == 0 {
		cfg.Match.Rules = defaultRules()
	}
	normalizeGroupsAndRuleGroups(&cfg)
	status := map[string]any{}
	statusErr := ""
	if cfgErr == nil {
		resp, err := callIPC(ipcPath, resolveIPCToken(configPath, cfg.Token), "status")
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
		Now:               time.Now().Format(time.RFC3339),
		Status:            status,
		StatusError:       statusErr,
		ConfigPath:        configPath,
		IPCPath:           ipcPath,
		Config:            cfg,
		RulesJSON:         prettyJSON(cfg.Match.Rules),
		GroupsJSON:        prettyJSON(cfg.Notify.Discord.Groups),
		GroupWebhooksJSON: prettyJSON(cfg.Notify.Discord.GroupWebhooks),
		HooksJSON:         prettyJSON(cfg.Hooks.Commands),
		DefaultRules:      template.JS(jsonOrEmpty(defaultRules())),
		CurrentRules:      template.JS(jsonOrEmpty(cfg.Match.Rules)),
		CurrentGroups:     template.JS(jsonOrEmpty(cfg.Notify.Discord.Groups)),
		CurrentGroupHooks: template.JS(jsonOrEmpty(cfg.Notify.Discord.GroupWebhooks)),
		DefaultHooks:      template.JS(jsonOrEmpty(defaultHookCommands())),
		CurrentHooks:      template.JS(jsonOrEmpty(cfg.Hooks.Commands)),
		Message:           message,
	}
	logToCore(configPath, ipcPath, "info", fmt.Sprintf("renderPage: rules=%d hooks=%d status_error=%v", len(cfg.Match.Rules), len(cfg.Hooks.Commands), statusErr != ""))
	tpl := template.Must(template.New("page").Parse(pageHTML))
	_ = tpl.Execute(w, data)
}

func handleGUILog(r *http.Request, configPath, ipcPath string) error {
	if err := r.ParseForm(); err != nil {
		return err
	}
	level := strings.TrimSpace(r.FormValue("level"))
	if level == "" {
		level = "info"
	}
	message := strings.TrimSpace(r.FormValue("message"))
	if message == "" {
		return nil
	}
	return logToCore(configPath, ipcPath, level, message)
}

func logToCore(configPath, ipcPath, level, message string) error {
	cfg, err := loadConfig(configPath)
	if err != nil {
		cfg = defaultGUIConfig(configPath)
	}
	token := resolveIPCToken(configPath, cfg.Token)
	_, err = callIPC(ipcPath, token, "gui.log", map[string]any{
		"level":   level,
		"message": message,
	})
	return err
}

func callIPC(ipcPath, token, method string, body ...any) (response, error) {
	conn, err := dialIPC(ipcPath)
	if err != nil {
		return response{}, err
	}
	defer conn.Close()
	req := request{Token: token, Method: method}
	if len(body) > 0 {
		req.Body = body[0]
	}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
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
	applyGUIRuleEnabledDefaultsFromRaw(b, &cfg)
	if cfg.Observability.LogLevel == "" {
		cfg.Observability.LogLevel = "info"
	}
	if cfg.Notify.Discord.MaxContentRune == 0 {
		cfg.Notify.Discord.MaxContentRune = 1600
	}
	if cfg.Notify.Discord.GroupWebhooks == nil {
		cfg.Notify.Discord.GroupWebhooks = map[string]string{}
	}
	if len(cfg.Notify.Discord.Groups) == 0 {
		cfg.Notify.Discord.Groups = []string{"info", "error"}
	}
	if cfg.Notify.Retry.MaxAttempts == 0 {
		cfg.Notify.Retry.MaxAttempts = 3
	}
	if cfg.Notify.Retry.InitialBackoffMs == 0 {
		cfg.Notify.Retry.InitialBackoffMs = 500
	}
	if cfg.Notify.Retry.MaxBackoffMs == 0 {
		cfg.Notify.Retry.MaxBackoffMs = 5000
	}
	if cfg.State.SaveIntervalSec == 0 {
		cfg.State.SaveIntervalSec = 10
	}
	if cfg.Runtime.ConfigReloadSec == 0 {
		cfg.Runtime.ConfigReloadSec = 2
	}
	if cfg.Observability.StatusLogSec == 0 {
		cfg.Observability.StatusLogSec = 30
	}
	if cfg.Hooks.MaxConcurrency == 0 {
		cfg.Hooks.MaxConcurrency = 1
	}
	if cfg.Hooks.TimeoutSec == 0 {
		cfg.Hooks.TimeoutSec = 5
	}
	if len(cfg.Match.Rules) == 0 {
		cfg.Match.Rules = defaultRules()
	}
	normalizeGroupsAndRuleGroups(&cfg)
	for i := range cfg.Match.Rules {
		if strings.TrimSpace(cfg.Match.Rules[i].Group) == "" {
			cfg.Match.Rules[i].Group = defaultRuleGroupGUI(cfg.Match.Rules[i].Name)
		}
	}
	return cfg, nil
}

func applyGUIRuleEnabledDefaultsFromRaw(clean []byte, cfg *guiConfig) {
	if len(cfg.Match.Rules) == 0 {
		return
	}
	var raw struct {
		Match struct {
			Rules []map[string]any `json:"rules"`
		} `json:"match"`
	}
	if err := json.Unmarshal(clean, &raw); err != nil {
		return
	}
	for i := range cfg.Match.Rules {
		if i >= len(raw.Match.Rules) {
			cfg.Match.Rules[i].Enabled = true
			continue
		}
		if _, ok := raw.Match.Rules[i]["enabled"]; !ok {
			cfg.Match.Rules[i].Enabled = true
		}
	}
}

func defaultGUIConfig(path string) guiConfig {
	home, _ := os.UserHomeDir()
	if home == "" {
		home = "."
	}
	baseDir := filepath.Dir(path)
	var logDir string
	switch runtime.GOOS {
	case "windows":
		localApp := os.Getenv("LOCALAPPDATA")
		if localApp != "" {
			logDir = filepath.Join(filepath.Dir(localApp), "LocalLow", "VRChat", "VRChat")
		} else {
			logDir = filepath.Join(home, "AppData", "LocalLow", "VRChat", "VRChat")
		}
	case "darwin":
		logDir = filepath.Join(home, "Library", "Logs", "VRChat")
	default:
		logDir = filepath.Join(home, ".config", "unity3d", "VRChat", "VRChat")
	}
	var cfg guiConfig
	cfg.Version = "1"
	cfg.Monitor.PollIntervalSec = 15
	cfg.Monitor.LogDir = logDir
	cfg.Monitor.FileGlob = "output_log_*.txt"
	cfg.Monitor.CheckExisting = true
	cfg.State.Path = filepath.Join(baseDir, "state.json")
	cfg.State.SaveIntervalSec = 10
	cfg.Notify.Discord.Enabled = false
	cfg.Notify.Discord.WebhookURL = ""
	cfg.Notify.Discord.GroupWebhooks = map[string]string{}
	cfg.Notify.Discord.Groups = []string{"info", "error"}
	cfg.Notify.Discord.Username = "VRC LogHook"
	cfg.Notify.Discord.MaxContentRune = 1600
	cfg.Notify.Discord.MinIntervalSec = 5
	cfg.Notify.Local.Path = filepath.Join(baseDir, "events.log")
	cfg.Notify.Retry.MaxAttempts = 3
	cfg.Notify.Retry.InitialBackoffMs = 500
	cfg.Notify.Retry.MaxBackoffMs = 5000
	cfg.Match.Rules = defaultRules()
	cfg.Match.DedupeWindowSec = 30
	cfg.Hooks.Enabled = false
	cfg.Hooks.UnsafeConsent = false
	cfg.Hooks.MaxConcurrency = 1
	cfg.Hooks.TimeoutSec = 5
	cfg.Hooks.Commands = defaultHookCommands()
	cfg.Runtime.DryRun = false
	cfg.Runtime.HotReload = true
	cfg.Runtime.ConfigReloadSec = 2
	cfg.Observability.SelfLogPath = filepath.Join(baseDir, "self.log")
	cfg.Observability.StatusLogSec = 30
	cfg.Observability.LogLevel = "info"
	cfg.Observability.Stdout = true
	return cfg
}

func saveConfig(path string, cfg guiConfig) error {
	baseDir := filepath.Dir(path)
	if strings.TrimSpace(cfg.Version) == "" {
		cfg.Version = "1"
	}
	if strings.TrimSpace(cfg.State.Path) == "" {
		cfg.State.Path = filepath.Join(baseDir, "state.json")
	}
	if cfg.State.SaveIntervalSec == 0 {
		cfg.State.SaveIntervalSec = 10
	}
	if strings.TrimSpace(cfg.Notify.Local.Path) == "" {
		cfg.Notify.Local.Path = filepath.Join(baseDir, "events.log")
	}
	if cfg.Notify.Discord.MaxContentRune == 0 {
		cfg.Notify.Discord.MaxContentRune = 1600
	}
	if cfg.Notify.Retry.MaxAttempts == 0 {
		cfg.Notify.Retry.MaxAttempts = 3
	}
	if cfg.Notify.Retry.InitialBackoffMs == 0 {
		cfg.Notify.Retry.InitialBackoffMs = 500
	}
	if cfg.Notify.Retry.MaxBackoffMs == 0 {
		cfg.Notify.Retry.MaxBackoffMs = 5000
	}
	if cfg.Match.DedupeWindowSec == 0 {
		cfg.Match.DedupeWindowSec = 30
	}
	normalizeGroupsAndRuleGroups(&cfg)
	if len(cfg.Match.Rules) == 0 {
		cfg.Match.Rules = defaultRules()
	}
	if cfg.Runtime.ConfigReloadSec == 0 {
		cfg.Runtime.ConfigReloadSec = 2
	}
	if cfg.Observability.StatusLogSec == 0 {
		cfg.Observability.StatusLogSec = 30
	}
	if cfg.Hooks.MaxConcurrency == 0 {
		cfg.Hooks.MaxConcurrency = 1
	}
	if cfg.Hooks.TimeoutSec == 0 {
		cfg.Hooks.TimeoutSec = 5
	}
	if strings.TrimSpace(cfg.Observability.SelfLogPath) == "" {
		cfg.Observability.SelfLogPath = filepath.Join(baseDir, "self.log")
	}
	if cfg.Monitor.PollIntervalSec < 1 || cfg.Monitor.PollIntervalSec > 60 {
		return errors.New("monitor.poll_interval_sec must be 1..60")
	}
	if cfg.Monitor.LogDir == "" || cfg.Monitor.FileGlob == "" {
		return errors.New("monitor.log_dir/file_glob is required")
	}
	if cfg.State.Path == "" || cfg.Notify.Local.Path == "" {
		return errors.New("state.path/notify.local.path is required")
	}
	if cfg.Notify.Discord.MaxContentRune < 100 || cfg.Notify.Discord.MaxContentRune > 1900 {
		return errors.New("notify.discord.max_content_rune must be 100..1900")
	}
	if cfg.Notify.Discord.MinIntervalSec < 0 || cfg.Notify.Discord.MinIntervalSec > 300 {
		return errors.New("notify.discord.min_interval_sec must be 0..300")
	}
	if cfg.Notify.Retry.MaxAttempts < 1 || cfg.Notify.Retry.MaxAttempts > 10 {
		return errors.New("notify.retry.max_attempts must be 1..10")
	}
	if cfg.Notify.Retry.InitialBackoffMs < 10 || cfg.Notify.Retry.MaxBackoffMs < cfg.Notify.Retry.InitialBackoffMs {
		return errors.New("notify.retry backoff range is invalid")
	}
	if cfg.Match.DedupeWindowSec < 0 || cfg.Match.DedupeWindowSec > 3600 {
		return errors.New("match.dedupe_window_sec must be 0..3600")
	}
	if len(cfg.Match.Rules) == 0 {
		return errors.New("match.rules must not be empty")
	}
	if len(cfg.Notify.Discord.Groups) == 0 {
		return errors.New("notify.discord.groups must not be empty")
	}
	seenGroups := map[string]struct{}{}
	for i, g := range cfg.Notify.Discord.Groups {
		g = strings.TrimSpace(g)
		if g == "" {
			return fmt.Errorf("notify.discord.groups[%d] is empty", i)
		}
		if _, ok := seenGroups[g]; ok {
			return fmt.Errorf("notify.discord.groups[%d] duplicates %q", i, g)
		}
		seenGroups[g] = struct{}{}
	}
	for i, rule := range cfg.Match.Rules {
		group := strings.TrimSpace(rule.Group)
		if group == "" {
			return fmt.Errorf("match.rules[%d].group is empty", i)
		}
		if _, ok := seenGroups[group]; !ok {
			return fmt.Errorf("match.rules[%d].group %q is not defined", i, rule.Group)
		}
	}
	if cfg.Hooks.MaxConcurrency < 1 || cfg.Hooks.MaxConcurrency > 16 {
		return errors.New("hooks.max_concurrency must be 1..16")
	}
	if cfg.Hooks.TimeoutSec < 1 || cfg.Hooks.TimeoutSec > 120 {
		return errors.New("hooks.timeout_sec must be 1..120")
	}
	if cfg.Runtime.ConfigReloadSec < 1 || cfg.Runtime.ConfigReloadSec > 300 {
		return errors.New("runtime.config_reload_sec must be 1..300")
	}
	if cfg.Observability.SelfLogPath == "" {
		return errors.New("observability.self_log_path is required")
	}
	if cfg.Observability.StatusLogSec < 1 || cfg.Observability.StatusLogSec > 3600 {
		return errors.New("observability.status_log_sec must be 1..3600")
	}
	if cfg.Observability.LogLevel == "" {
		cfg.Observability.LogLevel = "info"
	}
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return err
	}
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	out = append([]byte(guiConfigCommentHeader), out...)
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

func openDirectory(path string) error {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("explorer.exe", path).Start()
	case "darwin":
		return exec.Command("open", path).Start()
	default:
		return exec.Command("xdg-open", path).Start()
	}
}

func runtimeTokenPath(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), "ipc.token")
}

func resolveIPCToken(configPath, fallback string) string {
	b, err := os.ReadFile(runtimeTokenPath(configPath))
	if err == nil {
		if tok := strings.TrimSpace(string(b)); tok != "" {
			return tok
		}
	}
	return strings.TrimSpace(fallback)
}

func guiIdentity() string {
	v := strings.TrimSpace(version)
	if v == "" {
		v = "dev"
	}
	return "vrc-loghook-gui/" + v
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

func parseRulesJSON(raw string) ([]guiRule, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("empty")
	}
	var rawArr []map[string]any
	_ = json.Unmarshal([]byte(raw), &rawArr)
	var out []guiRule
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, err
	}
	for i := range out {
		if i >= len(rawArr) {
			out[i].Enabled = true
			if strings.TrimSpace(out[i].Group) == "" {
				out[i].Group = defaultRuleGroupGUI(out[i].Name)
			}
			continue
		}
		if _, ok := rawArr[i]["enabled"]; !ok {
			out[i].Enabled = true
		}
		if _, ok := rawArr[i]["group"]; !ok || strings.TrimSpace(out[i].Group) == "" {
			out[i].Group = defaultRuleGroupGUI(out[i].Name)
		}
	}
	return out, nil
}

func parseHookCommandsJSON(raw string) ([]guiHookCommand, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return []guiHookCommand{}, nil
	}
	var out []guiHookCommand
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, err
	}
	return out, nil
}

func parseStringMapJSON(raw string) (map[string]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return map[string]string{}, nil
	}
	var out map[string]string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = map[string]string{}
	}
	return out, nil
}

func parseStringArrayJSON(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return []string{}, nil
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []string{}
	}
	return out, nil
}

func parseRulesFromForm(form url.Values) ([]guiRule, bool) {
	count := mustInt(form.Get("rule_count"), -1)
	if count < 0 {
		return nil, false
	}
	out := make([]guiRule, 0, count)
	for i := 0; i < count; i++ {
		prefix := "rule_" + strconv.Itoa(i) + "_"
		del := strings.TrimSpace(form.Get(prefix + "delete"))
		if del == "on" || del == "1" || strings.EqualFold(del, "true") {
			continue
		}
		r := guiRule{
			Enabled:         form.Get(prefix+"enabled") == "on",
			Name:            strings.TrimSpace(form.Get(prefix + "name")),
			Group:           strings.TrimSpace(form.Get(prefix + "group")),
			Contains:        strings.TrimSpace(form.Get(prefix + "contains")),
			Regex:           strings.TrimSpace(form.Get(prefix + "regex")),
			CaseSensitive:   form.Get(prefix+"case_sensitive") == "on",
			MessageTemplate: strings.TrimSpace(form.Get(prefix + "message_template")),
		}
		if r.Name == "" && r.Contains == "" && r.Regex == "" && r.MessageTemplate == "" {
			continue
		}
		if r.Group == "" {
			r.Group = defaultRuleGroupGUI(r.Name)
		}
		out = append(out, r)
	}
	return out, true
}

func prettyJSON(v any) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "[]"
	}
	return string(b)
}

func jsonOrEmpty(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "[]"
	}
	return string(b)
}

const guiConfigCommentHeader = `// VRC LogHook configuration (edited by GUI)
// Detailed field descriptions are shown next to each field in GUI.
// Placeholders for match.rules[*].message_template: {rule} {line} {file} {at}
// Runtime IPC auth token is regenerated per daemon start and stored in <config-dir>/ipc.token.

`

func defaultRules() []guiRule {
	return []guiRule{
		{
			Enabled:         true,
			Name:            "player-joined",
			Group:           "info",
			Contains:        "",
			Regex:           `(?i)OnPlayer(Joined|EnteredRoom)\b`,
			CaseSensitive:   false,
			MessageTemplate: "[join] {line}",
		},
		{
			Enabled:         true,
			Name:            "player-left",
			Group:           "info",
			Contains:        "",
			Regex:           `(?i)OnPlayerLeft\s`,
			CaseSensitive:   false,
			MessageTemplate: "[left] {line}",
		},
		{
			Enabled:         true,
			Name:            "runtime-exception",
			Group:           "error",
			Contains:        "Exception",
			Regex:           "",
			CaseSensitive:   false,
			MessageTemplate: "[error] {line}",
		},
	}
}

func defaultHookCommands() []guiHookCommand {
	return []guiHookCommand{}
}

func defaultRuleGroupGUI(name string) string {
	switch name {
	case "runtime-exception":
		return "error"
	case "player-joined", "player-left":
		return "info"
	default:
		return "info"
	}
}

func normalizeGroupsAndRuleGroups(cfg *guiConfig) {
	if cfg.Notify.Discord.GroupWebhooks == nil {
		cfg.Notify.Discord.GroupWebhooks = map[string]string{}
	}
	groups := make([]string, 0, len(cfg.Notify.Discord.Groups)+len(cfg.Match.Rules)+2)
	addGroup := func(g string) {
		g = strings.TrimSpace(g)
		if g == "" {
			return
		}
		for _, ex := range groups {
			if ex == g {
				return
			}
		}
		groups = append(groups, g)
	}
	for _, g := range cfg.Notify.Discord.Groups {
		addGroup(g)
	}
	if len(groups) == 0 {
		addGroup("info")
		addGroup("error")
	}
	for i := range cfg.Match.Rules {
		if strings.TrimSpace(cfg.Match.Rules[i].Group) == "" {
			cfg.Match.Rules[i].Group = defaultRuleGroupGUI(cfg.Match.Rules[i].Name)
		}
		addGroup(cfg.Match.Rules[i].Group)
	}
	cfg.Notify.Discord.Groups = groups
	filteredHooks := map[string]string{}
	for _, g := range cfg.Notify.Discord.Groups {
		if u := strings.TrimSpace(cfg.Notify.Discord.GroupWebhooks[g]); u != "" {
			filteredHooks[g] = u
		}
	}
	cfg.Notify.Discord.GroupWebhooks = filteredHooks
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
    input[type=text], input[type=number], select, textarea { width:100%; box-sizing:border-box; padding:8px; border:1px solid var(--line); border-radius:8px; }
    .inline { display:flex; gap:8px; align-items:center; flex-wrap:wrap;}
    button { border:none; border-radius:8px; padding:8px 12px; background:var(--accent); color:white; cursor:pointer; }
    button.secondary { background:#55617e; }
    code { font-size:12px; background:#f1f4fb; padding:2px 4px; border-radius:4px;}
    .star::after { content:"*"; color:#c53939; margin-left:4px; }
    details { margin-top:10px; padding:8px; border:1px dashed var(--line); border-radius:8px; }
    .warn { color:#a54700; }
    .priority-tabs { margin:10px 0 14px; }
    .priority-tabs > input[type=radio] { display:none; }
    .tabs { display:flex; gap:0; flex-wrap:wrap; margin:0; border-bottom:1px solid #ccd7f0; }
    .tab-label { display:inline-block; padding:9px 14px; margin:0 4px -1px 0; border:1px solid #ccd7f0; border-bottom:none; border-top-left-radius:10px; border-top-right-radius:10px; background:#edf2ff; color:#20304d; cursor:pointer; user-select:none; font-size:13px; font-weight:600; }
    #prio-tab-top:checked ~ .tabs label[for=prio-tab-top],
    #prio-tab-high:checked ~ .tabs label[for=prio-tab-high],
    #prio-tab-mid:checked ~ .tabs label[for=prio-tab-mid],
    #prio-tab-low:checked ~ .tabs label[for=prio-tab-low],
    #prio-tab-adv:checked ~ .tabs label[for=prio-tab-adv] { background:#fff; color:var(--accent); border-color:#9bb8ef; box-shadow:0 -2px 0 var(--accent) inset; }
    .tab-panel { display:none; }
    #prio-tab-top:checked ~ .tab-panels #tab-top { display:block; }
    #prio-tab-high:checked ~ .tab-panels #tab-high { display:block; }
    #prio-tab-mid:checked ~ .tab-panels #tab-mid { display:block; }
    #prio-tab-low:checked ~ .tab-panels #tab-low { display:block; }
    #prio-tab-adv:checked ~ .tab-panels #tab-adv { display:block; }
    .tab-panels { border:1px solid #ccd7f0; border-top:none; padding:12px; border-bottom-left-radius:10px; border-bottom-right-radius:10px; background:#fff; }
    .rule-summary-line { display:inline-flex; align-items:center; gap:8px; vertical-align:middle; }
    .rule-summary-line input[type=checkbox] { margin:0; }
    .rule-summary-line .rule-summary-text { white-space:nowrap; overflow:hidden; text-overflow:ellipsis; }
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

    <form id="config-form" method="post" action="/save" class="card" onsubmit="saveScrollY(); return (typeof prepareHookJSON==='function') ? prepareHookJSON() : true;">
      <h2>設定編集（優先度順）</h2>
      <div class="priority-tabs">
      <input type="radio" name="priority-tab" id="prio-tab-top" checked onchange="if (typeof sendGuiLog==='function') sendGuiLog('info','tab changed: top')">
      <input type="radio" name="priority-tab" id="prio-tab-high" onchange="if (typeof sendGuiLog==='function') sendGuiLog('info','tab changed: high')">
      <input type="radio" name="priority-tab" id="prio-tab-mid" onchange="if (typeof sendGuiLog==='function') sendGuiLog('info','tab changed: mid')">
      <input type="radio" name="priority-tab" id="prio-tab-low" onchange="if (typeof sendGuiLog==='function') sendGuiLog('info','tab changed: low')">
      <input type="radio" name="priority-tab" id="prio-tab-adv" onchange="if (typeof sendGuiLog==='function') sendGuiLog('info','tab changed: adv')">
      <div class="tabs">
        <label class="tab-label" for="prio-tab-top">最優先</label>
        <label class="tab-label" for="prio-tab-high">高優先</label>
        <label class="tab-label" for="prio-tab-mid">中優先</label>
        <label class="tab-label" for="prio-tab-low">低優先</label>
        <label class="tab-label" for="prio-tab-adv">上級者向け</label>
      </div>
      <div class="tab-panels">

      <div id="tab-top" class="tab-panel">
      <h3>最優先</h3>
      <div class="inline">
        <label><input type="checkbox" name="notify.discord.enabled" {{if .Config.Notify.Discord.Enabled}}checked{{end}}> notify.discord.enabled</label>
        <label><input type="checkbox" name="runtime.dry_run" {{if .Config.Runtime.DryRun}}checked{{end}}> runtime.dry_run</label>
      </div>
      <div class="muted">notify.discord.enabled: Discord送信の有効化。runtime.dry_run: 外部送信/Hook停止。</div>
      <label>monitor.log_dir</label>
      <input type="text" name="monitor.log_dir" value="{{.Config.Monitor.LogDir}}">
      <div class="muted">VRChatログのディレクトリパス。</div>
      <label>notify.discord.webhook_url</label>
      <input type="text" name="notify.discord.webhook_url" value="{{.Config.Notify.Discord.WebhookURL}}">
      <div class="muted">デフォルトのDiscord Webhook URL。グループ専用Webhook未設定時に使用します。</div>
      </div>

      <div id="tab-high" class="tab-panel">
      <h3>高優先</h3>
      <label>notify.discord.username</label>
      <input type="text" name="notify.discord.username" value="{{.Config.Notify.Discord.Username}}">
      <div class="muted">Discordに表示する送信者名。</div>
      <label>notify.discord.groups（GUI編集）</label>
      <div id="group-list">
        {{range $i, $g := .Config.Notify.Discord.Groups}}
          <div style="border:1px solid #d7dbea;border-radius:8px;padding:10px;margin-bottom:8px;">
            <div class="inline" style="justify-content:space-between;">
              <strong>Group {{$i}}</strong>
              <button type="button" class="secondary" onclick="removeGroup({{$i}})">削除</button>
            </div>
            <label>group name</label>
            <input type="text" value="{{$g}}" oninput="setGroupName({{$i}}, this.value)">
            <div class="muted">ルールが参照するグループ名です（空白不可）。</div>
            <label>webhook_url (optional)</label>
            <input type="text" value="{{index $.Config.Notify.Discord.GroupWebhooks $g}}" oninput="setGroupWebhook({{$i}}, this.value)">
            <div class="muted">このグループ専用Webhook。空欄なら notify.discord.webhook_url を使用します。</div>
          </div>
        {{end}}
      </div>
      <input type="hidden" id="discord-groups-json" name="notify.discord.groups_json" value="{{.GroupsJSON}}">
      <input type="hidden" id="discord-group-webhooks-json" name="notify.discord.group_webhooks_json" value="{{.GroupWebhooksJSON}}">
      <div class="inline" style="margin-top:8px;">
        <button type="button" onclick="addGroup()">グループ追加</button>
      </div>
      <div class="muted">各グループにWebhook URLを設定できます。空欄の場合は <code>notify.discord.webhook_url</code> を使用します。</div>

      <details open id="match-rules-section">
        <summary>高優先: match.rules（GUI編集）</summary>
        <p class="muted">ルールはここで管理します。contains または regex のどちらかは必須です。</p>
        <div id="rule-list">
          {{if .Config.Match.Rules}}
            {{range $i, $r := .Config.Match.Rules}}
              <div style="border:1px solid #d7dbea;border-radius:8px;padding:10px;margin-bottom:8px;">
                <details>
                  <summary>
                    <span class="rule-summary-line">
                      <input type="checkbox" name="rule_{{$i}}_enabled" {{if $r.Enabled}}checked{{end}} onclick="event.stopPropagation()">
                      <span class="rule-summary-text"><strong>{{$r.Name}}</strong> / group=<code>{{$r.Group}}</code> / contains=<code>{{$r.Contains}}</code> / regex=<code>{{$r.Regex}}</code></span>
                    </span>
                  </summary>
                  <div class="muted">enabled: true のときこのルールを評価します。false なら無効化されます。</div>
                  <label>name</label>
                  <input type="text" name="rule_{{$i}}_name" value="{{$r.Name}}">
                  <div class="muted">ルール名。通知やログで識別するための名前です。</div>
                  <label>group</label>
                  <select name="rule_{{$i}}_group" class="rule-group-select">
                    {{range $g := $.Config.Notify.Discord.Groups}}
                      <option value="{{$g}}" {{if eq $r.Group $g}}selected{{end}}>{{$g}}</option>
                    {{end}}
                  </select>
                  <div class="muted">ルールの通知グループ。上で定義したグループから選択します。</div>
                  <label>contains</label>
                  <input type="text" name="rule_{{$i}}_contains" value="{{$r.Contains}}">
                  <div class="muted">この文字列を含む行を一致対象にします（部分一致）。</div>
                  <label>regex</label>
                  <input type="text" name="rule_{{$i}}_regex" value="{{$r.Regex}}">
                  <div class="muted">正規表現で一致判定します。contains と regex のどちらかは必須です。</div>
                  <label>message_template</label>
                  <input type="text" name="rule_{{$i}}_message_template" value="{{$r.MessageTemplate}}">
                  <div class="muted">通知本文テンプレート。例: <code>[{rule}] {line}</code>。利用可能: <code>{rule}</code> <code>{line}</code> <code>{file}</code> <code>{at}</code></div>
                  <label><input type="checkbox" name="rule_{{$i}}_case_sensitive" {{if $r.CaseSensitive}}checked{{end}}> case_sensitive</label>
                  <div class="muted">true で大文字小文字を区別して一致判定します。</div>
                  <button type="submit" class="secondary" formaction="/action?rule_delete_index={{$i}}#match-rules-section" formmethod="post" name="action" value="delete-rule" onclick="saveScrollY(); return confirm('このルールを削除します。よろしいですか？');">このルールを削除</button>
                </details>
              </div>
            {{end}}
          {{else}}
            <div class="muted">ルールがありません。ルール追加で作成できます。</div>
          {{end}}
        </div>
        <input type="hidden" id="rule-count" name="rule_count" value="{{len .Config.Match.Rules}}">
        <input type="hidden" id="match-rules-json" name="match.rules_json" value="">
        <div class="inline" style="margin-top:8px;">
          <button type="submit" class="secondary" formaction="/action#match-rules-section" formmethod="post" name="action" value="add-rule" onclick="saveScrollY()">ルール追加</button>
          <button type="submit" class="secondary" formaction="/action#match-rules-section" formmethod="post" name="action" value="reset-rules-defaults" onclick="saveScrollY(); return confirm('ルールを初期値に戻します。よろしいですか？');">初期値に戻す</button>
        </div>
      </details>

      <label>notify.discord.max_content_rune</label>
      <input type="number" min="100" max="1900" name="notify.discord.max_content_rune" value="{{.Config.Notify.Discord.MaxContentRune}}">
      <div class="muted">通知本文の最大文字数。超過分は切り詰め。</div>
      <label>notify.discord.min_interval_sec</label>
      <input type="number" min="0" max="300" name="notify.discord.min_interval_sec" value="{{.Config.Notify.Discord.MinIntervalSec}}">
      <div class="muted">Discord通知の最小送信間隔(秒)。期間内イベントは1件にまとめて送信。0で無効。</div>
      <label>match.dedupe_window_sec</label>
      <input type="number" min="0" max="3600" name="match.dedupe_window_sec" value="{{.Config.Match.DedupeWindowSec}}">
      <div class="muted">同一イベントの重複通知抑制時間(秒)。0で無効。</div>
      </div>

      <div id="tab-mid" class="tab-panel">
      <h3>中優先</h3>
      <label>monitor.poll_interval_sec</label>
      <input type="number" min="1" max="60" name="monitor.poll_interval_sec" value="{{.Config.Monitor.PollIntervalSec}}">
      <div class="muted">ログ追跡の間隔(秒)。短いほど検知が早く、負荷は増えます。</div>
      <label>runtime.config_reload_sec</label>
      <input type="number" min="1" max="300" name="runtime.config_reload_sec" value="{{.Config.Runtime.ConfigReloadSec}}">
      <div class="muted">設定ホットリロード間隔(秒)。</div>
      <div class="inline">
        <label><input type="checkbox" name="runtime.hot_reload" {{if .Config.Runtime.HotReload}}checked{{end}}> runtime.hot_reload</label>
        <label><input type="checkbox" name="observability.stdout" {{if .Config.Observability.Stdout}}checked{{end}}> observability.stdout</label>
      </div>
      <div class="muted">runtime.hot_reload: 設定自動再読込。observability.stdout: 標準出力にも自己ログを出力。</div>
      <label>notify.retry.max_attempts</label>
      <input type="number" min="1" max="10" name="notify.retry.max_attempts" value="{{.Config.Notify.Retry.MaxAttempts}}">
      <div class="muted">送信失敗時の最大再試行回数。</div>
      <label>notify.retry.initial_backoff_ms</label>
      <input type="number" min="10" max="60000" name="notify.retry.initial_backoff_ms" value="{{.Config.Notify.Retry.InitialBackoffMs}}">
      <div class="muted">最初の待機時間(ミリ秒)。</div>
      <label>notify.retry.max_backoff_ms</label>
      <input type="number" min="10" max="60000" name="notify.retry.max_backoff_ms" value="{{.Config.Notify.Retry.MaxBackoffMs}}">
      <div class="muted">再試行待機時間の上限(ミリ秒)。</div>
      <label>observability.log_level</label>
      <select name="observability.log_level">
        <option value="debug" {{if eq .Config.Observability.LogLevel "debug"}}selected{{end}}>debug</option>
        <option value="info" {{if eq .Config.Observability.LogLevel "info"}}selected{{end}}>info</option>
        <option value="warn" {{if eq .Config.Observability.LogLevel "warn"}}selected{{end}}>warn</option>
        <option value="error" {{if eq .Config.Observability.LogLevel "error"}}selected{{end}}>error</option>
      </select>
      </div>

      <div id="tab-low" class="tab-panel">
      <h3>低優先</h3>
      <label>monitor.file_glob</label>
      <input type="text" name="monitor.file_glob" value="{{.Config.Monitor.FileGlob}}">
      <div class="muted">監視対象ファイル名パターン。通常は <code>output_log_*.txt</code>。</div>
      <label><input type="checkbox" name="monitor.check_existing_on_first_run" {{if .Config.Monitor.CheckExisting}}checked{{end}}> monitor.check_existing_on_first_run</label>
      <div class="muted">初回起動時に既存行も評価します。false なら追記行のみ。</div>
      <label>state.save_interval_sec</label>
      <input type="number" min="1" max="3600" name="state.save_interval_sec" value="{{.Config.State.SaveIntervalSec}}">
      <div class="muted">状態保存の間隔(秒)。</div>
      <label>observability.status_log_sec</label>
      <input type="number" min="1" max="3600" name="observability.status_log_sec" value="{{.Config.Observability.StatusLogSec}}">
      <div class="muted">ステータス出力間隔(秒)。</div>
      <label>notify.local.path</label>
      <input type="text" name="notify.local.path" value="{{.Config.Notify.Local.Path}}">
      <div class="muted">ローカル通知ログ(JSONL)の保存先。</div>
      </div>

      <div id="tab-adv" class="tab-panel">
      <h3>上級者向け（通常は変更不要）</h3>
      <label class="star">token</label>
      <input type="text" name="token" value="{{.Config.Token}}" readonly>
      <div class="muted">互換用設定値。実運用のIPC認証は <code>ipc.token</code>（起動ごと再生成）を使用します。</div>
      <label>state.path</label>
      <input type="text" name="state.path" value="{{.Config.State.Path}}">
      <div class="muted">オフセット等の状態保存先ファイル。</div>
      <label>observability.self_log_path</label>
      <input type="text" name="observability.self_log_path" value="{{.Config.Observability.SelfLogPath}}">
      <div class="muted">本ツール自身のログ出力先。</div>
      <div class="muted"><span class="star"></span> は表示のみ（再生成前提）です。token/versionはGUIから直接編集しません。IPC実行時は <code>ipc.token</code> を優先します。</div>
      <details>
        <summary>上級者向け: Hook設定（危険性あり）</summary>
        <p class="warn">Hookは任意コマンドを実行できます。危険性を理解した場合のみ有効化してください。</p>
        <div class="inline">
          <label><input type="checkbox" name="hooks.enabled" {{if .Config.Hooks.Enabled}}checked{{end}}> hooks.enabled</label>
          <label><input type="checkbox" name="hooks.unsafe_consent" {{if .Config.Hooks.UnsafeConsent}}checked{{end}}> hooks.unsafe_consent</label>
        </div>
        <div class="muted">hooks.enabled: Hook実行を許可。hooks.unsafe_consent: 危険性を理解して有効化したことを明示。</div>
        <label>hooks.max_concurrency</label>
        <input type="number" min="1" max="16" name="hooks.max_concurrency" value="{{.Config.Hooks.MaxConcurrency}}">
        <div class="muted">同時実行するHookの最大数。</div>
        <label>hooks.timeout_sec</label>
        <input type="number" min="1" max="120" name="hooks.timeout_sec" value="{{.Config.Hooks.TimeoutSec}}">
        <div class="muted">1コマンドあたりの実行タイムアウト(秒)。</div>
        <label>hooks.commands（GUI編集）</label>
        <div class="muted">各Hookは <code>program + args</code> を分離して指定します。</div>
        <div id="hook-list"></div>
        <input type="hidden" id="hooks-commands-json" name="hooks.commands_json" value="{{.HooksJSON}}">
        <div class="inline" style="margin-top:8px;">
          <button type="button" onclick="addHook()">Hook追加</button>
          <button type="button" class="secondary" onclick="resetHooksToDefault()">初期値に戻す</button>
        </div>
      </details>
      <details>
        <summary>その他</summary>
        <p class="muted">補助的な操作です。</p>
        <div class="inline">
          <button type="submit" class="secondary" formaction="/action" formmethod="post" name="action" value="open-config-dir">設定ファイルのあるディレクトリを開く</button>
          <button type="submit" class="secondary" formaction="/action" formmethod="post" name="action" value="reset-defaults" onclick="return confirm('設定値を初期化します。よろしいですか？');">設定値を初期化</button>
        </div>
      </details>
      </div>
      </div>
      </div>
      <div style="margin-top:10px;">
        <button type="submit">保存して再読込</button>
      </div>
    </form>
  </div>
</body>
<script>
var defaultRules = {{.DefaultRules}};
if (!Array.isArray(defaultRules)) { defaultRules = []; }
var currentRules = {{.CurrentRules}};
if (!Array.isArray(currentRules)) { currentRules = []; }
if (currentRules.length === 0 && defaultRules.length > 0) {
  currentRules = JSON.parse(JSON.stringify(defaultRules));
}
var currentGroups = {{.CurrentGroups}};
if (!Array.isArray(currentGroups)) { currentGroups = []; }
if (currentGroups.length === 0) { currentGroups = ["info", "error"]; }
var currentGroupHooks = {{.CurrentGroupHooks}};
if (!currentGroupHooks || typeof currentGroupHooks !== "object" || Array.isArray(currentGroupHooks)) {
  currentGroupHooks = {};
}
var defaultHooks = {{.DefaultHooks}};
if (!Array.isArray(defaultHooks)) { defaultHooks = []; }
var currentHooks = {{.CurrentHooks}};
if (!Array.isArray(currentHooks)) { currentHooks = []; }

function esc(s){
  if (s === undefined || s === null) {
    s = "";
  }
  return String(s)
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");
}

function sendGuiLog(level, message) {
  const data = new URLSearchParams();
  data.set("level", String(level || "info"));
  data.set("message", String(message || ""));
  fetch("/gui-log", {
    method: "POST",
    headers: { "Content-Type": "application/x-www-form-urlencoded; charset=UTF-8" },
    body: data.toString()
  }).catch(function(){});
}

function saveScrollY() {
  try {
    sessionStorage.setItem("vrc-loghook-gui-scroll-y", String(window.scrollY || 0));
  } catch (_) {}
}

function restoreScrollY() {
  try {
    const yRaw = sessionStorage.getItem("vrc-loghook-gui-scroll-y");
    if (yRaw === null) {
      return;
    }
    const y = parseInt(yRaw, 10);
    if (!Number.isNaN(y) && y >= 0) {
      window.scrollTo(0, y);
      setTimeout(function () { window.scrollTo(0, y); }, 0);
      setTimeout(function () { window.scrollTo(0, y); }, 80);
    }
    sessionStorage.removeItem("vrc-loghook-gui-scroll-y");
  } catch (_) {}
}

window.addEventListener("error", function (ev) {
  const msg = "js error: " + (ev.message || "unknown") + " @ " + (ev.filename || "?") + ":" + (ev.lineno || 0);
  sendGuiLog("error", msg);
});

function setupPriorityTabs() {
  const tabs = [
    { id: "prio-tab-top", name: "top" },
    { id: "prio-tab-high", name: "high" },
    { id: "prio-tab-mid", name: "mid" },
    { id: "prio-tab-low", name: "low" },
    { id: "prio-tab-adv", name: "adv" }
  ];
  tabs.forEach(function (t) {
    const el = document.getElementById(t.id);
    if (!el) { return; }
    el.addEventListener("change", function () {
      if (!el.checked) { return; }
      try {
        sessionStorage.setItem("vrc-loghook-gui-active-tab", t.name);
      } catch (_) {}
      sendGuiLog("info", "tab changed: " + t.name);
    });
    const label = document.querySelector('label[for="' + t.id + '"]');
    if (label) {
      label.addEventListener("click", function () {
        setTimeout(function () {
          if (el.checked) {
            try {
              sessionStorage.setItem("vrc-loghook-gui-active-tab", t.name);
            } catch (_) {}
            sendGuiLog("info", "tab clicked: " + t.name);
          }
        }, 0);
      });
    }
  });

  let initial = "top";
  try {
    const saved = sessionStorage.getItem("vrc-loghook-gui-active-tab");
    if (saved) { initial = saved; }
  } catch (_) {}
  if (window.location && window.location.hash === "#match-rules-section") {
    initial = "top";
  }
  const target = document.getElementById("prio-tab-" + initial);
  if (target) { target.checked = true; }
  sendGuiLog("info", "tab init: " + initial);
}

function groupOptionsHTML(selected) {
  let html = "";
  const groups = Array.isArray(currentGroups) ? currentGroups : [];
  groups.forEach(function (g) {
    const sel = String(g) === String(selected) ? " selected" : "";
    html += '<option value="' + esc(g) + '"' + sel + '>' + esc(g) + '</option>';
  });
  return html;
}

function refreshRuleGroupSelectOptions() {
  const selects = document.querySelectorAll("select.rule-group-select");
  selects.forEach(function (sel) {
    const prev = sel.value;
    sel.innerHTML = groupOptionsHTML(prev);
    if (!sel.value && currentGroups.length > 0) {
      sel.value = currentGroups[0];
    }
  });
}

function renderGroups() {
  const root = document.getElementById("group-list");
  if (!root) { return; }
  root.innerHTML = "";
  if (!Array.isArray(currentGroups)) { currentGroups = []; }
  if (currentGroups.length === 0) {
    currentGroups = ["info"];
  }
  currentGroups.forEach(function (g, i) {
    const row = document.createElement("div");
    row.style.border = "1px solid #d7dbea";
    row.style.borderRadius = "8px";
    row.style.padding = "10px";
    row.style.marginBottom = "8px";
    row.innerHTML = [
      '<div class="inline" style="justify-content:space-between;">',
      '<strong>Group ' + (i + 1) + '</strong>',
      '<button type="button" class="secondary" onclick="removeGroup(' + i + ')">削除</button>',
      '</div>',
      '<label>group name</label>',
      '<input type="text" value="' + esc(g) + '" oninput="setGroupName(' + i + ', this.value)">',
      '<div class="muted">ルールが参照するグループ名です（空白不可）。</div>',
      '<label>webhook_url (optional)</label>',
      '<input type="text" value="' + esc(currentGroupHooks[g] || "") + '" oninput="setGroupWebhook(' + i + ', this.value)">',
      '<div class="muted">このグループ専用Webhook。空欄なら notify.discord.webhook_url を使用します。</div>'
    ].join("");
    root.appendChild(row);
  });
  refreshRuleGroupSelectOptions();
}

function setGroupName(i, raw) {
  const next = String(raw || "").trim();
  const prev = currentGroups[i] || "";
  if (next === prev) {
    return;
  }
  currentGroups[i] = next;
  if (prev && Object.prototype.hasOwnProperty.call(currentGroupHooks, prev)) {
    currentGroupHooks[next] = currentGroupHooks[prev];
    delete currentGroupHooks[prev];
  }
  if (Array.isArray(currentRules)) {
    currentRules.forEach(function (r) {
      if ((r.group || "") === prev) {
        r.group = next;
      }
    });
  }
  renderGroups();
}

function setGroupWebhook(i, raw) {
  const group = (currentGroups[i] || "").trim();
  if (!group) {
    return;
  }
  const val = String(raw || "").trim();
  if (val) {
    currentGroupHooks[group] = val;
  } else {
    delete currentGroupHooks[group];
  }
}

function addGroup() {
  let base = "group";
  let idx = 1;
  while (currentGroups.indexOf(base + idx) >= 0) {
    idx += 1;
  }
  currentGroups.push(base + idx);
  renderGroups();
}

function removeGroup(i) {
  if (currentGroups.length <= 1) {
    alert("最低1つのグループが必要です。");
    return;
  }
  const removed = currentGroups[i];
  currentGroups.splice(i, 1);
  delete currentGroupHooks[removed];
  const fallback = currentGroups[0] || "info";
  if (Array.isArray(currentRules)) {
    currentRules.forEach(function (r) {
      if ((r.group || "") === removed) {
        r.group = fallback;
      }
    });
  }
  renderGroups();
}

function prepareGroupJSON() {
  const cleaned = [];
  const seen = {};
  currentGroups.forEach(function (g) {
    g = String(g || "").trim();
    if (!g || seen[g]) { return; }
    seen[g] = true;
    cleaned.push(g);
  });
  if (cleaned.length === 0) {
    cleaned.push("info");
  }
  currentGroups = cleaned;
  const map = {};
  cleaned.forEach(function (g) {
    const v = String(currentGroupHooks[g] || "").trim();
    if (v) {
      map[g] = v;
    }
  });
  currentGroupHooks = map;
  const groupsEl = document.getElementById("discord-groups-json");
  const hooksEl = document.getElementById("discord-group-webhooks-json");
  if (groupsEl) {
    groupsEl.value = JSON.stringify(currentGroups);
  }
  if (hooksEl) {
    hooksEl.value = JSON.stringify(currentGroupHooks);
  }
}

function renderRules() {
  const root = document.getElementById("rule-list");
  root.innerHTML = "";
  const countEl = document.getElementById("rule-count");
  if (!Array.isArray(currentRules)) {
    currentRules = [];
    sendGuiLog("warn", "renderRules: currentRules was not array; reset to []");
  }
  if (currentRules.length === 0 && Array.isArray(defaultRules) && defaultRules.length > 0) {
    currentRules = JSON.parse(JSON.stringify(defaultRules));
    sendGuiLog("info", "renderRules: applied defaultRules fallback; count=" + currentRules.length);
  }
  if (currentRules.length === 0) {
    root.innerHTML = '<div class="muted">ルールがありません。ルール追加で作成できます。</div>';
    if (countEl) { countEl.value = "0"; }
    sendGuiLog("warn", "renderRules: no rules to render");
    return;
  }
  try {
    currentRules.forEach((r, i) => {
      const box = document.createElement("div");
      box.style.border = "1px solid #d7dbea";
      box.style.borderRadius = "8px";
      box.style.padding = "10px";
      box.style.marginBottom = "8px";
    box.innerHTML = [
      '<div class="inline" style="justify-content:space-between;">',
      '<strong>Rule ' + (i + 1) + '</strong>',
      '<button type="button" class="secondary" onclick="removeRule(' + i + ')">削除</button>',
      '</div>',
      '<label><input type="checkbox" name="rule_' + i + '_enabled" ' + (r.enabled ? 'checked' : '') + ' onchange="setRule(' + i + ', \\'enabled\\', this.checked)"> enabled</label>',
      '<div class="muted">true のときこのルールを評価します。false なら無効化されます。</div>',
      '<label>name</label>',
      '<input type="text" name="rule_' + i + '_name" value="' + esc(r.name) + '" oninput="setRule(' + i + ', \\'name\\', this.value)">',
      '<div class="muted">ルール名。通知やログで識別するための名前です。</div>',
      '<label>group</label>',
      '<select name="rule_' + i + '_group" class="rule-group-select" onchange="setRule(' + i + ', \\'group\\', this.value)">' + groupOptionsHTML(r.group || "info") + '</select>',
      '<div class="muted">ルールの通知グループ。上で定義したグループから選択します。</div>',
      '<label>contains</label>',
      '<input type="text" name="rule_' + i + '_contains" value="' + esc(r.contains) + '" oninput="setRule(' + i + ', \\'contains\\', this.value)">',
      '<div class="muted">この文字列を含む行を一致対象にします（部分一致）。</div>',
      '<label>regex</label>',
      '<input type="text" name="rule_' + i + '_regex" value="' + esc(r.regex) + '" oninput="setRule(' + i + ', \\'regex\\', this.value)">',
      '<div class="muted">正規表現で一致判定します。contains と regex のどちらかは必須です。</div>',
      '<label>message_template</label>',
      '<input type="text" name="rule_' + i + '_message_template" value="' + esc(r.message_template) + '" oninput="setRule(' + i + ', \\'message_template\\', this.value)">',
      '<div class="muted">通知本文テンプレート。例: <code>[{rule}] {line}</code>。利用可能: <code>{rule}</code> <code>{line}</code> <code>{file}</code> <code>{at}</code></div>',
      '<label><input type="checkbox" name="rule_' + i + '_case_sensitive" ' + (r.case_sensitive ? 'checked' : '') + ' onchange="setRule(' + i + ', \\'case_sensitive\\', this.checked)"> case_sensitive</label>',
      '<div class="muted">true で大文字小文字を区別して一致判定します。</div>',
      '<input type="hidden" id="rule_' + i + '_delete" name="rule_' + i + '_delete" value="0">',
      '<button type="button" class="secondary" onclick="markRuleDelete(' + i + ')">このルールを削除</button>'
    ].join('');
      root.appendChild(box);
    });
  } catch (e) {
    root.innerHTML = '<div class="warn">ルール描画に失敗しました。ログを確認してください。</div>';
    sendGuiLog("error", "renderRules exception: " + (e && e.message ? e.message : String(e)));
    return;
  }
  if (countEl) { countEl.value = String(currentRules.length); }
  sendGuiLog("info", "renderRules: rendered rule count=" + currentRules.length);
}

function setRule(i, key, value) {
  currentRules[i][key] = value;
}

function addRule() {
  sendGuiLog("info", "addRule: before=" + currentRules.length);
  currentRules.push({
    enabled: true,
    name: "new-rule",
    group: "info",
    contains: "",
    regex: "",
    case_sensitive: false,
    message_template: "[{rule}] {line}"
  });
  renderRules();
  sendGuiLog("info", "addRule: after=" + currentRules.length);
}

function removeRule(i) {
  currentRules.splice(i, 1);
  renderRules();
}

function markRuleDelete(i) {
  const hidden = document.getElementById("rule_" + i + "_delete");
  if (hidden) {
    hidden.value = "1";
  }
  const box = hidden ? hidden.closest("div[style]") : null;
  if (box) {
    box.style.opacity = "0.45";
  }
  sendGuiLog("info", "rule marked for delete: index=" + i);
}

function prepareRuleJSON() {
  document.getElementById("match-rules-json").value = JSON.stringify(currentRules);
  return true;
}

function renderHooks() {
  const root = document.getElementById("hook-list");
  root.innerHTML = "";
  if (!Array.isArray(currentHooks)) {
    currentHooks = [];
  }
  if (currentHooks.length === 0) {
    root.innerHTML = '<div class="muted">Hookコマンドは未設定です。</div>';
    return;
  }
  currentHooks.forEach((h, i) => {
    const box = document.createElement("div");
    box.style.border = "1px solid #d7dbea";
    box.style.borderRadius = "8px";
    box.style.padding = "10px";
    box.style.marginBottom = "8px";
    const argsText = Array.isArray(h.args) ? h.args.join(" ") : "";
    box.innerHTML = [
      '<div class="inline" style="justify-content:space-between;">',
      '<strong>Hook ' + (i + 1) + '</strong>',
      '<button type="button" class="secondary" onclick="removeHook(' + i + ')">削除</button>',
      '</div>',
      '<label>name</label>',
      '<input type="text" value="' + esc(h.name) + '" oninput="setHook(' + i + ', \\'name\\', this.value)">',
      '<label>program</label>',
      '<input type="text" value="' + esc(h.program) + '" oninput="setHook(' + i + ', \\'program\\', this.value)">',
      '<label>args (space separated)</label>',
      '<input type="text" value="' + esc(argsText) + '" oninput="setHookArgs(' + i + ', this.value)">',
      '<label><input type="checkbox" ' + (h.enabled ? 'checked' : '') + ' onchange="setHook(' + i + ', \\'enabled\\', this.checked)"> enabled</label>'
    ].join('');
    root.appendChild(box);
  });
}

function setHook(i, key, value) {
  currentHooks[i][key] = value;
}

function setHookArgs(i, raw) {
  const parts = String(raw).trim().split(/\s+/).filter(Boolean);
  currentHooks[i].args = parts;
}

function addHook() {
  currentHooks.push({
    name: "new-hook",
    enabled: false,
    program: "",
    args: []
  });
  renderHooks();
}

function removeHook(i) {
  currentHooks.splice(i, 1);
  renderHooks();
}

function resetHooksToDefault() {
  currentHooks = JSON.parse(JSON.stringify(defaultHooks));
  renderHooks();
}

function prepareHookJSON() {
  prepareGroupJSON();
  document.getElementById("hooks-commands-json").value = JSON.stringify(currentHooks);
  return true;
}

try {
  document.addEventListener("submit", function () { saveScrollY(); }, true);
  window.addEventListener("beforeunload", function () { saveScrollY(); });
  setupPriorityTabs();
  restoreScrollY();
  sendGuiLog("info", "gui init pre-render: defaultRulesType=" + (Array.isArray(defaultRules) ? "array" : typeof defaultRules) + " currentRulesType=" + (Array.isArray(currentRules) ? "array" : typeof currentRules));
  renderGroups();
  renderHooks();
  sendGuiLog("info", "gui init done: defaultRules=" + defaultRules.length + " currentRules=" + currentRules.length + " currentHooks=" + currentHooks.length);
} catch (e) {
  sendGuiLog("error", "gui init exception: " + (e && e.message ? e.message : String(e)));
}
</script>
</html>
`
