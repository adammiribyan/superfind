package upgrade

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"

	"github.com/adammiribyan/superfind/internal/agent"
	"github.com/adammiribyan/superfind/internal/release"
	"github.com/adammiribyan/superfind/internal/store"
)

type Method string

const (
	Script    Method = "script"
	Homebrew  Method = "homebrew"
	GoInstall Method = "go"
	Checkout  Method = "checkout"
	Unknown   Method = "unknown"
)

type Install struct {
	Method Method
	Path   string
	Clone  string
}

type Receipt struct {
	Method Method `json:"method"`
	Path   string `json:"path"`
	Clone  string `json:"clone,omitempty"`
}

type Facts struct {
	Executable string
	Receipt    *Receipt
	ModuleSum  string
	FromClone  bool
}

func Detect(f Facts) Install {
	switch {
	case strings.Contains(filepath.ToSlash(f.Executable), "/Caskroom/superfind/"):
		return Install{Method: Homebrew, Path: f.Executable}
	case f.Receipt != nil && f.Receipt.Path == f.Executable:
		return Install{Method: f.Receipt.Method, Path: f.Executable, Clone: f.Receipt.Clone}
	case f.ModuleSum != "":
		return Install{Method: GoInstall, Path: f.Executable}
	case f.FromClone:
		return Install{Method: Checkout, Path: f.Executable}
	}
	return Install{Method: Unknown, Path: f.Executable}
}

func Gather() (Facts, error) {
	exe, err := os.Executable()
	if err != nil {
		return Facts{}, err
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	f := Facts{Executable: exe, Receipt: ReadReceipt()}
	if info, ok := debug.ReadBuildInfo(); ok {
		f.ModuleSum = info.Main.Sum
		for _, s := range info.Settings {
			f.FromClone = f.FromClone || s.Key == "vcs.revision"
		}
	}
	return f, nil
}

const receiptName = "install.json"

func ReadReceipt() *Receipt {
	raw, err := os.ReadFile(store.StatePath(receiptName))
	if err != nil {
		return nil
	}
	var r Receipt
	if json.Unmarshal(raw, &r) != nil || (r.Method != Script && r.Method != Checkout) || !filepath.IsAbs(r.Path) {
		return nil
	}
	if real, err := filepath.EvalSymlinks(r.Path); err == nil {
		r.Path = real
	}
	return &r
}

func WriteReceipt(r Receipt) error {
	var body bytes.Buffer
	enc := json.NewEncoder(&body)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(r); err != nil {
		return err
	}
	return store.WriteAtomically(store.StatePath(receiptName), body.Bytes())
}

const (
	brewCommand = "brew upgrade --cask adammiribyan/tap/superfind"
	curlCommand = "curl -fsSL https://superfind.dev/install | sh"
	goModule    = "github.com/adammiribyan/superfind"
)

func CurlCommand(v *release.Version) string {
	if v == nil {
		return curlCommand
	}
	return "curl -fsSL https://superfind.dev/install | SUPERFIND_VERSION=" + v.String() + " sh"
}

func (i Install) Command(v *release.Version) string {
	switch i.Method {
	case Homebrew:
		return brewCommand
	case GoInstall:
		if v == nil {
			return "go install " + goModule + "@latest"
		}
		return "go install " + goModule + "@v" + v.String()
	case Checkout:
		in := ""
		if i.Clone != "" {
			in = " -C " + agent.ShellQuote(i.Clone)
		}
		script := "./install.sh"
		if i.Clone != "" {
			script = agent.ShellQuote(i.Clone) + "/install.sh"
		}
		if v == nil {
			return "git" + in + " pull && " + script
		}
		return "git" + in + " fetch --tags && git" + in + " checkout v" + v.String() + " && " + script
	}
	return CurlCommand(v)
}

func (i Install) origin() string {
	switch i.Method {
	case Homebrew:
		return "Homebrew installed this copy"
	case GoInstall:
		return "go install built this copy"
	case Checkout:
		if i.Clone != "" {
			return "this copy was built from the superfind clone at " + i.Clone
		}
		return "this copy was built from a clone of superfind"
	}
	return "superfind doesn't know what installed this copy at " + i.Path
}

func (i Install) leftAlone(v *release.Version) string {
	what := "To upgrade"
	if v != nil {
		what = "To install " + v.String()
	}
	switch i.Method {
	case Unknown:
		what += " in ~/.local/bin"
	case Checkout:
		if i.Clone == "" {
			what += ", in that clone"
		}
	}
	return i.origin() + ", so superfind left it alone. " + what + ":\n  " + i.Command(v)
}
