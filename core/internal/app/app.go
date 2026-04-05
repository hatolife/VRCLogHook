package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hatolife/VRCLogHook/core/internal/config"
	"github.com/hatolife/VRCLogHook/core/internal/hook"
	"github.com/hatolife/VRCLogHook/core/internal/ipc"
	"github.com/hatolife/VRCLogHook/core/internal/matcher"
	"github.com/hatolife/VRCLogHook/core/internal/monitor"
	"github.com/hatolife/VRCLogHook/core/internal/notify"
	"github.com/hatolife/VRCLogHook/core/internal/state"
)

type Service struct {
	configPath string
	ipcPath    string

	mu             sync.RWMutex
	cfg            config.Config
	rules          []matcher.CompiledRule
	tailer         *monitor.Tailer
	stateStore     *state.Store
	dispatcher     *notify.Dispatcher
	hooks          *hook.Runner
	logger         *log.Logger
	logLevel       int32
	lastEventAt    time.Time
	seen           map[string]time.Time
	initialized    bool
	lastNoFileWarn time.Time
}

func New(configPath, ipcPath string) (*Service, error) {
	cfg, err := config.LoadOrCreate(configPath)
	if err != nil {
		return nil, err
	}
	if fixedDir, ok := resolveMonitorLogDir(runtime.GOOS, cfg.Monitor.LogDir, pathExists); ok {
		cfg.Monitor.LogDir = fixedDir
	}
	if err := os.MkdirAll(filepath.Dir(cfg.Observability.SelfLogPath), 0o755); err != nil {
		return nil, err
	}
	logf, err := os.OpenFile(cfg.Observability.SelfLogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	var writer io.Writer = logf
	if cfg.Observability.Stdout {
		writer = io.MultiWriter(os.Stdout, logf)
	}
	logger := log.New(writer, "", log.LstdFlags|log.LUTC)

	rs, err := matcher.Compile(cfg.Match.Rules)
	if err != nil {
		return nil, err
	}
	st, err := state.Open(cfg.State.Path)
	if err != nil {
		return nil, err
	}
	t := monitor.New(cfg.Monitor.LogDir, cfg.Monitor.FileGlob)

	s := &Service{
		configPath: configPath,
		ipcPath:    ipcPath,
		cfg:        cfg,
		rules:      rs,
		tailer:     t,
		stateStore: st,
		dispatcher: notify.New(),
		hooks: hook.New(cfg.Hooks.MaxConcurrency, func(err error) {
			logger.Printf("hook error: %v", err)
		}),
		logger:         logger,
		seen:           map[string]time.Time{},
		lastEventAt:    time.Time{},
		lastNoFileWarn: time.Time{},
	}
	s.setLogLevel(cfg.Observability.LogLevel)
	s.logInfo("service init: config=%s state=%s log_dir=%s file_glob=%s dry_run=%v", s.configPath, cfg.State.Path, cfg.Monitor.LogDir, cfg.Monitor.FileGlob, cfg.Runtime.DryRun)
	s.logStartupProbe(cfg)
	s.logEffectiveNotificationAndRules(cfg)
	if cfg.Monitor.LogDir == "" || !pathExists(cfg.Monitor.LogDir) {
		s.logWarn("monitor directory does not exist: log_dir=%s", cfg.Monitor.LogDir)
	}
	s.logDebug("service init detail: %s", BuildSafeTokenLine(cfg))
	if err := notify.CurlPreflight(cfg); err != nil {
		s.logWarn("notification preflight: %v", err)
	}
	return s, nil
}

func (s *Service) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	server := ipc.NewServer(s.ipcPath, s.cfg.Token, ipc.Handlers{
		GetStatus: s.status,
		GetConfig: s.safeConfig,
		Reload:    s.reload,
		Stop:      cancel,
	})
	s.logInfo("ipc server start: path=%s", s.ipcPath)
	go func() {
		if err := server.Start(ctx); err != nil {
			s.logWarn("ipc server stopped: %v", err)
		}
	}()
	defer server.Close()

	s.mu.RLock()
	initialPollSec := s.cfg.Monitor.PollIntervalSec
	saveSec := s.cfg.State.SaveIntervalSec
	statusSec := s.cfg.Observability.StatusLogSec
	reloadSec := s.cfg.Runtime.ConfigReloadSec
	s.mu.RUnlock()

	pollTicker := time.NewTicker(time.Duration(initialPollSec) * time.Second)
	saveTicker := time.NewTicker(time.Duration(saveSec) * time.Second)
	statusTicker := time.NewTicker(time.Duration(statusSec) * time.Second)
	reloadTicker := time.NewTicker(time.Duration(reloadSec) * time.Second)
	currentPollSec := initialPollSec
	currentReloadSec := reloadSec
	currentStatusSec := statusSec
	currentSaveSec := saveSec
	defer pollTicker.Stop()
	defer saveTicker.Stop()
	defer statusTicker.Stop()
	defer reloadTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.logInfo("shutdown requested")
			_ = s.stateStore.Save()
			s.hooks.Wait()
			s.logInfo("shutdown completed")
			return nil
		case <-pollTicker.C:
			if err := s.pollOnce(ctx); err != nil {
				s.logError("poll error: %v", err)
			}
		case <-saveTicker.C:
			if err := s.stateStore.Save(); err != nil {
				s.logError("state save error: %v", err)
			} else {
				s.logDebug("state saved")
			}
		case <-statusTicker.C:
			b, _ := json.Marshal(s.status())
			s.logInfo("status=%s", string(b))
		case <-reloadTicker.C:
			s.mu.RLock()
			hotReload := s.cfg.Runtime.HotReload
			s.mu.RUnlock()
			if hotReload {
				if err := s.reload(); err != nil {
					s.logError("reload error: %v", err)
				}
			}

			// Keep ticker intervals aligned with runtime config changes.
			s.mu.RLock()
			nextPollSec := s.cfg.Monitor.PollIntervalSec
			nextReloadSec := s.cfg.Runtime.ConfigReloadSec
			nextStatusSec := s.cfg.Observability.StatusLogSec
			nextSaveSec := s.cfg.State.SaveIntervalSec
			s.mu.RUnlock()

			if nextPollSec != currentPollSec {
				pollTicker.Reset(time.Duration(nextPollSec) * time.Second)
				currentPollSec = nextPollSec
				s.logInfo("poll interval updated: %ds", currentPollSec)
			}
			if nextReloadSec != currentReloadSec {
				reloadTicker.Reset(time.Duration(nextReloadSec) * time.Second)
				currentReloadSec = nextReloadSec
				s.logInfo("reload interval updated: %ds", currentReloadSec)
			}
			if nextStatusSec != currentStatusSec {
				statusTicker.Reset(time.Duration(nextStatusSec) * time.Second)
				currentStatusSec = nextStatusSec
				s.logInfo("status interval updated: %ds", currentStatusSec)
			}
			if nextSaveSec != currentSaveSec {
				saveTicker.Reset(time.Duration(nextSaveSec) * time.Second)
				currentSaveSec = nextSaveSec
				s.logInfo("state save interval updated: %ds", currentSaveSec)
			}
		}
	}
}

