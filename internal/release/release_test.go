package release

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func TestParseVersion(t *testing.T) {
	for in, want := range map[string]string{"0.12.0": "0.12.0", "v1.2.3": "1.2.3", "10.0.20": "10.0.20"} {
		v, err := ParseVersion(in)
		if err != nil || v.String() != want {
			t.Errorf("ParseVersion(%q) = %v, %v; want %s", in, v, err, want)
		}
	}
	for _, in := range []string{"", "v", "0.12", "0.12.0.1", "0.12.0-rc1", "01.2.3", "1.2.x", "1..2", "vv1.2.3", " 1.2.3", "1.2.+3"} {
		if v, err := ParseVersion(in); err == nil {
			t.Errorf("ParseVersion(%q) = %v, want an error", in, v)
		}
	}
	if _, err := ParseVersion("0.12"); err == nil || err.Error() != `"0.12" is not a version like 0.12.0` {
		t.Errorf("ParseVersion(0.12) error = %v", err)
	}
}

func TestLess(t *testing.T) {
	for _, c := range []struct {
		a, b string
		less bool
	}{
		{"0.11.0", "0.11.1", true},
		{"0.11.1", "0.11.0", false},
		{"0.9.9", "0.10.0", true},
		{"1.0.0", "0.99.99", false},
		{"0.12.0", "0.12.0", false},
	} {
		if got := mustVersion(t, c.a).Less(mustVersion(t, c.b)); got != c.less {
			t.Errorf("%s < %s = %v, want %v", c.a, c.b, got, c.less)
		}
	}
}

func TestVerifyAcceptsASignatureMadeByTheMinisignCLI(t *testing.T) {
	id, err := testKeys(t).Verify(read(t, "testdata/release/checksums.txt"), read(t, "testdata/release/checksums.txt.minisig"), mustVersion(t, "0.11.1"))
	if err != nil || id.String() != "F8D8C9C1F5F2C355" {
		t.Fatalf("verify = %s, %v; want F8D8C9C1F5F2C355, nil", id, err)
	}
}

func TestSignMakesTheSignatureMinisignVerified(t *testing.T) {
	got := Sign(read(t, "testdata/release/checksums.txt"), testSigningKey(t), mustVersion(t, "0.11.1"))
	if want := read(t, "testdata/superfind-sign.minisig"); !bytes.Equal(got, want) {
		t.Fatalf("Sign =\n%s\nwant (verified by minisign -V in make-fixtures.sh)\n%s", got, want)
	}
}

func TestMinisignCLIAgreesWithSignAndGenerateKey(t *testing.T) {
	if _, err := exec.LookPath("minisign"); err != nil {
		t.Skip("minisign is not installed; testdata/superfind-sign.minisig covers this")
	}
	dir := t.TempDir()
	message := []byte("0000000000000000000000000000000000000000000000000000000000000000  superfind_1.2.3_darwin_arm64.tar.gz\n")
	os.WriteFile(filepath.Join(dir, "checksums.txt"), message, 0o644)
	os.WriteFile(filepath.Join(dir, "checksums.txt.minisig"), Sign(message, testSigningKey(t), mustVersion(t, "1.2.3")), 0o644)
	pub := strings.Split(string(read(t, "testdata/test.pub")), "\n")[1]
	out, err := exec.Command("minisign", "-V", "-m", filepath.Join(dir, "checksums.txt"), "-P", pub).CombinedOutput()
	if err != nil || !strings.Contains(string(out), "Trusted comment: superfind v1.2.3 checksums.txt") {
		t.Fatalf("minisign -V: %v\n%s", err, out)
	}

	k, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "generated.key"), k.File(), 0o600)
	out, err = exec.Command("minisign", "-S", "-l", "-s", filepath.Join(dir, "generated.key"), "-t", "superfind v1.2.4 checksums.txt", "-m", filepath.Join(dir, "checksums.txt")).CombinedOutput()
	if err != nil {
		t.Fatalf("minisign -S with a GenerateKey key: %v\n%s", err, out)
	}
	id, err := Keys{k.Public().ID: k.Public()}.Verify(message, read(t, filepath.Join(dir, "checksums.txt.minisig")), mustVersion(t, "1.2.4"))
	if err != nil || id != k.Public().ID {
		t.Fatalf("Verify of minisign's signature with a GenerateKey key = %s, %v", id, err)
	}
}

