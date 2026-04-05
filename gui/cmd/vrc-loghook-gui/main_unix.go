//go:build !windows

package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

type request struct {
	Token  string `json:"token"`
	Method string `json:"method"`
}

func main() {
	configPath := flag.String("config", defaultConfigPath(), "config path")
	ipcPath := flag.String("ipc", filepath.Join(os.TempDir(), "vrc-loghook.sock"), "ipc socket path")
	watch := flag.Bool("watch", true, "watch status periodically")
	interval := flag.Int("interval-sec", 2, "status refresh interval")
	flag.Parse()

	token, err := loadToken(*configPath)
	exitOnErr(err)

	if !*watch {
		printStatus(*ipcPath, token)
		return
	}
	for {
		printStatus(*ipcPath, token)
		time.Sleep(time.Duration(*interval) * time.Second)
	}
}

func loadToken(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	b = sanitizeHJSONLike(b)
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		return "", err
	}
	v, _ := raw["token"].(string)
	if v == "" {
		return "", fmt.Errorf("token is missing in config")
	}
	return v, nil
}

func call(ipcPath, token, method string) (map[string]any, error) {
	c, err := net.Dial("unix", ipcPath)
	if err != nil {
		return nil, err
	}
	defer c.Close()

	if err := json.NewEncoder(c).Encode(request{Token: token, Method: method}); err != nil {
		return nil, err
	}
	var resp map[string]any
	if err := json.NewDecoder(bufio.NewReader(c)).Decode(&resp); err != nil {
		return nil, err
	}
	return resp, nil
}

func printStatus(ipcPath, token string) {
	resp, err := call(ipcPath, token, "status")
	if err != nil {
		fmt.Println("status error:", err)
		return
	}
	out, _ := json.MarshalIndent(resp, "", "  ")
	fmt.Println(string(out))
}

func sanitizeHJSONLike(b []byte) []byte {
	lines := bytes.Split(b, []byte("\n"))
	for i := range lines {
		line := lines[i]
		if idx := bytes.Index(line, []byte("//")); idx >= 0 {
			line = line[:idx]
		}
		if idx := bytes.Index(line, []byte("#")); idx == 0 {
			line = nil
		}
		lines[i] = line
	}
	return bytes.Join(lines, []byte("\n"))
}

func defaultConfigPath() string {
	home, _ := os.UserHomeDir()
	if home == "" {
		home = "."
	}
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Application Support", "VRCLogHook", "config.hjson")
	}
	return filepath.Join(home, ".config", "vrc-loghook", "config.hjson")
}

func exitOnErr(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
