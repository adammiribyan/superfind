package main

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/adammiribyan/superfind/internal/release"
)

func main() {
	keys, args := release.Trusted(), os.Args[1:]
	if len(args) == 4 && args[0] == "-pubkey" {
		keys, args = readKey(args[1]), args[2:]
	}
	if len(args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: SUPERFIND_SIGNING_KEY=... go run ./internal/release/cmd/sign [-pubkey FILE] VERSION DIR")
		os.Exit(2)
	}
	v, err := release.ParseVersion(args[0])
	if err != nil {
		fail(err)
	}
	dir := args[1]
	secret, err := base64.StdEncoding.DecodeString(strings.TrimSpace(os.Getenv("SUPERFIND_SIGNING_KEY")))
	if err != nil {
		fail(fmt.Errorf("SUPERFIND_SIGNING_KEY is not base64: %w", err))
	}
	key, err := release.ParseSecretKey(secret)
	if err != nil {
		fail(fmt.Errorf("SUPERFIND_SIGNING_KEY: %w", err))
	}
	if !keys.Trusts(key.Public()) {
		fail(fmt.Errorf("SUPERFIND_SIGNING_KEY is key %s, which is not a trusted release key, so no superfind would accept its signatures", key.Public().ID))
	}
	checksums, err := os.ReadFile(filepath.Join(dir, "checksums.txt"))
	if err != nil {
		fail(err)
	}
	if err := release.CheckAssets(dir, checksums); err != nil {
		fail(err)
	}
	sig := release.Sign(checksums, key, v)
	if _, err := keys.Verify(checksums, sig, v); err != nil {
		fail(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "checksums.txt.minisig"), sig, 0o644); err != nil {
		fail(err)
	}
	fmt.Printf("signed checksums.txt for superfind %s with release key %s\n", v, key.Public().ID)
}

func readKey(path string) release.Keys {
	file, err := os.ReadFile(path)
	if err != nil {
		fail(err)
	}
	k, err := release.ParsePublicKey(file)
	if err != nil {
		fail(fmt.Errorf("%s: %w", path, err))
	}
	return release.Keys{k.ID: k}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "sign:", err)
	os.Exit(1)
}
