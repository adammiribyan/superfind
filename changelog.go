package main

import (
	_ "embed"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/adammiribyan/superfind/internal/news"
	"github.com/adammiribyan/superfind/internal/release"
)

//go:embed CHANGELOG.md
var changelog string

const maxHeadline = 64

var errNoSection = errors.New("no such section")

func headline(changelog, section string) (string, error) {
	lines := strings.Split(changelog, "\n")
	for i, line := range lines {
		if !strings.HasPrefix(line, "## ["+section+"]") {
			continue
		}
		for _, next := range lines[i+1:] {
			if strings.TrimSpace(next) == "" {
				continue
			}
			text, ok := strings.CutPrefix(next, "> ")
			if !ok {
				return "", fmt.Errorf("## [%s] starts with %q, not a one-line > headline", section, next)
			}
			text = strings.TrimSpace(text)
			return text, plain(text)
		}
		return "", fmt.Errorf("## [%s] is empty, it needs a one-line > headline", section)
	}
	return "", errNoSection
}

func plain(text string) error {
	switch n := utf8.RuneCountInString(text); {
	case n == 0:
		return errors.New("the headline is empty")
	case n > maxHeadline:
		return fmt.Errorf("the headline %q is %d characters, more than %d", text, n, maxHeadline)
	case strings.HasSuffix(text, "."):
		return fmt.Errorf("the headline %q ends in a period", text)
	case strings.ContainsAny(text, "`[]<>*_—–"), strings.Contains(text, "://"):
		return fmt.Errorf("the headline %q has markdown, a link, or a long dash; it is shown as plain text", text)
	}
	return nil
}

func build() news.Build {
	v, _ := release.ParseVersion(version)
	text, err := headline(changelog, version)
	if err != nil {
		text = ""
	}
	return news.Build{Version: v, Headline: text}
}
