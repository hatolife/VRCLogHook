package matcher

import (
	"testing"

	"github.com/hatolife/VRCLogHook/core/internal/config"
)

func TestMatchLine(t *testing.T) {
	rules, err := Compile([]config.Rule{
		{Enabled: true, Name: "contains", Contains: "joined", CaseSensitive: false},
		{Enabled: true, Name: "regex", Regex: `\bERROR\b`, CaseSensitive: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	line := "PlayerA joined room - ERROR code"
	got := MatchLine(line, rules)
	if len(got) != 2 {
		t.Fatalf("expected 2 matches, got %d", len(got))
	}
}

func TestCompileSkipsDisabledRules(t *testing.T) {
	rules, err := Compile([]config.Rule{
		{Enabled: false, Name: "disabled", Contains: "joined", CaseSensitive: false},
		{Enabled: true, Name: "enabled", Contains: "joined", CaseSensitive: false},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := MatchLine("Player joined room", rules)
	if len(got) != 1 {
		t.Fatalf("expected 1 match, got %d", len(got))
	}
	if got[0].Name != "enabled" {
		t.Fatalf("unexpected matched rule: %s", got[0].Name)
	}
}
