package config

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

type Config struct {
	Version       string        `json:"version"`
	Token         string        `json:"token"`
	Monitor       MonitorConfig `json:"monitor"`
	State         StateConfig   `json:"state"`
	Notify        NotifyConfig  `json:"notify"`
	Match         MatchConfig   `json:"match"`
	Hooks         HookConfig    `json:"hooks"`
	Runtime       RuntimeConfig `json:"runtime"`
	Observability ObserveConfig `json:"observability"`
}

type MonitorConfig struct {
	PollIntervalSec         int    `json:"poll_interval_sec"`
	LogDir                  string `json:"log_dir"`
	FileGlob                string `json:"file_glob"`
	CheckExistingOnFirstRun bool   `json:"check_existing_on_first_run"`
}

type StateConfig struct {
	Path            string `json:"path"`
	SaveIntervalSec int    `json:"save_interval_sec"`
}

type NotifyConfig struct {
	Discord DiscordConfig `json:"discord"`
	Local   LocalConfig   `json:"local"`
	Retry   RetryConfig   `json:"retry"`
}

type DiscordConfig struct {
	Enabled        bool   `json:"enabled"`
	WebhookURL     string `json:"webhook_url"`
	Username       string `json:"username"`
	MaxContentRune int    `json:"max_content_rune"`
}

type LocalConfig struct {
	Path string `json:"path"`
}

type RetryConfig struct {
	MaxAttempts      int `json:"max_attempts"`
	InitialBackoffMs int `json:"initial_backoff_ms"`
	MaxBackoffMs     int `json:"max_backoff_ms"`
}

type MatchConfig struct {
	Rules           []Rule `json:"rules"`
	DedupeWindowSec int    `json:"dedupe_window_sec"`
}

type Rule struct {
	Name            string `json:"name"`
	Contains        string `json:"contains"`
	Regex           string `json:"regex"`
	CaseSensitive   bool   `json:"case_sensitive"`
	MessageTemplate string `json:"message_template"`
}

type HookConfig struct {
	Enabled        bool          `json:"enabled"`
	UnsafeConsent  bool          `json:"unsafe_consent"`
	MaxConcurrency int           `json:"max_concurrency"`
	TimeoutSec     int           `json:"timeout_sec"`
	Commands       []HookCommand `json:"commands"`
}

type HookCommand struct {
	Name    string   `json:"name"`
	Enabled bool     `json:"enabled"`
	Program string   `json:"program"`
	Args    []string `json:"args"`
}

type RuntimeConfig struct {
	DryRun          bool `json:"dry_run"`
	HotReload       bool `json:"hot_reload"`
	ConfigReloadSec int  `json:"config_reload_sec"`
}

type ObserveConfig struct {
	SelfLogPath  string `json:"self_log_path"`
	StatusLogSec int    `json:"status_log_sec"`
	LogLevel     string `json:"log_level"`
	Stdout       bool   `json:"stdout"`
}

func DefaultPath() string {
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

func Defaults() Config {
	home, _ := os.UserHomeDir()
	if home == "" {
		home = "."
	}
	baseDir := filepath.Dir(DefaultPath())
	return Config{
		Version: "1",
		Token:   randomToken(),
		Monitor: MonitorConfig{
			PollIntervalSec:         15,
			LogDir:                  defaultVRChatLogDir(home),
			FileGlob:                "output_log_*.txt",
			CheckExistingOnFirstRun: true,
		},
		State: StateConfig{
			Path:            filepath.Join(baseDir, "state.json"),
			SaveIntervalSec: 10,
		},
		Notify: NotifyConfig{
			Discord: DiscordConfig{
				Enabled:        false,
				WebhookURL:     "",
				Username:       "VRC LogHook",
				MaxContentRune: 1600,
			},
			Local: LocalConfig{Path: filepath.Join(baseDir, "events.log")},
			Retry: RetryConfig{MaxAttempts: 3, InitialBackoffMs: 500, MaxBackoffMs: 5000},
		},
		Match: MatchConfig{
			DedupeWindowSec: 30,
			Rules: []Rule{
				{
					Name:            "player-joined",
					Regex:           `(?i)OnPlayer(Joined|EnteredRoom)\b`,
					CaseSensitive:   false,
					MessageTemplate: "[join] {line}",
				},
				{
					Name:            "player-left",
					Regex:           `(?i)OnPlayerLeft(Room)?\b`,
					CaseSensitive:   false,
					MessageTemplate: "[left] {line}",
				},
				{
					Name:            "runtime-exception",
					Contains:        "Exception",
					CaseSensitive:   false,
					MessageTemplate: "[error] {line}",
				},
			},
		},
		Hooks: HookConfig{
			Enabled:        false,
			UnsafeConsent:  false,
			MaxConcurrency: 1,
			TimeoutSec:     5,
		},
		Runtime: RuntimeConfig{
			DryRun:          false,
			HotReload:       true,
			ConfigReloadSec: 2,
		},
		Observability: ObserveConfig{
			SelfLogPath:  filepath.Join(baseDir, "self.log"),
			StatusLogSec: 30,
			LogLevel:     "info",
			Stdout:       true,
		},
	}
}

func LoadOrCreate(path string) (Config, error) {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		cfg := Defaults()
		if err := Save(path, cfg); err != nil {
			return Config{}, err
		}
		return cfg, nil
	}
	return Load(path)
}