func TestVerifyRejects(t *testing.T) {
	message := read(t, "testdata/release/checksums.txt")
	good := string(read(t, "testdata/release/checksums.txt.minisig"))
	lines := strings.Split(good, "\n")
	other := SigningKey{public: PublicKey{ID: KeyID{1, 2, 3, 4, 5, 6, 7, 8}}, private: ed25519.NewKeyFromSeed(bytes.Repeat([]byte{7}, 32))}
	other.public.Key = other.private.Public().(ed25519.PublicKey)
	impostor := other
	impostor.public.ID = testSigningKey(t).Public().ID
	prehashed := []byte(strings.Replace(good, "RWRV", "RURV", 1))

	for _, c := range []struct {
		name    string
		message []byte
		sig     string
		version string
		want    string
	}{
		{"a changed byte in checksums.txt", append([]byte{'2'}, message[1:]...), good, "0.11.1", "checksums.txt.minisig does not match checksums.txt"},
		{"a key it doesn't trust", message, string(sign(message, other, "superfind v0.11.1 checksums.txt")), "0.11.1", "checksums.txt.minisig is signed with key 0807060504030201, which this superfind doesn't trust"},
		{"another key under a trusted key's id", message, string(sign(message, impostor, "superfind v0.11.1 checksums.txt")), "0.11.1", "checksums.txt.minisig does not match checksums.txt"},
		{"a trusted comment for another file", message, string(sign(message, testSigningKey(t), "superfind v0.11.1 install.sh")), "0.11.1", `checksums.txt.minisig is signed for "superfind v0.11.1 install.sh", not "superfind v0.11.1 checksums.txt"`},
		{"another version", message, good, "0.11.2", `checksums.txt.minisig is signed for "superfind v0.11.1 checksums.txt", not "superfind v0.11.2 checksums.txt"`},
		{"a trusted comment changed after signing", message, strings.Replace(good, "v0.11.1", "v0.11.2", 1), "0.11.2", "checksums.txt.minisig's trusted comment was changed after signing"},
		{"no global signature", message, strings.Join(lines[:3], "\n") + "\n", "0.11.1", "checksums.txt.minisig is not a minisign signature: want four lines, the last two a trusted comment and its signature"},
		{"a prehashed signature", message, string(prehashed), "0.11.1", `checksums.txt.minisig uses algorithm "ED"; superfind release signatures use the legacy "Ed"`},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := testKeys(t).Verify(c.message, []byte(c.sig), mustVersion(t, c.version))
			if err == nil || err.Error() != c.want {
				t.Fatalf("verify error = %v\nwant %s", err, c.want)
			}
		})
	}
}

func TestParseKeys(t *testing.T) {
	pub := read(t, "testdata/test.pub")
	keys, err := parseKeys(fstest.MapFS{"superfind-2026.pub": {Data: pub}, "README": {Data: []byte("not a key")}})
	if err != nil || len(keys) != 1 || keys[KeyID{0x55, 0xc3, 0xf2, 0xf5, 0xc1, 0xc9, 0xd8, 0xf8}].ID.String() != "F8D8C9C1F5F2C355" {
		t.Fatalf("parseKeys = %v, %v", keys, err)
	}
	_, err = parseKeys(fstest.MapFS{"a.pub": {Data: pub}, "b.pub": {Data: pub}})
	if err == nil || err.Error() != "release keys a.pub and b.pub share the key id F8D8C9C1F5F2C355" {
		t.Fatalf("duplicate ids: %v", err)
	}
	_, err = parseKeys(fstest.MapFS{"bad.pub": {Data: []byte("untrusted comment: x\nRWQ=\n")}})
	if err == nil || err.Error() != "release key bad.pub: not a minisign public key: the key line is not an Ed25519 key" {
		t.Fatalf("bad key: %v", err)
	}
}

