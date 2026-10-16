package agent

import "strings"

const (
	millionTokens         = 1_000_000
	olderWindow           = 200_000
	smallWindowCompaction = 300_000
)

var olderClaude = []string{"claude-3", "claude-haiku-4-5", "claude-opus-4-5", "claude-sonnet-4-5", "claude-opus-4-1",
	"claude-opus-4-0", "claude-sonnet-4-0", "claude-opus-4-2", "claude-sonnet-4-2"}

func claudeWindow(model string) int {
	if !strings.HasPrefix(model, "claude-") {
		return 0
	}
	for _, older := range olderClaude {
		if strings.HasPrefix(model, older) {
			return olderWindow
		}
	}
	return millionTokens
}

func ContextLeft(used, window int) (int, bool) {
	if window <= 0 || used <= 0 {
		return 0, false
	}
	if used > window && window == olderWindow {
		window = millionTokens
	}
	return ((window-min(used, window))*100 + window/2) / window, true
}