func Load(path string) (Config, error) {
	if err := validateFilePermission(path); err != nil {
		return Config{}, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	clean := sanitizeHJSONLike(b)
	var cfg Config
	dec := json.NewDecoder(bytes.NewReader(clean))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("config parse error: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != nil && !errors.Is(err, io.EOF) {
		return Config{}, errors.New("config parse error: multiple JSON values are not allowed")
	}
	if cfg.Version == "" {
		cfg.Version = "1"
	}
	if runtime.GOOS == "windows" {
		cfg.Monitor.LogDir = normalizeLegacyWindowsLogDir(cfg.Monitor.LogDir)
	}
	upgradeLegacyMatchRules(&cfg)
	if strings.TrimSpace(cfg.Observability.LogLevel) == "" {
		cfg.Observability.LogLevel = "info"
	}
	if err := Validate(cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func upgradeLegacyMatchRules(cfg *Config) {
	for i := range cfg.Match.Rules {
		r := &cfg.Match.Rules[i]
		if r.Regex != "" {
			continue
		}
		if r.Name == "player-joined" && strings.EqualFold(strings.TrimSpace(r.Contains), "OnPlayerJoined") {
			r.Contains = ""
			r.Regex = `(?i)OnPlayer(Joined|EnteredRoom)\b`
			r.CaseSensitive = false
		}
		if r.Name == "player-left" && strings.EqualFold(strings.TrimSpace(r.Contains), "OnPlayerLeft") {
			r.Contains = ""
			r.Regex = `(?i)OnPlayerLeft(Room)?\b`
			r.CaseSensitive = false
		}
		if strings.TrimSpace(r.MessageTemplate) == "" {
			r.MessageTemplate = defaultRuleTemplate(r.Name)
		}
	}
}

func defaultRuleTemplate(name string) string {
	switch name {
	case "player-joined":
		return "[join] {line}"
	case "player-left":
		return "[left] {line}"
	case "runtime-exception":
		return "[error] {line}"
	default:
		return "[{rule}] {line}"
	}
}

func Save(path string, cfg Config) error {
	if err := Validate(cfg); err != nil {
		return err
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

func Validate(cfg Config) error {
	if cfg.Monitor.PollIntervalSec < 1 || cfg.Monitor.PollIntervalSec > 60 {
		return errors.New("monitor.poll_interval_sec must be in [1,60]")
	}
	if cfg.Monitor.LogDir == "" {
		return errors.New("monitor.log_dir is required")
	}
	if cfg.Monitor.FileGlob == "" {
		return errors.New("monitor.file_glob is required")
	}
	if cfg.State.Path == "" {
		return errors.New("state.path is required")
	}
	if cfg.State.SaveIntervalSec < 1 {
		return errors.New("state.save_interval_sec must be >= 1")
	}
	if cfg.Notify.Local.Path == "" {
		return errors.New("notify.local.path is required")
	}
	if cfg.Notify.Discord.MaxContentRune < 100 || cfg.Notify.Discord.MaxContentRune > 1900 {
		return errors.New("notify.discord.max_content_rune must be in [100,1900]")
	}
	if cfg.Notify.Retry.MaxAttempts < 1 || cfg.Notify.Retry.MaxAttempts > 10 {
		return errors.New("notify.retry.max_attempts must be in [1,10]")
	}
	if cfg.Notify.Retry.InitialBackoffMs < 10 || cfg.Notify.Retry.MaxBackoffMs < cfg.Notify.Retry.InitialBackoffMs {
		return errors.New("notify.retry backoff range is invalid")
	}
	if len(cfg.Match.Rules) == 0 {
		return errors.New("match.rules must have at least one rule")
	}
	for i, r := range cfg.Match.Rules {
		if r.Name == "" {
			return fmt.Errorf("match.rules[%d].name is required", i)
		}
		if strings.TrimSpace(r.Contains) == "" && strings.TrimSpace(r.Regex) == "" {
			return fmt.Errorf("match.rules[%d] needs contains or regex", i)
		}
		if r.Regex != "" {
			if _, err := regexp.Compile(r.Regex); err != nil {
				return fmt.Errorf("match.rules[%d].regex invalid: %w", i, err)
			}
		}
	}
	if cfg.Match.DedupeWindowSec < 0 || cfg.Match.DedupeWindowSec > 3600 {
		return errors.New("match.dedupe_window_sec must be in [0,3600]")
	}
	if cfg.Hooks.MaxConcurrency < 1 || cfg.Hooks.MaxConcurrency > 16 {
		return errors.New("hooks.max_concurrency must be in [1,16]")
	}
	if cfg.Hooks.TimeoutSec < 1 || cfg.Hooks.TimeoutSec > 120 {
		return errors.New("hooks.timeout_sec must be in [1,120]")
	}
	if cfg.Runtime.ConfigReloadSec < 1 || cfg.Runtime.ConfigReloadSec > 300 {
		return errors.New("runtime.config_reload_sec must be in [1,300]")
	}
	if cfg.Observability.SelfLogPath == "" {
		return errors.New("observability.self_log_path is required")
	}
	if cfg.Observability.StatusLogSec < 1 || cfg.Observability.StatusLogSec > 3600 {
		return errors.New("observability.status_log_sec must be in [1,3600]")
	}
	switch strings.ToLower(cfg.Observability.LogLevel) {
	case "debug", "info", "warn", "error":
	default:
		return errors.New("observability.log_level must be one of: debug, info, warn, error")
	}
	return nil
}

func sanitizeHJSONLike(b []byte) []byte {
	clean := stripCommentsOutsideStrings(string(b))
	trailingComma := regexp.MustCompile(`,\s*([}\]])`)
	return trailingComma.ReplaceAll([]byte(clean), []byte(`$1`))
}

func MaskedWebhookURL(raw string) string {
	if raw == "" {
		return ""
	}
	if len(raw) <= 12 {
		return "***"
	}
	return raw[:8] + "..." + raw[len(raw)-4:]
}

func MaskedToken(raw string) string {
	if raw == "" {
		return ""
	}
	if len(raw) <= 12 {
		return "***"
	}
	return raw[:8] + "..." + raw[len(raw)-4:]
}

func randomToken() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("tok-%d", time.Now().UnixNano())
	}
	return "tok-" + hex.EncodeToString(buf[:])
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

func defaultVRChatLogDir(home string) string {
	switch runtime.GOOS {
	case "windows":
		localApp := os.Getenv("LOCALAPPDATA")
		if localApp != "" {
			return filepath.Join(filepath.Dir(localApp), "LocalLow", "VRChat", "VRChat")
		}
		return filepath.Join(home, "AppData", "LocalLow", "VRChat", "VRChat")
	case "darwin":
		return filepath.Join(home, "Library", "Logs", "VRChat")
	default:
		return filepath.Join(home, ".config", "unity3d", "VRChat", "VRChat")
	}
}

func normalizeLegacyWindowsLogDir(path string) string {
	p := strings.TrimSpace(path)
	if p == "" {
		return p
	}
	p = strings.NewReplacer("/", `\`).Replace(p)
	lower := strings.ToLower(p)
	needle := `\appdata\local\low\vrchat\vrchat`
	if i := strings.Index(lower, needle); i >= 0 {
		return p[:i] + `\AppData\LocalLow\VRChat\VRChat`
	}
	return p
}
