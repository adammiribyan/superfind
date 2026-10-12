package mask

import (
	"regexp"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
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
		{"tskey-auth-k1A2b3C4CNTRL-" + strings.Repeat("Tsk9", 8), "[masked Tailscale key]"},
		{"TS_AUTHKEY=tskey-client-kAbCdEf0CNTRL-" + strings.Repeat("c7", 16) + " up", "TS_AUTHKEY=[masked Tailscale key] up"},
		{"curl -u \"tskey-api-kXyZ123CNTRL-" + strings.Repeat("Ap1", 11) + ":\" https://api.tailscale.com/api/v2/tailnet", "curl -u \"[masked Tailscale key]:\" https://api.tailscale.com/api/v2/tailnet"},
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
		{"curl -X POST https://hooks.slack" + ".com/services/T0ABCDEF1/B0ABCDEF2/" + strings.Repeat("abcdefgh", 3) + " -d @msg.json",
			"curl -X POST https://[masked Slack webhook] -d @msg.json"},
		{`SLACK_WEBHOOK_URL: "https://hooks.slack` + `.com/triggers/T0ABCDEF1/1234567890123/` + strings.Repeat("0123456789abcdef", 2) + `"`,
			`SLACK_WEBHOOK_URL: "https://[masked Slack webhook]"`},
		{"https://discord.com/api/webhooks/123456789012345678/" + strings.Repeat("aB3_-", 13) + "xyz done",
			"https://[masked Discord webhook] done"},
		{"(https://canary.discordapp.com/api/webhooks/123456789012345678/" + strings.Repeat("Zz9", 20) + ")",
			"(https://[masked Discord webhook])"},
		{"https://example.webhook.office.com/webhookb2/0a1b2c3d-0000-4000-8000-0123456789ab@0a1b2c3d-1111-4111-8111-0123456789ab/IncomingWebhook/" + strings.Repeat("0123456789abcdef", 2) + "/0a1b2c3d-2222-4222-8222-0123456789ab next",
			"https://[masked Teams webhook] next"},
		{"https://chat.googleapis.com/v1/spaces/AAAAabcdefg/messages?key=AIza" + strings.Repeat("h", 35) + "&token=" + strings.Repeat("Tk3", 15) + "%3D",
			"https://chat.googleapis.com/v1/spaces/AAAAabcdefg/messages?key=[masked Google API key]&token=[masked token in a URL]"},
		{"https://example.logic.azure.com/workflows/abc/triggers/manual/paths/invoke?api-version=2016-06-01&sp=%2Ftriggers&sv=1.0&sig=" + strings.Repeat("Sg7-", 11) + " ok",
			"https://example.logic.azure.com/workflows/abc/triggers/manual/paths/invoke?api-version=2016-06-01&sp=%2Ftriggers&sv=1.0&sig=[masked token in a URL] ok"},
		{"https://discord.com/api/v10/webhooks/123456789012345678/" + strings.Repeat("Ab9", 20), "https://[masked Discord webhook]"},
		{"https://hooks.slack-gov" + ".com/services/T0ABCDEF1/B0ABCDEF2/" + strings.Repeat("abcdefgh", 3), "https://[masked Slack webhook]"},
		{"https://outlook.office.com/webhook/0a1b2c3d-0000-4000-8000-0123456789ab@0a1b2c3d-1111-4111-8111-0123456789ab/IncomingWebhook/" + strings.Repeat("0123456789abcdef", 2) + "/x",
			"https://[masked Teams webhook]"},
		{"https://bucket.s3.amazonaws.com/f?X-Amz-Credential=AKIA0000&amp;X-Amz-Signature=0a1b2c3d4e5f60718293a4b5c6d7e8f9",
			"https://bucket.s3.amazonaws.com/f?X-Amz-Credential=AKIA0000&amp;X-Amz-Signature=[masked token in a URL]"},
		{"https://gitlab.example.com/api/v4/projects?private_token=glpat0aB1cD2eF3gH4iJ5", "https://gitlab.example.com/api/v4/projects?private_token=[masked token in a URL]"},
		{"https://deploy:Hunter2pass@registry.example.com/v2/", "https://deploy:[masked password in a URL]@registry.example.com/v2/"},
		{"https://x:abc-ghp_" + strings.Repeat("e", 36) + "@github.com/o/r", "https://x:[masked password in a URL]@github.com/o/r"},
	}
	for _, c := range cases {
		if got := Secrets(c.in); got != c.want {
			t.Errorf("Secrets(%q)\n got %q\nwant %q", c.in, got, c.want)
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
		{"https://ghp_" + strings.Repeat("A", 36) + ":hunter22@example.com", "https://[masked GitHub token]:[masked password in a URL]@example.com"},
	}
	for _, c := range cases {
		if got := Secrets(c.in); got != c.want {
			t.Errorf("Secrets(%q)\n got %q\nwant %q", c.in, got, c.want)
		}
	}
}

