package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
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

	mu          sync.RWMutex
	cfg         config.Config
	rules       []matcher.CompiledRule
	tailer      *monitor.Tailer
	stateStore  *state.Store
	dispatcher  *notify.Dispatcher
	hooks       *hook.Runner
	logger      *log.Logger
	logLevel    int32
	lastEventAt time.Time
	seen        map[string]time.Time
	initialized bool
}

func New(configPath, ipcPath string) (*Service, error) {
	cfg, err := config.LoadOrCreate(configPath)
	if err != nil {
		return nil, err
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
		logger:      logger,
		seen:        map[string]time.Time{},
		lastEventAt: time.Time{},
	}
	s.setLogLevel(cfg.Observability.LogLevel)
	s.logInfo("service init: config=%s state=%s log_dir=%s file_glob=%s dry_run=%v", s.configPath, cfg.State.Path, cfg.Monitor.LogDir, cfg.Monitor.FileGlob, cfg.Runtime.DryRun)
	s.logDebug("service init detail: %s", BuildSafeTokenLine(cfg))
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

	saveTicker := time.NewTicker(time.Duration(s.cfg.State.SaveIntervalSec) * time.Second)
	statusTicker := time.NewTicker(time.Duration(s.cfg.Observability.StatusLogSec) * time.Second)
	reloadTicker := time.NewTicker(time.Duration(s.cfg.Runtime.ConfigReloadSec) * time.Second)
	defer saveTicker.Stop()
	defer statusTicker.Stop()
	defer reloadTicker.Stop()

	for {
		s.mu.RLock()
		interval := s.cfg.Monitor.PollIntervalSec
		hotReload := s.cfg.Runtime.HotReload
		s.mu.RUnlock()

		pollTimer := time.NewTimer(time.Duration(interval) * time.Second)
		select {
		case <-ctx.Done():
			s.logInfo("shutdown requested")
			_ = s.stateStore.Save()
			s.hooks.Wait()
			s.logInfo("shutdown completed")
			return nil
		case <-pollTimer.C:
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
			if hotReload {
				if err := s.reload(); err != nil {
					s.logError("reload error: %v", err)
				}
			}
		}
		pollTimer.Stop()
	}
}

func (s *Service) pollOnce(ctx context.Context) error {
	s.mu.RLock()
	cfg := s.cfg
	rules := s.rules
	s.mu.RUnlock()

	if !s.initialized {
		if _, err := s.tailer.Poll(true); err != nil {
			return err
		}
		curFile, curOffset := s.tailer.Current()
		s.logInfo("startup monitor target: file=%s offset=%d", curFile, curOffset)
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
	if curFile != "" {
		s.stateStore.Set(curFile, curOffset)
	}
	if len(events) > 0 {
		s.logDebug("poll received events: count=%d file=%s", len(events), curFile)
	}
	return s.processEvents(ctx, cfg, rules, events)
}

func (s *Service) processEvents(ctx context.Context, cfg config.Config, rules []matcher.CompiledRule, events []monitor.Event) error {
	for _, ev := range events {
		matched := matcher.MatchLine(ev.Line, rules)
		for _, rule := range matched {
			key := rule.Name + ":" + ev.Line
			if s.isDuplicate(key, time.Duration(cfg.Match.DedupeWindowSec)*time.Second) {
				s.logDebug("event deduped: rule=%s file=%s", rule.Name, ev.File)
				continue
			}
			s.logInfo("event matched: rule=%s file=%s", rule.Name, ev.File)
			if err := s.dispatcher.Send(ctx, cfg, rule.Name, ev); err != nil {
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
	rules, err := matcher.Compile(cfg.Match.Rules)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
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
