package release

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
)

const sigAlgorithm = "Ed"

type KeyID [8]byte

func (id KeyID) String() string { return fmt.Sprintf("%016X", binary.LittleEndian.Uint64(id[:])) }

type PublicKey struct {
	ID  KeyID
	Key ed25519.PublicKey
}

func ParsePublicKey(file []byte) (PublicKey, error) {
	lines := strings.Split(strings.TrimRight(string(file), "\n"), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "untrusted comment: ") {
		return PublicKey{}, errors.New("not a minisign public key: want an untrusted comment line and a key line")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(lines[1]))
	if err != nil || len(raw) != 2+8+ed25519.PublicKeySize || string(raw[:2]) != sigAlgorithm {
		return PublicKey{}, errors.New("not a minisign public key: the key line is not an Ed25519 key")
	}
	var k PublicKey
	copy(k.ID[:], raw[2:10])
	k.Key = ed25519.PublicKey(raw[10:])
	return k, nil
}

func (k PublicKey) File() []byte {
	raw := append(append([]byte(sigAlgorithm), k.ID[:]...), k.Key...)
	return fmt.Appendf(nil, "untrusted comment: minisign public key %s\n%s\n", k.ID, base64.StdEncoding.EncodeToString(raw))
}

type SigningKey struct {
	public  PublicKey
	private ed25519.PrivateKey
}

func (k SigningKey) Public() PublicKey { return k.public }

func GenerateKey() (SigningKey, error) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return SigningKey{}, err
	}
	k := SigningKey{public: PublicKey{Key: public}, private: private}
	_, err = rand.Read(k.public.ID[:])
	return k, err
}

const secretKeySize = 2 + 2 + 2 + 32 + 8 + 8 + 8 + ed25519.PrivateKeySize + 32

func ParseSecretKey(file []byte) (SigningKey, error) {
	lines := strings.Split(strings.TrimRight(string(file), "\n"), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "untrusted comment: ") {
		return SigningKey{}, errors.New("not a minisign secret key: want an untrusted comment line and a key line")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(lines[1]))
	if err != nil || len(raw) != secretKeySize || string(raw[:2]) != sigAlgorithm || string(raw[4:6]) != "B2" {
		return SigningKey{}, errors.New("not a minisign secret key: the key line is not an Ed25519 key")
	}
	if string(raw[2:4]) != "\x00\x00" {
		return SigningKey{}, errors.New("the minisign secret key is encrypted with a password; the release signer needs one made with minisign -G -W")
	}
	var id KeyID
	copy(id[:], raw[54:62])
	private := ed25519.PrivateKey(raw[62:126])
	if !bytes.Equal(ed25519.NewKeyFromSeed(private.Seed()), private) {
		return SigningKey{}, errors.New("the minisign secret key is damaged: its public half does not match its seed")
	}
	return SigningKey{public: PublicKey{ID: id, Key: private.Public().(ed25519.PublicKey)}, private: private}, nil
}

func (k SigningKey) File() []byte {
	raw := make([]byte, 0, secretKeySize)
	raw = append(raw, sigAlgorithm+"\x00\x00B2"...)
	raw = append(raw, make([]byte, 32+8+8)...)
	raw = append(raw, k.public.ID[:]...)
	raw = append(raw, k.private...)
	raw = append(raw, make([]byte, 32)...)
	return fmt.Appendf(nil, "untrusted comment: minisign secret key %s, unencrypted\n%s\n", k.public.ID, base64.StdEncoding.EncodeToString(raw))
}

func TrustedComment(v Version) string { return "superfind v" + v.String() + " checksums.txt" }

func Sign(checksums []byte, k SigningKey, v Version) []byte {
	return sign(checksums, k, TrustedComment(v))
}

func sign(message []byte, k SigningKey, trustedComment string) []byte {
	sig := ed25519.Sign(k.private, message)
	global := ed25519.Sign(k.private, append(append([]byte{}, sig...), trustedComment...))
	line := append(append([]byte(sigAlgorithm), k.public.ID[:]...), sig...)
	return fmt.Appendf(nil, "untrusted comment: signature from superfind release key %s\n%s\ntrusted comment: %s\n%s\n",
		k.public.ID, base64.StdEncoding.EncodeToString(line), trustedComment, base64.StdEncoding.EncodeToString(global))
}

type ErrUnknownKey struct{ ID KeyID }

func (e ErrUnknownKey) Error() string {
	return fmt.Sprintf("checksums.txt.minisig is signed with key %s, which this superfind doesn't trust", e.ID)
}

var errBadSignature = errors.New("checksums.txt.minisig does not match checksums.txt")

func (keys Keys) Verify(message, sigFile []byte, v Version) (KeyID, error) {
	lines := strings.Split(strings.TrimRight(string(sigFile), "\n"), "\n")
	if len(lines) != 4 || !strings.HasPrefix(lines[0], "untrusted comment: ") || !strings.HasPrefix(lines[2], "trusted comment: ") {
		return KeyID{}, errors.New("checksums.txt.minisig is not a minisign signature: want four lines, the last two a trusted comment and its signature")
	}
	line, err := base64.StdEncoding.DecodeString(lines[1])
	if err != nil || len(line) != 2+8+ed25519.SignatureSize {
		return KeyID{}, errors.New("checksums.txt.minisig is not a minisign signature: the signature line is malformed")
	}
	if string(line[:2]) != sigAlgorithm {
		return KeyID{}, fmt.Errorf("checksums.txt.minisig uses algorithm %q; superfind release signatures use the legacy %q", line[:2], sigAlgorithm)
	}
	global, err := base64.StdEncoding.DecodeString(lines[3])
	if err != nil || len(global) != ed25519.SignatureSize {
		return KeyID{}, errors.New("checksums.txt.minisig is not a minisign signature: the trusted comment's signature is malformed")
	}
	var id KeyID
	copy(id[:], line[2:10])
	key, ok := keys[id]
	if !ok {
		return id, ErrUnknownKey{id}
	}
	sig := line[10:]
	if !ed25519.Verify(key.Key, message, sig) {
		return id, errBadSignature
	}
	comment := strings.TrimPrefix(lines[2], "trusted comment: ")
	if !ed25519.Verify(key.Key, append(bytes.Clone(sig), comment...), global) {
		return id, errors.New("checksums.txt.minisig's trusted comment was changed after signing")
	}
	if want := TrustedComment(v); comment != want {
		return id, fmt.Errorf("checksums.txt.minisig is signed for %q, not %q", comment, want)
	}
	return id, nil
}
