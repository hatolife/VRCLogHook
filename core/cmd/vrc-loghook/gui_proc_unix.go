//go:build !windows

package main

import "os/exec"

func applyGUIProcessAttrs(_ *exec.Cmd) {}
