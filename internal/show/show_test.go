package show

import (
	"regexp"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

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
		{"Authorization: Bearer " + strings.Repeat("j7", 15), "Authorization: Bearer [masked bearer token]"},
		{"redis://:redispass123@cache/0", "redis://:[masked password in a URL]@cache/0"},
		{"export OPENAI_API_KEY=sk-proj-" + strings.Repeat("B", 48), "export OPENAI_API_KEY=[masked OpenAI key]"},
		{"ANTHROPIC_API_KEY=sk-ant-api03-" + strings.Repeat("a", 40), "ANTHROPIC_API_KEY=[masked Anthropic key]"},
		{"DB_PASSWORD=correcthorse", "DB_PASSWORD=[masked secret]"},
		{"[db]\npassword = s3cretvalue\n", "[db]\npassword = [masked secret]\n"},
		{"set `CHAT_API_KEY=abc123def456` first", "set `CHAT_API_KEY=[masked secret]` first"},
		{`"api_key": "sk-ant-api03-` + strings.Repeat("a", 40) + `"`, `"api_key": "[masked Anthropic key]"`},
		{"postgres://app:s3cretpass@db.internal:5432/x", "postgres://app:[masked password in a URL]@db.internal:5432/x"},
		{`password = "hunter2hunter2"`, `password = "[masked secret]"`},
		{`"api_key": "abcd1234efgh5678"`, `"api_key": "[masked secret]"`},
		{"CLIENT_SECRET=0123456789abcdef", "CLIENT_SECRET=[masked secret]"},
		{"-----BEGIN RSA PRIVATE KEY-----\nMIIEow\n-----END RSA PRIVATE KEY-----\nafter", "[masked private key]\nafter"},
		{"-----BEGIN OPENSSH PRIVATE KEY-----\nb3Blbn", "[masked private key]"},
		{`password="correct horse battery staple" next`, `password="[masked secret]" next`},
		{`secret='it\'s a secret' ok`, `secret='[masked secret]' ok`},
		{"password=hunter2", "password=[masked secret]"},
		{"DATABASE_PASSWORD=correcthorsebattery", "DATABASE_PASSWORD=[masked secret]"},
		{"export OPENAI_API_KEY=abc123def456", "export OPENAI_API_KEY=[masked secret]"},
		{"password=abcdefgh,timeout=30", "password=[masked secret],timeout=30"},
		{"user=a&password=pa55word&next=1", "user=a&password=[masked secret]&next=1"},
		{"password: hunter2", "password: [masked secret]"},
		{"passphrase: correcthorsebatterystaple", "passphrase: [masked secret]"},
	}
	for _, c := range cases {
		if got := Mask(c.in); got != c.want {
			t.Errorf("Mask(%q)\n got %q\nwant %q", c.in, got, c.want)
		}
	}
}

