package tailnet

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"tailscale.com/ipn"
)

// Standard AES-GCM envelope around the official StateStore interface.
// The random master key lives only in the application's dedicated Keychain service.
type encryptedStore struct {
	mu   sync.Mutex
	path string
	gcm  cipher.AEAD
	data map[ipn.StateKey][]byte
}

func openStore(path string, key []byte) (*encryptedStore, error) {
	b, e := aes.NewCipher(key)
	if e != nil {
		return nil, e
	}
	g, e := cipher.NewGCM(b)
	if e != nil {
		return nil, e
	}
	s := &encryptedStore{path: path, gcm: g, data: map[ipn.StateKey][]byte{}}
	raw, e := os.ReadFile(path)
	if errors.Is(e, os.ErrNotExist) {
		return s, nil
	}
	if e != nil {
		return nil, e
	}
	if len(raw) < g.NonceSize() {
		return nil, errors.New("invalid encrypted state")
	}
	plain, e := g.Open(nil, raw[:g.NonceSize()], raw[g.NonceSize():], []byte("shioji-tailnet-v1"))
	if e != nil {
		return nil, e
	}
	if e = json.Unmarshal(plain, &s.data); e != nil {
		return nil, e
	}
	return s, nil
}
func (s *encryptedStore) ReadState(k ipn.StateKey) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.data[k]
	if !ok {
		return nil, ipn.ErrStateNotExist
	}
	return bytes.Clone(b), nil
}
func (s *encryptedStore) WriteState(k ipn.StateKey, b []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := make(map[ipn.StateKey][]byte, len(s.data)+1)
	for k, v := range s.data {
		next[k] = v
	}
	if b == nil {
		delete(next, k)
	} else {
		next[k] = bytes.Clone(b)
	}
	plain, e := json.Marshal(next)
	if e != nil {
		return e
	}
	nonce := make([]byte, s.gcm.NonceSize())
	if _, e = rand.Read(nonce); e != nil {
		return e
	}
	raw := s.gcm.Seal(nonce, nonce, plain, []byte("shioji-tailnet-v1"))
	f, e := os.CreateTemp(filepath.Dir(s.path), ".encrypted-")
	if e != nil {
		return e
	}
	name := f.Name()
	defer os.Remove(name)
	if e = f.Chmod(0600); e == nil {
		_, e = f.Write(raw)
	}
	if e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e == nil {
		e = closeErr
	}
	if e == nil {
		e = os.Rename(name, s.path)
	}
	if e != nil {
		return e
	}
	s.data = next
	return nil
}
