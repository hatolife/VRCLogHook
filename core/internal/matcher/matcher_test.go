package matcher

import (
	"testing"

	"github.com/hatolife/VRCLogHook/core/internal/config"
)

func TestMatchLine(t *testing.T) {
	rules, err := Compile([]config.Rule{
		{Name: "contains", Contains: "joined", CaseSensitive: false},
		{Name: "regex", Regex: `\bERROR\b`, CaseSensitive: true},
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
