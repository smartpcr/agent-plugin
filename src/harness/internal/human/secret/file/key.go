package file

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/smartpcr/agent-plugin/src/harness/internal/human/secret"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/fsx"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// KeySize is the length of the data key: AES-256.
const KeySize = 32

// ProtectionFile is the protection of a store whose key is an owner-only file
// beside it.
const ProtectionFile = "file"

// Keys holds the store's data key. The file backend (KeyFile) is the fallback
// where no keychain works; the keychain backend of DH-D-HUMAN-8 implements the
// same interface.
type Keys interface {
	// Protection names where the key is kept: "file", later "keychain".
	Protection() string
	// Load returns the key, or ok false when there is none (or what there is
	// cannot be a key): the store's values are then lost.
	Load(ctx context.Context) (key []byte, ok bool, err error)
	// Create makes a key if there is none and returns the key that exists. The
	// store calls it under its lock, before its first write.
	Create(ctx context.Context) ([]byte, error)
}

// KeyFile keeps the key in the owner-only file KeyFileName under dir.
func KeyFile(dir string) Keys { return keyFile{path: filepath.Join(dir, KeyFileName)} }

type keyFile struct{ path string }

func (keyFile) Protection() string { return ProtectionFile }

func (k keyFile) Load(context.Context) ([]byte, bool, error) {
	info, err := os.Lstat(k.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, herr.Wrap(secret.ReasonSource, err, "the secret store's key file cannot be read")
	}
	if !info.Mode().IsRegular() {
		return nil, false, herr.New(secret.ReasonSource, "the secret store's key file is refused: it is not a regular file")
	}
	f, err := fsx.Open(k.path)
	if err != nil {
		return nil, false, herr.Wrap(secret.ReasonSource, err, "the secret store's key file cannot be read")
	}
	defer f.Close()
	if err := home.CheckPrivateFile(f); err != nil {
		return nil, false, herr.Wrap(secret.ReasonSource, err, "the secret store's key file is refused")
	}
	key, err := io.ReadAll(io.LimitReader(f, KeySize+1))
	if err != nil {
		return nil, false, herr.Wrap(secret.ReasonSource, err, "the secret store's key file cannot be read")
	}
	if len(key) != KeySize {
		return nil, false, nil
	}
	return key, true, nil
}

func (k keyFile) Create(ctx context.Context) ([]byte, error) {
	key := make([]byte, KeySize)
	if _, err := rand.Read(key); err != nil {
		return nil, herr.Wrap(secret.ReasonSource, err, "no random key can be made")
	}
	err := fsx.CreateIfAbsent(k.path, fsx.Bytes(key), fsx.Options{Perm: 0o600, Durable: true})
	if err != nil && !errors.Is(err, fs.ErrExist) {
		return nil, herr.Wrap(secret.ReasonSource, err, "the secret store's key file cannot be written")
	}
	got, ok, err := k.Load(ctx)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, herr.New(secret.ReasonKeyLost, "the secret store's key file holds no key")
	}
	return got, nil
}
