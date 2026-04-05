package monitor

import "regexp"

var vrchatLinePattern = regexp.MustCompile(`^\d{4}\.\d{2}\.\d{2} \d{2}:\d{2}:\d{2} \w+\s+-\s+.+$`)

func LooksLikeVRChatLogLine(line string) bool {
	return vrchatLinePattern.MatchString(line)
}
