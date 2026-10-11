package telemetry

import (
	"encoding/json"
	"fmt"
)

type Event interface {
	Name() string
	event()
}

var Events = []Event{Upgraded{}}

type Upgraded struct {
	Method  Method  `json:"method"`
	Outcome Outcome `json:"outcome"`
}

func (Upgraded) Name() string { return "upgraded" }
func (Upgraded) event()       {}

type enum interface{ names() []string }

type Method uint8

const (
	_ Method = iota
	Script
	Homebrew
	GoInstall
	Checkout
	Unknown
)

var methodNames = []string{"", "script", "homebrew", "go", "checkout", "unknown"}

func (m Method) names() []string              { return methodNames }
func (m Method) MarshalJSON() ([]byte, error) { return enumJSON(m, uint8(m)) }

func ParseMethod(name string) Method {
	for i, known := range methodNames[1:] {
		if known == name {
			return Method(i + 1)
		}
	}
	return Unknown
}

type Outcome uint8

const (
	_ Outcome = iota
	Installed
	Unchanged
	Failed
)

var outcomeNames = []string{"", "installed", "unchanged", "failed"}

func (o Outcome) names() []string              { return outcomeNames }
func (o Outcome) MarshalJSON() ([]byte, error) { return enumJSON(o, uint8(o)) }

func enumJSON(e enum, i uint8) ([]byte, error) {
	names := e.names()
	if i == 0 || int(i) >= len(names) {
		return nil, fmt.Errorf("telemetry: %T has no value %d", e, i)
	}
	return json.Marshal(names[i])
}
