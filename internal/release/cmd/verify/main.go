package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/adammiribyan/superfind/internal/release"
)

func main() {
	keys, args := release.Trusted(), os.Args[1:]
	if len(args) == 4 && args[0] == "-pubkey" {
		keys, args = readKey(args[1]), args[2:]
	}
	if len(args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: go run ./internal/release/cmd/verify [-pubkey FILE] VERSION DIR")
		os.Exit(2)
	}
	v, err := release.ParseVersion(args[0])
	if err != nil {
		fail(err)
	}
	dir := args[1]
	checksums, err := os.ReadFile(filepath.Join(dir, "checksums.txt"))
	if err != nil {
		fail(err)
	}
	sig, err := os.ReadFile(filepath.Join(dir, "checksums.txt.minisig"))
	if err != nil {
		fail(err)
	}
	id, err := keys.Verify(checksums, sig, v)
	if err != nil {
		fail(err)
	}
	if err := release.CheckAssets(dir, checksums); err != nil {
		fail(err)
	}
	fmt.Printf("checksums.txt for superfind %s is signed by release key %s, and every file it names matches\n", v, id)
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
	fmt.Fprintln(os.Stderr, "verify:", err)
	os.Exit(1)
}
