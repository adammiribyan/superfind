package main

import (
	"errors"
	"testing"
)

func TestTheChangelogHasAHeadlineForThisVersion(t *testing.T) {
	if _, err := headline(changelog, version); err != nil {
		t.Errorf("CHANGELOG.md needs a > headline under ## [%s]: %v", version, err)
	}
	if _, err := headline(changelog, "Unreleased"); err != nil && !errors.Is(err, errNoSection) {
		t.Errorf("CHANGELOG.md needs a > headline under ## [Unreleased]: %v", err)
	}
}

func TestAHeadlineIsTheFirstLineUnderItsVersionAsPlainText(t *testing.T) {
	long := "Searches every session your agents have kept, then resumes the one"
	cases := []struct {
		changelog, want, problem string
	}{
		{"## [0.11.0] - 2026-10-11\n\n> Handoff, last, and fish\n\n### Added\n", "Handoff, last, and fish", ""},
		{"## [0.12.0]\n> New\n## [0.11.0] - 2026-10-11\n\n>  Handoff  \n", "Handoff", ""},
		{"## [Unreleased]\n\n> Upgrade in place\n## [0.11.0] - 2026-10-11\n\n### Added\n", "", "## [0.11.0] starts with \"### Added\", not a one-line > headline"},
		{"## [0.11.0] - 2026-10-11\n\n", "", "## [0.11.0] is empty, it needs a one-line > headline"},
		{"## [0.10.0]\n\n> Older\n", "", "no such section"},
		{"## [0.11.0]\n\n> \n", "", "the headline is empty"},
		{"## [0.11.0]\n\n> Handoff and last.\n", "Handoff and last.", `the headline "Handoff and last." ends in a period`},
		{"## [0.11.0]\n\n> " + long + "\n", long, `the headline "` + long + `" is 66 characters, more than 64`},
		{"## [0.11.0]\n\n> Run `superfind last`\n", "Run `superfind last`", "the headline \"Run `superfind last`\" has markdown, a link, or a long dash; it is shown as plain text"},
		{"## [0.11.0]\n\n> See [the docs](https://superfind.dev)\n", "See [the docs](https://superfind.dev)", `the headline "See [the docs](https://superfind.dev)" has markdown, a link, or a long dash; it is shown as plain text`},
		{"## [0.11.0]\n\n> Handoff — and last\n", "Handoff — and last", `the headline "Handoff — and last" has markdown, a link, or a long dash; it is shown as plain text`},
	}
	for _, c := range cases {
		got, err := headline(c.changelog, "0.11.0")
		problem := ""
		if err != nil {
			problem = err.Error()
		}
		if got != c.want || problem != c.problem {
			t.Errorf("headline(%q) = %q, %q; want %q, %q", c.changelog, got, problem, c.want, c.problem)
		}
	}
}
