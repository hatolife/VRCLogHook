package matcher

import (
	"regexp"
	"strings"

	"github.com/hatolife/VRCLogHook/core/internal/config"
)

type CompiledRule struct {
	Rule  config.Rule
	re    *regexp.Regexp
	lower string
}

func Compile(rules []config.Rule) ([]CompiledRule, error) {
	out := make([]CompiledRule, 0, len(rules))
	for _, r := range rules {
		cr := CompiledRule{Rule: r}
		if r.Regex != "" {
			re, err := regexp.Compile(r.Regex)
			if err != nil {
				return nil, err
			}
			cr.re = re
		}
		if r.Contains != "" && !r.CaseSensitive {
			cr.lower = strings.ToLower(r.Contains)
		}
		out = append(out, cr)
	}
	return out, nil
}

func MatchLine(line string, rules []CompiledRule) []config.Rule {
	matched := make([]config.Rule, 0, 2)
	lowerLine := strings.ToLower(line)
	for _, r := range rules {
		if r.re != nil && r.re.MatchString(line) {
			matched = append(matched, r.Rule)
			continue
		}
		if r.Rule.Contains == "" {
			continue
		}
		if r.Rule.CaseSensitive && strings.Contains(line, r.Rule.Contains) {
			matched = append(matched, r.Rule)
			continue
		}
		if !r.Rule.CaseSensitive && strings.Contains(lowerLine, r.lower) {
			matched = append(matched, r.Rule)
		}
	}
	return matched
}
