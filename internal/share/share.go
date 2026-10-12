package share

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"time"

	"github.com/adammiribyan/superfind/internal/store"
)

const (
	Version  = 1
	MaxBytes = 1 << 20
)

var Omissions = []string{"raw transcript", "tool output", "file contents", "uncommitted changes"}

type Manifest struct {
	Version         int       `json:"superfind_share"`
	BundleID        string    `json:"bundle_id"`
	CreatedAt       time.Time `json:"created_at"`
	SenderName      string    `json:"sender_name"`
	Agent           string    `json:"agent"`
	AgentLabel      string    `json:"agent_label"`
	HarnessVersion  string    `json:"harness_version"`
	Title           string    `json:"title"`
	SourceSessionID string    `json:"source_session_id"`
	RepoRemote      string    `json:"repo_remote"`
	RepoSubdir      string    `json:"repo_subdir"`
	Branch          string    `json:"branch"`
	HeadCommit      string    `json:"head_commit"`
	PRRefs          []string  `json:"pr_refs"`
	TurnsTotal      int       `json:"turns_total"`
	TurnsShown      int       `json:"turns_shown"`
	MaskedCount     int       `json:"masked_count"`
	Omissions       []string  `json:"omissions"`
	BriefingSHA256  string    `json:"briefing_sha256"`
}

type Share struct {
	Manifest Manifest
	Briefing string
}

var (
	ErrTooLarge      = errors.New("it is larger than 1 MiB")
	ErrNoFrontMatter = errors.New("it doesn't start with a superfind share header")
	ErrFrontMatter   = errors.New("its header can't be read")
	ErrMissingKey    = errors.New("its header has no")
	ErrVersion       = errors.New("it is a newer share format than this superfind reads")
	ErrHash          = errors.New("its briefing doesn't match its checksum, so it changed or was cut short after it was shared")
)

var required = []string{"superfind_share", "bundle_id", "created_at", "agent", "title", "briefing_sha256"}

var (
	keyName  = regexp.MustCompile(`^[a-z0-9_]+$`)
	bundleID = regexp.MustCompile(`^[0-9a-f]{32}$`)
	sha      = regexp.MustCompile(`^[0-9a-f]{64}$`)
	commit   = regexp.MustCompile(`^(?:[0-9a-f]{7,64})?$`)
)

func NewID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func Sum(briefing string) string {
	h := sha256.Sum256([]byte(briefing))
	return hex.EncodeToString(h[:])
}

func lf(s string) string { return strings.ReplaceAll(s, "\r\n", "\n") }

func Write(w io.Writer, s Share) error {
	m, body := s.Manifest, lf(s.Briefing)
	m.BriefingSHA256 = Sum(body)
	if m.PRRefs == nil {
		m.PRRefs = []string{}
	}
	if m.Omissions == nil {
		m.Omissions = []string{}
	}
	var out bytes.Buffer
	out.WriteString("---\n")
	v := reflect.ValueOf(m)
	for i := range v.NumField() {
		var value bytes.Buffer
		enc := json.NewEncoder(&value)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(v.Field(i).Interface()); err != nil {
			return err
		}
		fmt.Fprintf(&out, "%s: %s", v.Type().Field(i).Tag.Get("json"), value.Bytes())
	}
	out.WriteString("---\n")
	out.WriteString(body)
	_, err := w.Write(out.Bytes())
	return err
}

func Parse(raw []byte) (Share, error) {
	if len(raw) > MaxBytes {
		return Share{}, ErrTooLarge
	}
	head, ok := strings.CutPrefix(lf(string(raw)), "---\n")
	end := strings.Index("\n"+head, "\n---\n")
	if !ok || end < 0 {
		return Share{}, ErrNoFrontMatter
	}
	front, body := "", head[end+4:]
	if end > 0 {
		front = head[:end-1]
	}
	fields := map[string]json.RawMessage{}
	for n, line := range strings.Split(front, "\n") {
		key, value, ok := strings.Cut(line, ": ")
		switch {
		case !ok || !keyName.MatchString(key) || !json.Valid([]byte(value)):
			return Share{}, fmt.Errorf("%w: line %d isn't a key and a JSON value", ErrFrontMatter, n+2)
		case fields[key] != nil:
			return Share{}, fmt.Errorf("%w: %s appears twice", ErrFrontMatter, key)
		}
		fields[key] = json.RawMessage(value)
	}
	for _, key := range required {
		if fields[key] == nil {
			return Share{}, fmt.Errorf("%w %s", ErrMissingKey, key)
		}
		if key == "superfind_share" && string(fields[key]) != "1" {
			return Share{}, fmt.Errorf("%w: format %s, and this superfind reads format %d", ErrVersion, fields[key], Version)
		}
	}
	object, _ := json.Marshal(fields)
	var m Manifest
	if err := json.Unmarshal(object, &m); err != nil {
		var wrongType *json.UnmarshalTypeError
		if errors.As(err, &wrongType) {
			return Share{}, fmt.Errorf("%w: %s should be a %s", ErrFrontMatter, wrongType.Field, wrongType.Type)
		}
		return Share{}, fmt.Errorf("%w: %v", ErrFrontMatter, err)
	}
	for _, check := range []struct {
		key string
		ok  bool
	}{
		{"bundle_id", bundleID.MatchString(m.BundleID)},
		{"briefing_sha256", sha.MatchString(m.BriefingSHA256)},
		{"head_commit", commit.MatchString(m.HeadCommit)},
		{"repo_subdir", m.RepoSubdir == "" || filepath.IsLocal(m.RepoSubdir)},
		{"repo_remote", m.RepoRemote == "" || Remote(m.RepoRemote) == m.RepoRemote},
	} {
		if !check.ok {
			return Share{}, fmt.Errorf("%w: %s isn't valid", ErrFrontMatter, check.key)
		}
	}
	if Sum(body) != m.BriefingSHA256 {
		return Share{}, ErrHash
	}
	return Share{m, body}, nil
}

var scpLike = regexp.MustCompile(`^(?:[^@/]+@)?([^:/]+):(.+)$`)

func Remote(raw string) string {
	raw = strings.TrimSpace(raw)
	if !strings.Contains(raw, "://") {
		m := scpLike.FindStringSubmatch(raw)
		if m == nil {
			return ""
		}
		raw = "ssh://" + m[1] + "/" + strings.TrimPrefix(m[2], "/")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	switch u.Scheme {
	case "https", "http":
		if port := u.Port(); port != "" {
			host += ":" + port
		}
	case "ssh", "git", "git+ssh", "ssh+git":
	default:
		return ""
	}
	return "https://" + host + strings.TrimSuffix(strings.TrimSuffix(u.Path, "/"), ".git")
}

func ScrubHome(text, home string) string {
	home = filepath.Clean(home)
	if home == "/" || home == "." || !strings.Contains(text, home) {
		return text
	}
	return regexp.MustCompile(regexp.QuoteMeta(home)+`\b`).ReplaceAllLiteralString(text, "~")
}

type receipt struct {
	Manifest   Manifest  `json:"manifest"`
	ReceivedAt time.Time `json:"received_at"`
}

func Remember(m Manifest, now time.Time) (bool, error) {
	data, err := json.Marshal(receipt{m, now.UTC()})
	if err != nil {
		return false, err
	}
	return store.CreateNew(store.CachePath(filepath.Join("received", m.BundleID+".json")), data)
}
