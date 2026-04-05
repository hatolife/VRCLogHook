//go:build windows

package main

import (
	"os/exec"
	"syscall"
)

func applyGUIProcessAttrs(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}
