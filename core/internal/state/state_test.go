package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStoreSaveAndReload(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state.json")
	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	s.Set("log-a", 42)
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}

	s2, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	e, ok := s2.Get("log-a")
	if !ok {
		t.Fatal("state entry not found")
	}
	if e.Offset != 42 {
		t.Fatalf("unexpected offset: %d", e.Offset)
	}
}

func TestStoreCorruptionRecovery(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(p, []byte("{bad-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Get("any"); ok {
		t.Fatal("expected empty recovered state")
	}
}

func TestStoreUpdateOverwrite(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state.json")
	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	s.Set("log-a", 10)
	s.Set("log-a", 99)
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	e, ok := s2.Get("log-a")
	if !ok || e.Offset != 99 {
		t.Fatalf("unexpected state value: ok=%v offset=%d", ok, e.Offset)
	}
}
