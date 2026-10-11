package release

import (
	"fmt"
	"io/fs"
)

type Keys map[KeyID]PublicKey

var trusted = mustParseKeys(keyFiles)

func Trusted() Keys { return trusted }

func (keys Keys) Trusts(k PublicKey) bool {
	have, ok := keys[k.ID]
	return ok && have.Key.Equal(k.Key)
}

func mustParseKeys(fsys fs.FS) Keys {
	keys, err := parseKeys(fsys)
	if err != nil {
		panic(err)
	}
	return keys
}

func parseKeys(fsys fs.FS) (Keys, error) {
	names, err := fs.Glob(fsys, "*.pub")
	if err != nil {
		return nil, err
	}
	keys, files := Keys{}, map[KeyID]string{}
	for _, name := range names {
		file, err := fs.ReadFile(fsys, name)
		if err != nil {
			return nil, err
		}
		k, err := ParsePublicKey(file)
		if err != nil {
			return nil, fmt.Errorf("release key %s: %w", name, err)
		}
		if other, dup := files[k.ID]; dup {
			return nil, fmt.Errorf("release keys %s and %s share the key id %s", other, name, k.ID)
		}
		keys[k.ID], files[k.ID] = k, name
	}
	return keys, nil
}
