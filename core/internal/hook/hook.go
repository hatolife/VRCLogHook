package hook

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"time"

	"github.com/hatolife/VRCLogHook/core/internal/config"
	"github.com/hatolife/VRCLogHook/core/internal/monitor"
)

type Runner struct {
	sem     chan struct{}
	wg      sync.WaitGroup
	onError func(error)
}

func New(maxConcurrency int, onError func(error)) *Runner {
	if maxConcurrency < 1 {
		maxConcurrency = 1
	}
	if onError == nil {
		onError = func(error) {}
	}
	return &Runner{sem: make(chan struct{}, maxConcurrency), onError: onError}
}

func (r *Runner) RunAsync(cfg config.Config, ruleName string, ev monitor.Event) error {
	if !cfg.Hooks.Enabled {
		return nil
	}
	if !cfg.Hooks.UnsafeConsent {
		return errors.New("hooks are enabled but unsafe_consent is false")
	}
	r.sem <- struct{}{}
	r.wg.Add(1)
	go func() {
		defer func() {
			<-r.sem
			r.wg.Done()
		}()
		if err := runOnce(cfg, ruleName, ev); err != nil {
			r.onError(err)
		}
	}()
	return nil
}

func (r *Runner) Wait() {
	r.wg.Wait()
}

func runOnce(cfg config.Config, ruleName string, ev monitor.Event) error {
	eventJSON, _ := json.Marshal(map[string]any{
		"rule": ruleName,
		"file": ev.File,
		"line": ev.Line,
		"at":   ev.At.Format(time.RFC3339),
	})
	for _, c := range cfg.Hooks.Commands {
		if !c.Enabled || c.Program == "" {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.Hooks.TimeoutSec)*time.Second)
		cmd := exec.CommandContext(ctx, c.Program, append(c.Args, "--rule", ruleName, "--file", ev.File, "--at", ev.At.Format(time.RFC3339))...)
		cmd.Stdin = bytes.NewReader(eventJSON)
		_, err := cmd.CombinedOutput()
		cancel()

		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("hook timeout: %s", c.Name)
		}
		if err != nil {
			return fmt.Errorf("hook failed: %s: %w", c.Name, err)
		}
	}
	return nil
}