func (s *Service) pollOnce(ctx context.Context) error {
	s.mu.RLock()
	cfg := s.cfg
	rules := s.rules
	s.mu.RUnlock()
	prevFile, prevOffset := s.tailer.Current()

	if !s.initialized {
		evs, err := s.tailer.Poll(true)
		if err != nil {
			return err
		}
		curFile, curOffset := s.tailer.Current()
		if curFile == "" && s.tryRecoverLogDir(cfg) {
			evs, err = s.tailer.Poll(true)
			if err != nil {
				return err
			}
			curFile, curOffset = s.tailer.Current()
		}
		s.logDebug("poll trace: phase=startup prev_file=%s prev_offset=%d cur_file=%s cur_offset=%d events=%d", prevFile, prevOffset, curFile, curOffset, len(evs))
		s.logInfo("startup monitor target: file=%s offset=%d", curFile, curOffset)
		if curFile == "" {
			s.warnNoLogFile(cfg)
		}
		if curFile != "" {
			if saved, ok := s.stateStore.Get(curFile); ok {
				s.logInfo("resume from saved offset: file=%s offset=%d", curFile, saved.Offset)
				s.tailer.SetOffset(curFile, saved.Offset)
				events, err := s.tailer.Poll(false)
				if err != nil {
					return err
				}
				if err := s.processEvents(ctx, cfg, rules, events); err != nil {
					return err
				}
			} else if cfg.Monitor.CheckExistingOnFirstRun {
				s.logInfo("first run existing lines check enabled: file=%s", curFile)
				s.tailer.SetOffset(curFile, 0)
				events, err := s.tailer.Poll(false)
				if err != nil {
					return err
				}
				if err := s.processEvents(ctx, cfg, rules, events); err != nil {
					return err
				}
			}
			if curFile2, curOffset2 := s.tailer.Current(); curFile2 != "" {
				s.stateStore.Set(curFile2, curOffset2)
				s.logDebug("startup state set: file=%s offset=%d", curFile2, curOffset2)
			} else if curFile != "" {
				s.stateStore.Set(curFile, curOffset)
				s.logDebug("startup state set (fallback): file=%s offset=%d", curFile, curOffset)
			}
		}
		s.initialized = true
		return nil
	}

	events, err := s.tailer.Poll(true)
	if err != nil {
		return err
	}
	curFile, curOffset := s.tailer.Current()
	if curFile == "" && s.tryRecoverLogDir(cfg) {
		events, err = s.tailer.Poll(true)
		if err != nil {
			return err
		}
		curFile, curOffset = s.tailer.Current()
	}
	s.logDebug("poll trace: phase=steady prev_file=%s prev_offset=%d cur_file=%s cur_offset=%d events=%d", prevFile, prevOffset, curFile, curOffset, len(events))
	if curFile == "" {
		s.warnNoLogFile(cfg)
	}
	if curFile != "" {
		s.stateStore.Set(curFile, curOffset)
	}
	return s.processEvents(ctx, cfg, rules, events)
}

