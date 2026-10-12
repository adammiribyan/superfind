package main

import "testing"

func TestResumeLooksUpPicksOrRanksAndRunsOrPrintsByShapeAndTerminal(t *testing.T) {
	cases := []struct {
		shape                       shape
		interactive, stdoutTTY, run bool
		want                        plan
	}{
		{nothing, false, false, false, plan{false, byPicker, false}},
		{nothing, false, false, true, plan{false, byPicker, true}},
		{nothing, false, true, false, plan{false, byPicker, false}},
		{nothing, false, true, true, plan{false, byPicker, true}},
		{nothing, true, false, false, plan{false, byPicker, false}},
		{nothing, true, false, true, plan{false, byPicker, true}},
		{nothing, true, true, false, plan{false, byPicker, true}},
		{nothing, true, true, true, plan{false, byPicker, true}},
		{exact, false, false, false, plan{true, noChoice, false}},
		{exact, false, false, true, plan{true, noChoice, true}},
		{exact, false, true, false, plan{true, noChoice, false}},
		{exact, false, true, true, plan{true, noChoice, true}},
		{exact, true, false, false, plan{true, noChoice, false}},
		{exact, true, false, true, plan{true, noChoice, true}},
		{exact, true, true, false, plan{true, noChoice, true}},
		{exact, true, true, true, plan{true, noChoice, true}},
		{word, false, false, false, plan{true, byRank, false}},
		{word, false, false, true, plan{true, byRank, false}},
		{word, false, true, false, plan{true, byRank, false}},
		{word, false, true, true, plan{true, byRank, false}},
		{word, true, false, false, plan{true, byPicker, false}},
		{word, true, false, true, plan{true, byPicker, true}},
		{word, true, true, false, plan{true, byPicker, true}},
		{word, true, true, true, plan{true, byPicker, true}},
		{query, false, false, false, plan{false, byRank, false}},
		{query, false, false, true, plan{false, byRank, false}},
		{query, false, true, false, plan{false, byRank, false}},
		{query, false, true, true, plan{false, byRank, false}},
		{query, true, false, false, plan{false, byPicker, false}},
		{query, true, false, true, plan{false, byPicker, true}},
		{query, true, true, false, plan{false, byPicker, true}},
		{query, true, true, true, plan{false, byPicker, true}},
	}
	for _, c := range cases {
		if got := resumePlan(c.shape, c.interactive, c.stdoutTTY, c.run); got != c.want {
			t.Errorf("resumePlan(%d, interactive=%v, stdout tty=%v, --run=%v) = %+v, want %+v", c.shape, c.interactive, c.stdoutTTY, c.run, got, c.want)
		}
	}
}

func TestResumeNeverRunsATopHit(t *testing.T) {
	for s := nothing; s <= query; s++ {
		for _, interactive := range []bool{false, true} {
			for _, stdoutTTY := range []bool{false, true} {
				for _, run := range []bool{false, true} {
					if p := resumePlan(s, interactive, stdoutTTY, run); p.then == byRank && p.exec {
						t.Errorf("resumePlan(%d, %v, %v, %v) runs the top hit of a search", s, interactive, stdoutTTY, run)
					}
				}
			}
		}
	}
}

func TestResumeTellsASessionFromASearch(t *testing.T) {
	cases := []struct {
		tokens []string
		want   named
	}{
		{nil, named{nothing, ""}},
		{[]string{"22fa4000"}, named{exact, "22fa4000"}},
		{[]string{"deadbeef-00"}, named{exact, "deadbeef-00"}},
		{[]string{"0199000"}, named{word, "0199000"}},
		{[]string{"coachspect"}, named{word, "coachspect"}},
		{[]string{"notes.v1"}, named{word, "notes.v1"}},
		{[]string{"foo:bar"}, named{word, "foo:bar"}},
		{[]string{"-"}, named{exact, "-"}},
		{[]string{`"coachspect"`}, named{exact, `"coachspect"`}},
		{[]string{"./a.jsonl"}, named{exact, "./a.jsonl"}},
		{[]string{"~/x"}, named{exact, "~/x"}},
		{[]string{"a/b"}, named{exact, "a/b"}},
		{[]string{"x.jsonl"}, named{exact, "x.jsonl"}},
		{[]string{"in:here"}, named{query, "in:here"}},
		{[]string{"after:3days"}, named{query, "after:3days"}},
		{[]string{"file:internal/"}, named{query, "file:internal/"}},
		{[]string{"coachspect", "v2"}, named{query, "coachspect v2"}},
		{[]string{"22fa4000", "api"}, named{query, "22fa4000 api"}},
	}
	for _, c := range cases {
		if got := nameOf(c.tokens); got != c.want {
			t.Errorf("nameOf(%q) = %+v, want %+v", c.tokens, got, c.want)
		}
	}
}