func TestPublicKeyFileIsWhatMinisignWrites(t *testing.T) {
	k, err := ParsePublicKey(read(t, "testdata/test.pub"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(k.File()), string(read(t, "testdata/test.pub")); got != want {
		t.Fatalf("File() =\n%s\nwant\n%s", got, want)
	}
}

func TestParseSecretKey(t *testing.T) {
	file := read(t, "testdata/test.key")
	k, err := ParseSecretKey(file)
	if err != nil || k.Public().ID.String() != "F8D8C9C1F5F2C355" || !testKeys(t).Trusts(k.Public()) {
		t.Fatalf("ParseSecretKey(test.key) = %s, %v", k.Public().ID, err)
	}
	if got, want := strings.Split(string(k.File()), "\n")[1], strings.Split(string(file), "\n")[1]; got != want {
		t.Fatalf("File() key line =\n%s\nwant minisign's\n%s", got, want)
	}
	raw := func(edit func([]byte)) []byte {
		b, _ := base64.StdEncoding.DecodeString(strings.Split(string(file), "\n")[1])
		edit(b)
		return []byte("untrusted comment: x\n" + base64.StdEncoding.EncodeToString(b) + "\n")
	}
	for _, c := range []struct {
		name string
		file []byte
		want string
	}{
		{"encrypted", raw(func(b []byte) { copy(b[2:4], "Sc") }), "the minisign secret key is encrypted with a password; the release signer needs one made with minisign -G -W"},
		{"a changed byte in the public half", raw(func(b []byte) { b[125] ^= 1 }), "the minisign secret key is damaged: its public half does not match its seed"},
		{"a public key", read(t, "testdata/test.pub"), "not a minisign secret key: the key line is not an Ed25519 key"},
	} {
		if _, err := ParseSecretKey(c.file); err == nil || err.Error() != c.want {
			t.Errorf("%s: ParseSecretKey error = %v, want %s", c.name, err, c.want)
		}
	}
}

func TestGenerateKeyRoundTrips(t *testing.T) {
	k, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	back, err := ParseSecretKey(k.File())
	if err != nil || back.Public().ID != k.Public().ID || !back.Public().Key.Equal(k.Public().Key) {
		t.Fatalf("ParseSecretKey(GenerateKey().File()) = %v, %v", back.Public(), err)
	}
	pub, err := ParsePublicKey(k.Public().File())
	if err != nil || !(Keys{pub.ID: pub}).Trusts(back.Public()) || testKeys(t).Trusts(back.Public()) {
		t.Fatalf("ParsePublicKey(File()) = %v, %v", pub, err)
	}
	message := []byte("a\n")
	if _, err := (Keys{pub.ID: pub}).Verify(message, Sign(message, back, mustVersion(t, "2.0.0")), mustVersion(t, "2.0.0")); err != nil {
		t.Fatalf("Verify(Sign) with a generated key = %v", err)
	}
}

func TestCheckAssets(t *testing.T) {
	checksums := read(t, "testdata/release/checksums.txt")
	if err := CheckAssets("testdata/release", checksums); err != nil {
		t.Fatalf("CheckAssets(testdata/release) = %v", err)
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "install.sh"), []byte("#!/bin/sh\necho changed\n"), 0o644)
	err := CheckAssets(dir, checksums)
	want := "install.sh: sha256 is 9a111093110c4a677d0efe9017e0cd6f9acdc40c9c5a9769d991fd50749e008c, but checksums.txt says 13d4c1b32aa575d6976348888a129080e01b25b0db6132f2386b48b9cb34ce07\nsuperfind_0.11.1_linux_amd64.tar.gz: open "
	if err == nil || !strings.HasPrefix(err.Error(), want) {
		t.Fatalf("CheckAssets(changed, missing) = %v", err)
	}
	if err := CheckAssets(dir, []byte(strings.Repeat("a", 64)+"  ../install.sh\n")); err == nil || !strings.HasPrefix(err.Error(), "checksums.txt has a line that is not") {
		t.Fatalf("CheckAssets(../install.sh) = %v", err)
	}
	if err := CheckAssets(dir, nil); err == nil || err.Error() != "checksums.txt lists no files" {
		t.Fatalf("CheckAssets(empty) = %v", err)
	}
}

func mustVersion(t *testing.T, s string) Version {
	t.Helper()
	v, err := ParseVersion(s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func read(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func testKeys(t *testing.T) Keys {
	t.Helper()
	keys, err := parseKeys(os.DirFS("testdata"))
	if err != nil {
		t.Fatal(err)
	}
	return keys
}

func testSigningKey(t *testing.T) SigningKey {
	t.Helper()
	k, err := ParseSecretKey(read(t, "testdata/test.key"))
	if err != nil {
		t.Fatal(err)
	}
	return k
}
