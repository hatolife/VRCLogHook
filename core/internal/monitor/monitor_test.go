package monitor

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTailerReadsAppendedLines(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "output_log_2026-04-05_00-00-00.txt")
	if err := os.WriteFile(logPath, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	tailer := New(dir, "output_log_*.txt")
	_, err := tailer.Poll(false)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("2026.04.05 19:09:42 Log      - Joined room\n")
	_ = f.Close()

	evs, err := tailer.Poll(false)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 1 {
		t.Fatalf("expected 1 event, got %d", len(evs))
	}
	if !LooksLikeVRChatLogLine(evs[0].Line) {
		t.Fatalf("line does not match VRChat-like format: %q", evs[0].Line)
	}
}

func TestTailerFollowsRotation(t *testing.T) {
	dir := t.TempDir()
	oldPath := filepath.Join(dir, "output_log_2026-04-05_00-00-00.txt")
	newPath := filepath.Join(dir, "output_log_2026-04-05_00-10-00.txt")
	_ = os.WriteFile(oldPath, []byte("2026.04.05 19:00:00 Log      - old\n"), 0o600)
	tailer := New(dir, "output_log_*.txt")
	_, _ = tailer.Poll(true)

	time.Sleep(20 * time.Millisecond)
	_ = os.WriteFile(newPath, []byte(""), 0o600)
	_, err := tailer.Poll(true)
	if err != nil {
		t.Fatal(err)
	}

	f, _ := os.OpenFile(newPath, os.O_APPEND|os.O_WRONLY, 0)
	_, _ = f.WriteString("2026.04.05 19:10:00 Warning  - rotated\n")
	_ = f.Close()

	evs, err := tailer.Poll(true)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 1 {
		t.Fatalf("expected 1 rotated event, got %d", len(evs))
	}
}

func TestTailerHandlesTruncate(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "output_log_2026-04-05_00-00-00.txt")
	_ = os.WriteFile(logPath, []byte("2026.04.05 19:00:00 Log      - first\n"), 0o600)
	tailer := New(dir, "output_log_*.txt")
	if _, err := tailer.Poll(false); err != nil {
		t.Fatal(err)
	}
	if _, err := tailer.Poll(false); err != nil {
		t.Fatal(err)
	}

	// Simulate truncate + append.
	if err := os.WriteFile(logPath, []byte("2026.04.05 19:00:01 Log      - after-truncate\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	evs, err := tailer.Poll(false)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 1 {
		t.Fatalf("expected 1 event after truncate, got %d", len(evs))
	}
}

func TestLooksLikeVRChatLogLineNegative(t *testing.T) {
	if LooksLikeVRChatLogLine("not a vrchat log line") {
		t.Fatal("unexpected match for invalid log format")
	}
}
