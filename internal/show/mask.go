package show

import (
	"regexp"
	"strings"
	"unicode"
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
	{"password in a URL", regexp.MustCompile(`\b[a-z][a-z0-9+.-]*://[^\s:/@]*:[^\s@/]{3,}@`), -1},
}

var urlPassword = regexp.MustCompile(`^([a-z][a-z0-9+.-]*://[^\s:/@]*:)[^\s@/]+@$`)

var namedSecret = regexp.MustCompile(`(?i)\b[a-z0-9_-]*?(?:password|passwd|passphrase|secret|api[_-]?key|access[_-]?key|` +
	`access[_-]?token|auth[_-]?token|refresh[_-]?token|client[_-]?secret|private[_-]?key)["']?([ \t]*)([:=])([ \t]*)` +
	`(?:"((?:[^"\\\n]|\\.)*)"|'((?:[^'\\\n]|\\.)*)'|([^\s"',;&<>()\[\]{}` + "`" + `]+))`)

var (
	placeholder = regexp.MustCompile(`^(?:[$<{%]|\[masked |\*+$|x+$|\.\.\.$|…$)`)
	codeName    = regexp.MustCompile(`(?i)^(?:none|nil|null|undefined|true|false|key|token|secret|password|passwd|passphrase|` +
		`[a-z_][a-z0-9_]*(?:\.[a-z_][a-z0-9_]*)+)$`)
)

func secretValue(separator byte, quoted bool, value, after string) bool {
	if value == "" || placeholder.MatchString(value) {
		return false
	}
	if !quoted && (codeName.MatchString(value) || strings.HasPrefix(after, "(") || strings.HasPrefix(after, "[")) {
		return false
	}
	if quoted || separator == '=' {
		return true
	}
	return len(value) >= 16 || strings.IndexFunc(value, func(r rune) bool { return !unicode.IsLetter(r) }) >= 0
}

func maskNamedSecrets(text string) string {
	var out strings.Builder
	last := 0
	for _, m := range namedSecret.FindAllStringSubmatchIndex(text, -1) {
		separator := text[m[4]]
		emptyShellValue := separator == '=' && m[3] == m[2] && m[7] > m[6]
		for group := 4; group <= 6; group++ {
			start, end := m[2*group], m[2*group+1]
			if start < 0 || emptyShellValue || !secretValue(separator, group < 6, text[start:end], text[end:]) {
				continue
			}
			out.WriteString(text[last:start])
			out.WriteString("[masked secret]")
			last = end
		}
	}
	out.WriteString(text[last:])
	return out.String()
}

func Mask(text string) string {
	for _, p := range secretPatterns {
		text = p.re.ReplaceAllStringFunc(text, func(found string) string {
			if p.keep > 0 && !strings.ContainsAny(found[p.keep:], "0123456789") {
				return found
			}
			masked := "[masked " + p.kind + "]"
			if p.keep < 0 {
				return urlPassword.ReplaceAllString(found, "${1}"+masked+"@")
			}
			return found[:p.keep] + masked
		})
	}
	return maskNamedSecrets(text)
}
