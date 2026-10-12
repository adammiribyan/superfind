package main

import (
	"fmt"
	"os"

	"github.com/adammiribyan/superfind/internal/release"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: go run ./internal/release/cmd/keygen SECRET_KEY_PATH > PUBLIC_KEY_PATH")
		os.Exit(2)
	}
	k, err := release.GenerateKey()
	if err != nil {
		fail(err)
	}
	f, err := os.OpenFile(os.Args[1], os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		fail(fmt.Errorf("%w; keygen never overwrites a key", err))
	}
	if _, err := f.Write(k.File()); err != nil {
		fail(err)
	}
	if err := f.Close(); err != nil {
		fail(err)
	}
	os.Stdout.Write(k.Public().File())
	fmt.Fprintf(os.Stderr, "wrote the secret key %s to %s\n", k.Public().ID, os.Args[1])
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "keygen:", err)
	os.Exit(1)
}