func (s *Service) processEvents(ctx context.Context, cfg config.Config, rules []matcher.CompiledRule, events []monitor.Event) error {
	for _, ev := range events {
		matched := matcher.MatchLine(ev.Line, rules)
		linePreview := previewLine(ev.Line, 180)
		if len(matched) == 0 {
			s.logDebug("line analyzed: matched=none file=%s line=%q", ev.File, linePreview)
			continue
		}
		ruleNames := make([]string, 0, len(matched))
		for _, rule := range matched {
			ruleNames = append(ruleNames, rule.Name)
		}
		s.logInfo("line analyzed: matched=%s file=%s line=%q", strings.Join(ruleNames, ","), ev.File, linePreview)
		for _, rule := range matched {
			key := rule.Name + ":" + ev.Line
			if s.isDuplicate(key, time.Duration(cfg.Match.DedupeWindowSec)*time.Second) {
				s.logDebug("event deduped: rule=%s file=%s", rule.Name, ev.File)
				continue
			}
			s.logInfo("event matched: rule=%s file=%s", rule.Name, ev.File)
			if err := s.dispatcher.SendWithRule(ctx, cfg, rule, ev); err != nil {
				s.logError("notify error: rule=%s err=%v", rule.Name, err)
			} else {
				s.logDebug("notify success: rule=%s", rule.Name)
			}
			if err := s.hooks.RunAsync(cfg, rule.Name, ev); err != nil {
				s.logWarn("hook skipped: rule=%s err=%v", rule.Name, err)
			}
			s.lastEventAt = ev.At
		}
	}
	return nil
}

func (s *Service) SetDryRun(v bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg.Runtime.DryRun = v
	s.logInfo("dry-run set: %v", v)
}

func (s *Service) isDuplicate(key string, window time.Duration) bool {
	if window <= 0 {
		return false
	}
	now := time.Now()
	for k, at := range s.seen {
		if now.Sub(at) > window {
			delete(s.seen, k)
		}
	}
	if at, ok := s.seen[key]; ok && now.Sub(at) <= window {
		return true
	}
	s.seen[key] = now
	return false
}

func (s *Service) reload() error {
	cfg, err := config.Load(s.configPath)
	if err != nil {
		return err
	}
	s.mu.RLock()
	prevSnapshot := s.cfg
	s.mu.RUnlock()
	if reflect.DeepEqual(prevSnapshot, cfg) {
		s.logDebug("config reload checked: no changes")
		return nil
	}
	rules, err := matcher.Compile(cfg.Match.Rules)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if reflect.DeepEqual(s.cfg, cfg) {
		s.logDebug("config reload checked: no changes")
		return nil
	}
	prev := s.cfg
	s.cfg = cfg
	s.setLogLevel(cfg.Observability.LogLevel)
	s.rules = rules
	s.tailer.LogDir = cfg.Monitor.LogDir
	s.tailer.FileGlob = cfg.Monitor.FileGlob
	s.hooks = hook.New(cfg.Hooks.MaxConcurrency, func(err error) {
		s.logError("hook error: %v", err)
	})
	if prev.Observability.SelfLogPath != cfg.Observability.SelfLogPath || prev.Observability.Stdout != cfg.Observability.Stdout {
		s.logWarn("observability output destination change requires restart: self_log_path/stdout")
	}
	if fixedDir, ok := resolveMonitorLogDir(runtime.GOOS, s.cfg.Monitor.LogDir, pathExists); ok {
		s.cfg.Monitor.LogDir = fixedDir
		s.tailer.LogDir = fixedDir
		s.logWarn("monitor log_dir fallback applied: %s", fixedDir)
	}
	if s.cfg.Monitor.LogDir == "" || !pathExists(s.cfg.Monitor.LogDir) {
		s.logWarn("monitor directory does not exist: log_dir=%s", s.cfg.Monitor.LogDir)
	}
	if err := notify.CurlPreflight(cfg); err != nil {
		s.logWarn("notification preflight: %v", err)
	}
	s.logStartupProbe(s.cfg)
	s.logEffectiveNotificationAndRules(s.cfg)
	s.logInfo("config reloaded: log_dir=%s poll=%ds level=%s webhook=%s", cfg.Monitor.LogDir, cfg.Monitor.PollIntervalSec, cfg.Observability.LogLevel, config.MaskedWebhookURL(cfg.Notify.Discord.WebhookURL))
	return nil
}

