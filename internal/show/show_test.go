package show

import (
	"strings"
	"testing"

	"github.com/adammiribyan/superfind/internal/agent"
)

func TestMaskReplacesEachKindOfSecret(t *testing.T) {
	cases := []struct{ in, want string }{
		{"key sk-ant-api03-" + strings.Repeat("a", 40) + " done", "key [masked Anthropic key] done"},
		{"sk-proj-" + strings.Repeat("B", 48), "[masked OpenAI key]"},
		{"sk-" + strings.Repeat("c", 48), "[masked OpenAI key]"},
		{"xai-" + strings.Repeat("D", 60), "[masked xAI key]"},
		{"ghp_" + strings.Repeat("e", 36), "[masked GitHub token]"},
		{"github_pat_" + strings.Repeat("F", 60), "[masked GitHub token]"},
		{"AKIAIOSFODNN7EXAMPLE", "[masked AWS access key]"},
		{"xoxb-1234567890-abcdefghij", "[masked Slack token]"},
		{"sk_live_" + strings.Repeat("g", 24), "[masked Stripe key]"},
		{"AIza" + strings.Repeat("h", 35), "[masked Google API key]"},
		{"phc_" + strings.Repeat("i", 40), "[masked PostHog key]"},
		{"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U", "[masked JSON web token]"},
		{"Authorization: Bearer " + strings.Repeat("j", 30), "Authorization: Bearer [masked bearer token]"},
		{"postgres://app:s3cretpass@db.internal:5432/x", "postgres://app:[masked password in a URL]@db.internal:5432/x"},
		{`password = "hunter2hunter2"`, `password = "[masked secret]"`},
		{`"api_key": "abcd1234efgh5678"`, `"api_key": "[masked secret]"`},
		{"CLIENT_SECRET=0123456789abcdef", "CLIENT_SECRET=[masked secret]"},
		{"-----BEGIN RSA PRIVATE KEY-----\nMIIEow\n-----END RSA PRIVATE KEY-----\nafter", "[masked private key]\nafter"},
		{"-----BEGIN OPENSSH PRIVATE KEY-----\nb3Blbn", "[masked private key]"},
	}
	for _, c := range cases {
		if got := Mask(c.in); got != c.want {
			t.Errorf("Mask(%q)\n got %q\nwant %q", c.in, got, c.want)
		}
	}
}

func TestMaskLeavesOrdinaryTextAlone(t *testing.T) {
	for _, in := range []string{
		"we use sk-learn and scikit for this",
		"the task-ant-colony test",
		"password: short",
		"set the password field to required",
		"AKIA is the prefix AWS uses",
		"session 01990001-a751-7860-aac9-738072c82895 resumed",
		"commit 9e4b45f0c1d2e3f4a5b6c7d8e9f00112233445566",
		"https://example.com/path@v2",
		"Bearer tokens expire after an hour",
		"ghp_short",
	} {
		if got := Mask(in); got != in {
			t.Errorf("Mask(%q) changed it to %q", in, got)
		}
	}
}

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

func texts(c Conversation) []string {
	out := make([]string, len(c.Turns))
	for i, t := range c.Turns {
		out[i] = t.Text
	}
	return out
}

func TestSelectKeepsEverythingWithinTheLimit(t *testing.T) {
	c := Select(turns("one", "two", "three"), Options{MaxBytes: 100})
	if strings.Join(texts(c), ",") != "one,two,three" || c.Total != 3 || c.Omitted != 0 || c.Truncated {
		t.Fatalf("got %+v", c)
	}
}

func TestSelectTailKeepsTheLastTurnsWithoutCallingItTruncated(t *testing.T) {
	c := Select(turns("one", "two", "three", "four"), Options{Tail: 2})
	if strings.Join(texts(c), ",") != "three,four" || c.Omitted != 2 || c.Truncated {
		t.Fatalf("got %+v", c)
	}
	if c.Turns[0].Role != "user" || c.Turns[1].Role != "assistant" {
		t.Fatalf("roles were lost: %+v", c.Turns)
	}
}

func TestSelectTailLongerThanTheSessionKeepsItAll(t *testing.T) {
	if c := Select(turns("one", "two"), Options{Tail: 5}); len(c.Turns) != 2 || c.Omitted != 0 {
		t.Fatalf("got %+v", c)
	}
}

func TestSelectByteLimitDropsTheEarliestWholeTurns(t *testing.T) {
	c := Select(turns("aaaa", "bbbb", "cccc"), Options{MaxBytes: 8})
	if strings.Join(texts(c), ",") != "bbbb,cccc" || c.Omitted != 1 || !c.Truncated {
		t.Fatalf("got %+v", c)
	}
}

func TestSelectByteLimitExactlyFullIsNotTruncated(t *testing.T) {
	if c := Select(turns("aaaa", "bbbb"), Options{MaxBytes: 8}); c.Truncated || c.Omitted != 0 {
		t.Fatalf("got %+v", c)
	}
}

func TestSelectCutsAnOversizedLastTurnFromItsStartOnARuneBoundary(t *testing.T) {
	c := Select(turns("earlier", "héllo wörld"), Options{MaxBytes: 4})
	if len(c.Turns) != 1 || !c.Truncated || c.Omitted != 1 {
		t.Fatalf("got %+v", c)
	}
	if got := c.Turns[0].Text; got != "…rld" {
		t.Fatalf("cut text = %q", got)
	}
}

func TestSelectZeroMaxBytesMeansNoLimit(t *testing.T) {
	long := strings.Repeat("x", DefaultMaxBytes*2)
	if c := Select(turns(long), Options{}); c.Truncated || c.Turns[0].Text != long {
		t.Fatal("a zero limit cut the text")
	}
}

func TestSelectMasksAndStripsControlCharactersBeforeMeasuring(t *testing.T) {
	c := Select(turns("token ghp_"+strings.Repeat("z", 36)+"\x1b[31m red"), Options{MaxBytes: 1000})
	if got := c.Turns[0].Text; got != "token [masked GitHub token] red" {
		t.Fatalf("got %q", got)
	}
}
