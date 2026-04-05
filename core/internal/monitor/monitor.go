package monitor

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"
)

type Event struct {
	File string
	Line string
	At   time.Time
}

type Tailer struct {
	LogDir    string
	FileGlob  string
	readLimit int

	current string
	offset  int64
}

func New(logDir, fileGlob string) *Tailer {
	return &Tailer{
		LogDir:    logDir,
		FileGlob:  fileGlob,
		readLimit: 1024 * 1024,
	}
}

func (t *Tailer) SetOffset(path string, offset int64) {
	t.current = path
	t.offset = offset
}

func (t *Tailer) Current() (string, int64) {
	return t.current, t.offset
}

func (t *Tailer) Poll(startAtHead bool) ([]Event, error) {
	latest, err := latestLogFile(t.LogDir, t.FileGlob)
	if err != nil {
		return nil, err
	}
	if latest == "" {
		return nil, nil
	}
	st, err := os.Stat(latest)
	if err != nil {
		return nil, err
	}
	if t.current != latest {
		t.current = latest
		if startAtHead {
			t.offset = st.Size()
		} else {
			t.offset = 0
		}
	}
	if st.Size() < t.offset {
		t.offset = 0
	}
	f, err := os.Open(latest)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	if _, err := f.Seek(t.offset, io.SeekStart); err != nil {
		return nil, err
	}
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 0, 64*1024), t.readLimit)

	out := make([]Event, 0, 8)
	for s.Scan() {
		out = append(out, Event{File: latest, Line: s.Text(), At: time.Now()})
	}
	if err := s.Err(); err != nil {
		return nil, err
	}
	cur, err := f.Seek(0, io.SeekCurrent)
	if err != nil {
		return nil, err
	}
	t.offset = cur
	return out, nil
}

func latestLogFile(dir, glob string) (string, error) {
	if dir == "" {
		return "", errors.New("empty monitor directory")
	}
	pattern := filepath.Join(dir, glob)
	files, err := filepath.Glob(pattern)
	if err != nil {
		return "", fmt.Errorf("glob error: %w", err)
	}
	sort.Slice(files, func(i, j int) bool {
		ai, aErr := os.Stat(files[i])
		bi, bErr := os.Stat(files[j])
		if aErr != nil || bErr != nil {
			return files[i] > files[j]
		}
		return ai.ModTime().After(bi.ModTime())
	})
	if len(files) == 0 {
		return "", nil
	}
	return files[0], nil
}