func TestMaskHidesAKeyWholeEvenWhenPartOfItLooksLikeAnotherKey(t *testing.T) {
	cases := []struct{ in, want string }{
		{"xoxb-AKIABBBBBBBBBBBBBBBB-abcdefghij", "[masked Slack token]"},
		{"AIza-AKIABBBBBBBBBBBBBBBB-abcdefghijklm", "[masked Google API key]"},
		{"Authorization: Bearer abc1-AKIABBBBBBBBBBBBBBBB-tokenrest", "Authorization: Bearer [masked bearer token]"},
		{"Authorization: Bearer sk-ant-" + strings.Repeat("a", 30), "Authorization: Bearer [masked Anthropic key]"},
		{"AKIABBBBBBBBBBBBBBBB then xoxb-1234567890-abcdefghij", "[masked AWS access key] then [masked Slack token]"},
		{"password=-----BEGIN RSA PRIVATE KEY-----\nMIIEow\n-----END RSA PRIVATE KEY-----", "password=[masked private key]"},
		{"xoxb-abcdefghij-----BEGIN RSA PRIVATE KEY-----\nMIIEow\n-----END RSA PRIVATE KEY-----", "[masked Slack token][masked private key]"},
		{"sk-proj-" + strings.Repeat("B", 40) + "-----BEGIN OPENSSH PRIVATE KEY-----\nb3Blbn", "[masked OpenAI key][masked private key]"},
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
		"password: required",
		"secret: true",
		"password=$DB_PASSWORD and api_key=<your key> and token_secret={{ vault }}",
		`password: "" and secret=*** and api_key=...`,
		"password_hash: bcrypt",
		"secret:\n    - RAILS_MASTER_KEY",
		"run `TYPESAFE_API_KEY= superfind ask ledger`",
		"password =\nnext line",
		"client = OpenAI(api_key=key)",
		`api_key=os.environ["OPENAI_API_KEY"]`,
		"password=None, secret=settings.SECRET, password=password",
		"password = getpass()",
		"password: process.env.DB_PASSWORD",
		"Bearer authentication-is-the-scheme",
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
	if got := c.Turns[0].Text; got != "…rld" || !c.Turns[0].Cut {
		t.Fatalf("cut turn = %+v", c.Turns[0])
	}
}

func TestSelectWholeTurnsAreNeverMarkedCut(t *testing.T) {
	for _, turn := range Select(turns("aaaa", "bbbb", "cccc"), Options{MaxBytes: 8}).Turns {
		if turn.Cut {
			t.Fatalf("%+v is marked cut", turn)
		}
	}
}

func TestSelectAppliesTailThenTheByteLimit(t *testing.T) {
	c := Select(turns("aaaa", "bbbb", "cccc", "dddd"), Options{Tail: 3, MaxBytes: 8})
	if strings.Join(texts(c), ",") != "cccc,dddd" || c.Omitted != 2 || !c.Truncated {
		t.Fatalf("got %+v", c)
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

type keyFormat struct {
	prefix, alphabet string
	least, exact     int
	endsAtWord       bool
}

const (
	alphanumeric = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	urlSafe      = alphanumeric + "_-"
)

var keyFormats = []keyFormat{
	{prefix: "sk-ant-api03-", alphabet: urlSafe, least: 20},
	{prefix: "sk-proj-", alphabet: urlSafe, least: 32},
	{prefix: "sk-", alphabet: urlSafe, least: 32},
	{prefix: "xai-", alphabet: alphanumeric, least: 32},
	{prefix: "ghp_", alphabet: alphanumeric, least: 36},
	{prefix: "github_pat_", alphabet: alphanumeric + "_", least: 40},
	{prefix: "AKIA", alphabet: "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789", exact: 16, endsAtWord: true},
	{prefix: "xoxb-", alphabet: alphanumeric + "-", least: 10},
	{prefix: "sk_live_", alphabet: alphanumeric, least: 16},
	{prefix: "AIza", alphabet: urlSafe, exact: 35},
	{prefix: "phc_", alphabet: alphanumeric, least: 30},
}

func FuzzMaskHidesEveryKeyWrittenAnywhere(f *testing.F) {
	var every []byte
	for _, format := range keyFormats {
		every = append(every, byte(len(format.alphabet)-1))
	}
	for i := range 256 {
		every = append(every, byte(i))
	}
	for _, before := range []string{"key ", "export TOKEN=", `"api_key": "`, "Authorization: Bearer ", "password=", "(", "\n", ""} {
		for _, after := range []string{" done", "_x", "-", "", "\"", "x"} {
			for kind := range keyFormats {
				f.Add(before, after, uint8(kind), uint8(len(after)), every)
			}
		}
	}
	f.Fuzz(func(t *testing.T, before, after string, kind, extra uint8, body []byte) {
		format := keyFormats[int(kind)%len(keyFormats)]
		length := format.exact
		if length == 0 {
			length = format.least + int(extra)%40
		}
		key := []byte(format.prefix)
		for i := range length {
			b := byte(i)
			if len(body) > 0 {
				b = body[i%len(body)] + byte(i/len(body))
			}
			key = append(key, format.alphabet[int(b)%len(format.alphabet)])
		}
		if word(lastRune(before)) || format.endsAtWord && word(firstRune(after)) {
			t.Skip()
		}
		out, last := maskLabel.ReplaceAllString(Mask(before+string(key)+after), "]"), string(key[len(key)-1:])
		if after == "" && strings.HasSuffix(out, last) || after != "" && strings.Contains(out, last+after) && !strings.Contains(before+after, last+after) {
			t.Fatalf("Mask(%q) = %q still shows the end of the key", before+string(key)+after, out)
		}
	})
}

var maskLabel = regexp.MustCompile(`\[masked [^\]]*\]`)

func word(r rune) bool { return r == '_' || r < 0x80 && (unicode.IsLetter(r) || unicode.IsDigit(r)) }

func lastRune(s string) rune {
	r, _ := utf8.DecodeLastRuneInString(s)
	return r
}

func firstRune(s string) rune {
	r, _ := utf8.DecodeRuneInString(s)
	return r
}
