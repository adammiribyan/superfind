package show

import (
	"regexp"
)

type secretPattern struct {
	kind string
	re   *regexp.Regexp
	keep int
}

var secretPatterns = []secretPattern{
	{"private key", regexp.MustCompile(`-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----(?s:.*?)(?:-----END [A-Z0-9 ]*PRIVATE KEY-----|\z)`), 0},
	{"Anthropic key", regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_-]{20,}`), 0},
	{"OpenAI key", regexp.MustCompile(`\bsk-(?:proj-|svcacct-|admin-)?[A-Za-z0-9_-]{32,}`), 0},
	{"xAI key", regexp.MustCompile(`\bxai-[A-Za-z0-9]{32,}`), 0},
	{"GitHub token", regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9]{36,}|github_pat_[A-Za-z0-9_]{40,})`), 0},
	{"AWS access key", regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`), 0},
	{"Slack token", regexp.MustCompile(`\bxox[abposr]-[A-Za-z0-9-]{10,}`), 0},
	{"Stripe key", regexp.MustCompile(`\b(?:sk|rk)_(?:live|test)_[A-Za-z0-9]{16,}`), 0},
	{"Google API key", regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}`), 0},
	{"PostHog key", regexp.MustCompile(`\bph[cx]_[A-Za-z0-9]{30,}`), 0},
	{"JSON web token", regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`), 0},
	{"bearer token", regexp.MustCompile(`(?i)\bBearer [A-Za-z0-9._~+/-]{20,}=*`), len("Bearer ")},
	{"password in a URL", regexp.MustCompile(`\b[a-z][a-z0-9+.-]*://[^\s:/@]+:[^\s@/]{3,}@`), -1},
	{"secret", regexp.MustCompile(`(?i)\b(?:password|passwd|secret|api[_-]?key|access[_-]?token|auth[_-]?token|client[_-]?secret)["']?\s*[:=]\s*["']?[^\s"'<>]{8,}`), -2},
}

var (
	urlPassword = regexp.MustCompile(`^([a-z][a-z0-9+.-]*://[^\s:/@]+:)[^\s@/]+@$`)
	assignment  = regexp.MustCompile(`(?i)^([^:=]*[:=]\s*["']?)(.*)$`)
)

func Mask(text string) string {
	for _, p := range secretPatterns {
		text = p.re.ReplaceAllStringFunc(text, func(found string) string {
			masked := "[masked " + p.kind + "]"
			switch p.keep {
			case -1:
				return urlPassword.ReplaceAllString(found, "${1}"+masked+"@")
			case -2:
				return assignment.ReplaceAllString(found, "${1}"+masked)
			default:
				return found[:p.keep] + masked
			}
		})
	}
	return text
}
