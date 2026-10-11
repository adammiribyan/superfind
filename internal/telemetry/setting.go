package telemetry

import (
	"os"
	"strconv"
	"strings"

	"github.com/adammiribyan/superfind/internal/config"
	"github.com/adammiribyan/superfind/internal/store"
)

type Setting struct{ why string }

func (s Setting) On() bool    { return s.why == "" }
func (s Setting) Why() string { return s.why }

func (s Setting) Enabled() (Enabled, bool) { return Enabled{on: s.On()}, s.On() }

type Enabled struct{ on bool }

type Env func(name string) (string, bool)

const Variable = "SUPERFIND_TELEMETRY"

var ciVariables = []string{"CI", "GITHUB_ACTIONS", "GITLAB_CI", "BUILDKITE", "CIRCLECI", "JENKINS_URL", "TF_BUILD", "TEAMCITY_VERSION"}

func Resolve(env, file Env, stateWritable func() bool) Setting {
	if value, set := env(Variable); set {
		if !on(value) {
			return Setting{Variable + "=" + strconv.Quote(value) + " is in your environment"}
		}
	} else if value, set := file(Variable); set && !on(value) {
		return Setting{Variable + "=" + strconv.Quote(value) + " is in ~/.config/superfind/env"}
	}
	if value, set := env("DO_NOT_TRACK"); set && truthy(value) {
		return Setting{"DO_NOT_TRACK is set"}
	}
	for _, name := range ciVariables {
		if value, set := env(name); set && truthy(value) {
			return Setting{name + " is set, so this looks like CI"}
		}
	}
	if !stateWritable() {
		return Setting{"superfind can't write to its state folder, so it has nowhere to keep an install ID"}
	}
	return Setting{}
}

func on(value string) bool {
	return value == "" || value == "1" || strings.EqualFold(value, "on") || strings.EqualFold(value, "true")
}

func truthy(value string) bool {
	return value != "" && value != "0" && !strings.EqualFold(value, "false")
}

func Current() Setting {
	return Resolve(os.LookupEnv, config.Lookup, func() bool { return store.Writable(store.StateDir()) })
}

func TurnOff() error {
	if err := config.Set(Variable, "off"); err != nil {
		return err
	}
	for _, name := range []string{idFile, checkFile} {
		if err := os.Remove(store.StatePath(name)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return os.RemoveAll(store.StatePath(eventsDir))
}

func TurnOn() error { return config.Set(Variable, "") }
