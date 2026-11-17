package attachments

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"shioji/internal/domain"
	"testing"
)

func TestPathAttachmentPDFCannotBypassConsentAsUTF8Text(t *testing.T) {
	_, err := Describe("guide.pdf", []byte("%PDF-1.7\n1 0 obj\n<< /Type /Catalog >>\nendobj\n%%EOF\n"))
	if !errors.Is(err, ErrUnsupportedType) {
		t.Fatal("ASCII PDF would be uploaded without path confirmation", err)
	}
}

func TestPathAttachmentResourceIsImmutableScopedAndVerified(t *testing.T) {
	data := bytes.Repeat([]byte{0, 0xff, 4, 5}, 20000)
	hash := sha256.Sum256(data)
	a := domain.Attachment{ID: domain.NewID(), Kind: "file-path", Name: "design.pdf", MIME: "application/octet-stream", Size: int64(len(data)), SHA256: hex.EncodeToString(hash[:])}
	r := New(t.TempDir())
	w := domain.NewID()
	if _, err := r.Put(w, a, 0, data[:ChunkSize]); err != nil {
		t.Fatal("path resource rejected", err)
	}
	if _, err := r.Path(w, a); err == nil {
		t.Fatal("partial binary became model input")
	}
	if _, err := r.Put(w, a, ChunkSize, data[ChunkSize:]); err != nil {
		t.Fatal(err)
	}
	path, err := New(r.Directory).Path(w, a)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatal("binary snapshot changed", err)
	}
	if info, _ := os.Stat(path); info.Mode().Perm()&0077 != 0 {
		t.Fatal("binary resource is not private")
	}
	if _, err := r.Path(domain.NewID(), a); err == nil {
		t.Fatal("resource escaped owning workspace")
	}
	for _, mutate := range []func(*domain.Attachment){
		func(a *domain.Attachment) { a.SourcePath = "/arbitrary/remote/path" },
		func(a *domain.Attachment) { a.MIME = "text/plain" },
		func(a *domain.Attachment) { a.SHA256 = string(bytes.Repeat([]byte("0"), 64)) },
	} {
		bad := a
		bad.ID = domain.NewID()
		mutate(&bad)
		if _, err := r.Put(w, bad, 0, data[:ChunkSize]); err == nil {
			// A digest is checked on completion; metadata errors fail earlier.
			if _, err = r.Put(w, bad, ChunkSize, data[ChunkSize:]); err == nil {
				t.Fatal("forged path resource accepted", bad)
			}
		}
	}
}
