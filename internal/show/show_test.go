package show

import (
	"strings"
	"testing"

	"github.com/adammiribyan/superfind/internal/agent"
)

func turns(texts ...string) []agent.Turn {
	out := make([]agent.Turn, len(texts))
	for i, s := range texts {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		out[i] = agent.Turn{Role: role, Text: s}
	}
	return out
}

func pick(turns []agent.Turn, opt Options) Conversation {
	k := &keeper{opt: opt, every: 1}
	for _, t := range turns {
		k.say(t.Role, t.Text)
	}
	return k.conversation()
}

func texts(c Conversation) []string {
	out := make([]string, len(c.Turns))
	for i, t := range c.Turns {
		out[i] = t.Text
	}
	return out
}

func TestShowKeepsEverythingWithinTheLimit(t *testing.T) {
	c := pick(turns("one", "two", "three"), Options{MaxBytes: 100})
	if strings.Join(texts(c), ",") != "one,two,three" || c.Total != 3 || c.Omitted != 0 || c.Truncated {
		t.Fatalf("got %+v", c)
	}
}

func TestShowTailKeepsTheLastTurnsWithoutCallingItTruncated(t *testing.T) {
	c := pick(turns("one", "two", "three", "four"), Options{Tail: 2})
	if strings.Join(texts(c), ",") != "three,four" || c.Omitted != 2 || c.Truncated {
		t.Fatalf("got %+v", c)
	}
	if c.Turns[0].Role != "user" || c.Turns[1].Role != "assistant" {
		t.Fatalf("roles were lost: %+v", c.Turns)
	}
}

func TestShowTailLongerThanTheSessionKeepsItAll(t *testing.T) {
	if c := pick(turns("one", "two"), Options{Tail: 5}); len(c.Turns) != 2 || c.Omitted != 0 {
		t.Fatalf("got %+v", c)
	}
}

func TestShowByteLimitDropsTheEarliestWholeTurns(t *testing.T) {
	c := pick(turns("aaaa", "bbbb", "cccc"), Options{MaxBytes: 8})
	if strings.Join(texts(c), ",") != "bbbb,cccc" || c.Omitted != 1 || !c.Truncated {
		t.Fatalf("got %+v", c)
	}
}

func TestShowByteLimitExactlyFullIsNotTruncated(t *testing.T) {
	if c := pick(turns("aaaa", "bbbb"), Options{MaxBytes: 8}); c.Truncated || c.Omitted != 0 {
		t.Fatalf("got %+v", c)
	}
}

func TestShowCutsAnOversizedLastTurnFromItsStartOnARuneBoundary(t *testing.T) {
	c := pick(turns("earlier", "héllo wörld"), Options{MaxBytes: 4})
	if len(c.Turns) != 1 || !c.Truncated || c.Omitted != 1 {
		t.Fatalf("got %+v", c)
	}
	if got := c.Turns[0].Text; got != "…rld" || !c.Turns[0].Cut {
		t.Fatalf("cut turn = %+v", c.Turns[0])
	}
}

func TestShowWholeTurnsAreNeverMarkedCut(t *testing.T) {
	for _, turn := range pick(turns("aaaa", "bbbb", "cccc"), Options{MaxBytes: 8}).Turns {
		if turn.Cut {
			t.Fatalf("%+v is marked cut", turn)
		}
	}
}

func TestShowAppliesTailThenTheByteLimit(t *testing.T) {
	c := pick(turns("aaaa", "bbbb", "cccc", "dddd"), Options{Tail: 3, MaxBytes: 8})
	if strings.Join(texts(c), ",") != "cccc,dddd" || c.Omitted != 2 || !c.Truncated {
		t.Fatalf("got %+v", c)
	}
}

func TestShowZeroMaxBytesMeansNoLimit(t *testing.T) {
	long := strings.Repeat("x", DefaultMaxBytes*2)
	if c := pick(turns(long), Options{}); c.Truncated || c.Turns[0].Text != long {
		t.Fatal("a zero limit cut the text")
	}
}

func TestShowMasksAndStripsControlCharactersBeforeMeasuring(t *testing.T) {
	c := pick(turns("token ghp_"+strings.Repeat("z", 36)+"\x1b[31m red"), Options{MaxBytes: 1000})
	if got := c.Turns[0].Text; got != "token [masked GitHub token] red" {
		t.Fatalf("got %q", got)
	}
}
