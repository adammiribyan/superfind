package session

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

func Piped(r io.Reader) (string, error) {
	lines := bufio.NewReader(r)
	for {
		line, err := lines.ReadString('\n')
		if line = strings.TrimSpace(line); line != "" {
			io.Copy(io.Discard, lines)
			return reference(line)
		}
		if errors.Is(err, io.EOF) {
			return "", nil
		}
		if err != nil {
			return "", err
		}
	}
}

func reference(line string) (string, error) {
	if strings.HasPrefix(line, "{") {
		var named struct{ Path, ID string }
		if err := json.Unmarshal([]byte(line), &named); err != nil {
			return "", errors.New("the JSON line on standard input couldn't be read: " + err.Error())
		}
		switch {
		case named.Path != "":
			return named.Path, nil
		case named.ID != "":
			return named.ID, nil
		}
		return "", errors.New("the JSON line on standard input has no path or id")
	}
	first, _, _ := strings.Cut(line, "\t")
	return first, nil
}