func TestMaskLeavesOrdinaryTextAlone(t *testing.T) {
	for _, in := range []string{
		"we use sk-learn and scikit for this",
		"post to https://hooks.slack.com/services/ with your own webhook",
		"discord.com/api/webhooks/{id}/{token} takes JSON",
		"https://example.com/search?page=2&sort=name&key=value&token=abc123",
		"https://example.com/download?key=documentation.md",
		"use &token=YOUR_TOKEN_GOES_HERE in the example",
		"the task-ant-colony test",
		"tskey-auth-<id>-<secret> is the shape, tskey-auth-k123-short is too short",
		"set TS_AUTHKEY to a tskey-auth key from the admin console",
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
		if got := Secrets(in); got != in {
			t.Errorf("Secrets(%q) changed it to %q", in, got)
		}
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
	{prefix: "tskey-auth-k1A2b3C4CNTRL-", alphabet: alphanumeric, least: 16},
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
	for _, before := range []string{"key ", "export TOKEN=", `"api_key": "`, "Authorization: Bearer ", "password=", "(", "\n", "", "https://"} {
		for _, after := range []string{" done", "_x", "-", "", "\"", "x", ":hunter22@example.com"} {
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
		if word(lastRune(before)) || format.endsAtWord && word(firstRune(after)) || strings.Contains(before+after, "\x00") {
			t.Skip()
		}
		out, last, tail := maskLabel.ReplaceAllString(Secrets(before+string(key)+after), "\x00"), string(key[len(key)-1:]), string(key[len(key)-6:])
		if after == "" && strings.HasSuffix(out, last) || after != "" && strings.Contains(out, last+after) && !strings.Contains(before+after, last+after) ||
			strings.Contains(out, tail) && !strings.Contains(before+after, tail) {
			t.Fatalf("Secrets(%q) = %q still shows the end of the key", before+string(key)+after, out)
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

func TestMaskFindsSecretsThatOnlyCaseFoldingNames(t *testing.T) {
	cases := []struct{ in, want string }{
		{"auth_toKen=Zx9Qw8Er7Ty6Ui5Op4", "auth_toKen=[masked secret]"},
		{"paſſword=correct7horse", "paſſword=[masked secret]"},
		{"AUTHORIZATION: BEARER " + strings.Repeat("j7", 15), "AUTHORIZATION: BEARER [masked bearer token]"},
		{"get /x?TOKEN=" + strings.Repeat("a1", 10), "get /x?TOKEN=[masked token in a URL]"},
	}
	for _, c := range cases {
		if got := Secrets(c.in); got != c.want {
			t.Errorf("Secrets(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestCountSaysHowManySecretsWereMaskedNotHowManyMarkersThereAre(t *testing.T) {
	text := "keys sk-ant-api03-" + strings.Repeat("Q", 40) + " and password=hunter22, quoting [masked secret] from before"
	if got := Count(text); got != 2 {
		t.Errorf("Count = %d, want 2", got)
	}
	if got := Count("nothing here"); got != 0 {
		t.Errorf("Count of plain text = %d", got)
	}
}
