package telemetry

import (
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/adammiribyan/superfind/internal/store"
)

const idFile = "id"

var uuidV4 = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func LoadID() (string, bool) {
	raw, err := os.ReadFile(store.StatePath(idFile))
	id := strings.TrimSpace(string(raw))
	if err != nil || !uuidV4.MatchString(id) {
		return "", false
	}
	return id, true
}

func EnsureID() (string, error) {
	held, err := store.CreateOnce(store.StatePath(idFile), []byte(newUUID()+"\n"))
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(string(held))
	if !uuidV4.MatchString(id) {
		return "", errors.New(store.StatePath(idFile) + " doesn't hold an install ID")
	}
	return id, nil
}

func newUUID() string {
	var b [16]byte
	rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}