func (s *Service) safeConfig() any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c := s.cfg
	c.Token = config.MaskedToken(c.Token)
	c.Notify.Discord.WebhookURL = config.MaskedWebhookURL(c.Notify.Discord.WebhookURL)
	return c
}

func (s *Service) status() any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	file, offset := s.tailer.Current()
	last := ""
	if !s.lastEventAt.IsZero() {
		last = s.lastEventAt.Format(time.RFC3339)
	}
	return ipc.Status{
		Running:            true,
		CurrentLogFile:     file,
		CurrentOffset:      offset,
		LastEventAtRFC3339: last,
	}
}

func BuildSafeTokenLine(cfg config.Config) string {
	return fmt.Sprintf("token=%s webhook=%s", config.MaskedToken(cfg.Token), config.MaskedWebhookURL(cfg.Notify.Discord.WebhookURL))
}

func ParseFlagList(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func previewLine(line string, maxRune int) string {
	if maxRune <= 0 {
		return ""
	}
	runes := []rune(strings.TrimSpace(line))
	if len(runes) <= maxRune {
		return string(runes)
	}
	if maxRune <= 1 {
		return string(runes[:1])
	}
	if maxRune <= 3 {
		return string(runes[:maxRune])
	}
	return string(runes[:maxRune-3]) + "..."
}

func pathExists(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

func resolveMonitorLogDir(goos, dir string, exists func(string) bool) (string, bool) {
	clean := strings.TrimSpace(dir)
	if clean == "" {
		return dir, false
	}
	if goos != "windows" {
		return dir, false
	}
	if exists(clean) {
		return clean, false
	}
	alt, ok := windowsLogDirFallback(clean)
	if !ok {
		return clean, false
	}
	if exists(alt) {
		return alt, true
	}
	return clean, false
}

func windowsLogDirFallback(path string) (string, bool) {
	p := strings.ReplaceAll(strings.TrimSpace(path), "/", `\`)
	lp := strings.ToLower(p)
	legacy := `\appdata\local\low\`
	current := `\appdata\locallow\`
	switch {
	case strings.Contains(lp, legacy):
		i := strings.Index(lp, legacy)
		return p[:i] + `\AppData\LocalLow\` + p[i+len(legacy):], true
	case strings.Contains(lp, current):
		i := strings.Index(lp, current)
		return p[:i] + `\AppData\Local\Low\` + p[i+len(current):], true
	default:
		return "", false
	}
}

const (
	levelDebug int32 = iota
	levelInfo
	levelWarn
	levelError
)

func (s *Service) setLogLevel(raw string) {
	atomic.StoreInt32(&s.logLevel, parseLogLevel(raw))
}

func parseLogLevel(raw string) int32 {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "debug":
		return levelDebug
	case "warn":
		return levelWarn
	case "error":
		return levelError
	default:
		return levelInfo
	}
}

func (s *Service) logDebug(format string, args ...any) { s.logf(levelDebug, "DEBUG", format, args...) }
func (s *Service) logInfo(format string, args ...any)  { s.logf(levelInfo, "INFO", format, args...) }
func (s *Service) logWarn(format string, args ...any)  { s.logf(levelWarn, "WARN", format, args...) }
func (s *Service) logError(format string, args ...any) { s.logf(levelError, "ERROR", format, args...) }

func (s *Service) logf(level int32, label, format string, args ...any) {
	if level < atomic.LoadInt32(&s.logLevel) {
		return
	}
	s.logger.Printf("[%s] %s", label, fmt.Sprintf(format, args...))
}

func (s *Service) warnNoLogFile(cfg config.Config) {
	now := time.Now()
	if !s.lastNoFileWarn.IsZero() && now.Sub(s.lastNoFileWarn) < 60*time.Second {
		return
	}
	s.lastNoFileWarn = now
	s.logWarn("no log file found: log_dir=%s file_glob=%s", cfg.Monitor.LogDir, cfg.Monitor.FileGlob)
}

func (s *Service) tryRecoverLogDir(cfg config.Config) bool {
	candidates := monitorDirCandidates(runtime.GOOS, cfg.Monitor.LogDir)
	for _, dir := range candidates {
		if strings.EqualFold(strings.TrimSpace(dir), strings.TrimSpace(s.tailer.LogDir)) {
			continue
		}
		if !hasLogFileCandidate(dir, cfg.Monitor.FileGlob) {
			continue
		}
		s.mu.Lock()
		s.tailer.LogDir = dir
		s.cfg.Monitor.LogDir = dir
		s.mu.Unlock()
		s.logWarn("monitor log_dir auto-recovered: %s", dir)
		s.logStartupProbe(s.cfg)
		return true
	}
	return false
}

func hasLogFileCandidate(dir, glob string) bool {
	_, ok := latestByPattern(dir, glob)
	return ok
}

func latestByPattern(dir, glob string) (string, bool) {
	if strings.TrimSpace(dir) == "" || strings.TrimSpace(glob) == "" {
		return "", false
	}
	pattern := filepath.Join(dir, glob)
	files, err := filepath.Glob(pattern)
	if err != nil || len(files) == 0 {
		return "", false
	}
	sort.Slice(files, func(i, j int) bool {
		ai, aErr := os.Stat(files[i])
		bi, bErr := os.Stat(files[j])
		if aErr != nil || bErr != nil {
			return files[i] > files[j]
		}
		return ai.ModTime().After(bi.ModTime())
	})
	return files[0], true
}

func monitorDirCandidates(goos, dir string) []string {
	out := make([]string, 0, 3)
	seen := map[string]struct{}{}
	add := func(v string) {
		v = strings.TrimSpace(v)
		if v == "" {
			return
		}
		k := strings.ToLower(v)
		if _, ok := seen[k]; ok {
			return
		}
		seen[k] = struct{}{}
		out = append(out, v)
	}
	add(dir)
	if goos == "windows" {
		if alt, ok := windowsLogDirFallback(dir); ok {
			add(alt)
		}
		// Slash style variant helps when config was edited manually.
		add(strings.ReplaceAll(dir, `\`, "/"))
	}
	return out
}

func (s *Service) logStartupProbe(cfg config.Config) {
	candidates := monitorDirCandidates(runtime.GOOS, cfg.Monitor.LogDir)
	s.logInfo("startup probe: candidates=%d file_glob=%s", len(candidates), cfg.Monitor.FileGlob)
	for i, dir := range candidates {
		latest, ok := latestByPattern(dir, cfg.Monitor.FileGlob)
		if ok {
			s.logInfo("startup probe[%d]: dir=%s exists=%v latest=%s", i, dir, pathExists(dir), latest)
		} else {
			s.logInfo("startup probe[%d]: dir=%s exists=%v latest=", i, dir, pathExists(dir))
		}
	}
}

func (s *Service) logEffectiveNotificationAndRules(cfg config.Config) {
	names := make([]string, 0, len(cfg.Match.Rules))
	for _, r := range cfg.Match.Rules {
		if strings.TrimSpace(r.Name) != "" {
			names = append(names, r.Name)
		}
	}
	ruleList := strings.Join(names, ",")
	if ruleList == "" {
		ruleList = "(none)"
	}
	s.logInfo("effective rules: count=%d names=%s", len(cfg.Match.Rules), ruleList)
	s.logInfo(
		"effective notify: dry_run=%v discord_enabled=%v webhook=%s local_path=%s",
		cfg.Runtime.DryRun,
		cfg.Notify.Discord.Enabled,
		config.MaskedWebhookURL(cfg.Notify.Discord.WebhookURL),
		cfg.Notify.Local.Path,
	)
	if !cfg.Notify.Discord.Enabled {
		s.logWarn("discord notification is disabled")
	}
	if cfg.Notify.Discord.Enabled && strings.TrimSpace(cfg.Notify.Discord.WebhookURL) == "" {
		s.logWarn("discord webhook is empty while discord notification is enabled")
	}
}
