package tailnet

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestEncryptedIdentityPersistsWithoutPlaintext(t *testing.T) {
	p := filepath.Join(t.TempDir(), "identity")
	key := bytes.Repeat([]byte{42}, 32)
	s, err := openStore(p, key)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.WriteState("node", []byte("private-node-key")); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(p)
	if bytes.Contains(raw, []byte("private-node-key")) {
		t.Fatal("plaintext identity")
	}
	restored, err := openStore(p, key)
	if err != nil {
		t.Fatal(err)
	}
	got, err := restored.ReadState("node")
	if err != nil || string(got) != "private-node-key" {
		t.Fatal(string(got), err)
	}
	if _, err = openStore(p, bytes.Repeat([]byte{43}, 32)); err == nil {
		t.Fatal("wrong key accepted")
	}
}
