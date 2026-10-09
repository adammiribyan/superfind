package typesafe

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const jevURL = "https://api.typesafe.ai/v1/systemone"

var envKey = regexp.MustCompile(`(?m)^\s*(?:export\s+)?TYPESAFE_API_KEY\s*=\s*["']?([^"'\s]+)`)

func Key() string {
	if value, set := os.LookupEnv("TYPESAFE_API_KEY"); set {
		return value
	}
	var places []string
	if exe, err := os.Executable(); err == nil {
		if real, err := filepath.EvalSymlinks(exe); err == nil {
			places = append(places, filepath.Join(filepath.Dir(real), ".env"), filepath.Join(filepath.Dir(filepath.Dir(real)), ".env"))
		}
	}
	home, _ := os.UserHomeDir()
	places = append(places, filepath.Join(home, ".config", "hop", "env"))
	for _, p := range places {
		if raw, err := os.ReadFile(p); err == nil {
			if m := envKey.FindSubmatch(raw); m != nil {
				return string(m[1])
			}
		}
	}
	return ""
}

var UserAgent string

var httpClient = &http.Client{Timeout: 20 * time.Second}

type Answer struct {
	Choice        string             `json:"choice"`
	Probabilities map[string]float64 `json:"probabilities"`
	Noul          float64            `json:"noul"`
}

type Option struct {
	Key, Meaning string
}

type Options []Option

func (o Options) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, opt := range o {
		if i > 0 {
			buf.WriteByte(',')
		}
		encoder := json.NewEncoder(&buf)
		encoder.SetEscapeHTML(false)
		encoder.Encode(opt.Key)
		buf.Truncate(buf.Len() - 1)
		buf.WriteByte(':')
		encoder.Encode(opt.Meaning)
		buf.Truncate(buf.Len() - 1)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

func asciiJSON(v any) []byte {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	encoder.Encode(v)
	var out strings.Builder
	for _, r := range strings.TrimRight(buf.String(), "\n") {
		switch {
		case r < 0x80:
			out.WriteRune(r)
		case r > 0xFFFF:
			r -= 0x10000
			fmt.Fprintf(&out, "\\u%04x\\u%04x", 0xD800+(r>>10), 0xDC00+(r&0x3FF))
		default:
			fmt.Fprintf(&out, "\\u%04x", r)
		}
	}
	return []byte(out.String())
}

func Jev(key string, state any, questions map[string]any) (map[string]Answer, error) {
	body := asciiJSON(map[string]any{"model": "jev-latest", "state": state, "questions": questions})
	url := jevURL
	if override := os.Getenv("TYPESAFE_URL"); override != "" {
		url = override
	}
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", UserAgent)
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("TypeSafe answered %d", resp.StatusCode)
	}
	var out struct {
		Answers map[string]Answer `json:"answers"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out.Answers, nil
}
