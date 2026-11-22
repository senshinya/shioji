package mobileclient

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestPasswordEnrollmentAuthorizesOnlyTheReviewedHost(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	home := t.TempDir()
	keys := filepath.Join(home, ".ssh", "authorized_keys")
	if err := os.Mkdir(filepath.Dir(keys), 0700); err != nil {
		t.Fatal(err)
	}
	host, phone, existing := testSigner(t), testSigner(t), testSigner(t)
	original := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(existing.PublicKey()))) + " existing"
	if err := os.WriteFile(keys, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	const password = "test pass'word"
	var attempts atomic.Int32
	server := &ssh.ServerConfig{
		PasswordCallback: func(meta ssh.ConnMetadata, supplied []byte) (*ssh.Permissions, error) {
			attempts.Add(1)
			if meta.User() != "fixture" || string(supplied) != password {
				return nil, errors.New("incorrect account password")
			}
			return nil, nil
		},
		PublicKeyCallback: func(meta ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			data, err := os.ReadFile(keys)
			if err != nil {
				return nil, err
			}
			for len(data) != 0 {
				saved, _, _, rest, err := ssh.ParseAuthorizedKey(data)
				if err != nil {
					break
				}
				if meta.User() == "fixture" && ssh.FingerprintSHA256(saved) == ssh.FingerprintSHA256(key) {
					return nil, nil
				}
				data = rest
			}
			return nil, errors.New("key has not been authorized")
		},
	}
	server.AddHostKey(host)
	endpoint := Endpoint{Address: bootstrapFixtureSSH(t, ctx, server, home), User: "fixture",
		HostFingerprint: ssh.FingerprintSHA256(host.PublicKey()), WorkerPath: DefaultWorkerPath}
	enroll := func(endpoint Endpoint, secret string) error {
		return endpoint.AuthorizeKeyWithPassword(ctx, phone, nil, secret)
	}
	if _, _, err := endpoint.Bootstrap(ctx, phone, nil, nil); !errors.Is(err, ErrSSHKeyRejected) {
		t.Fatalf("unapproved key did not produce an enrollment error: %v", err)
	}
	replaced := endpoint
	replaced.HostFingerprint = ssh.FingerprintSHA256(testSigner(t).PublicKey())
	if err := enroll(replaced, password); err == nil || attempts.Load() != 0 {
		t.Fatal("password authentication reached a host with an unconfirmed key")
	}
	if err := enroll(endpoint, "incorrect"); err == nil {
		t.Fatal("incorrect password authorized the phone")
	}
	before, err := os.ReadFile(keys)
	if err != nil || string(before) != original {
		t.Fatalf("failed authentication changed existing keys: %v", err)
	}
	for range 2 {
		if err := enroll(endpoint, password); err != nil {
			t.Fatal(err)
		}
	}
	after, err := os.ReadFile(keys)
	if err != nil {
		t.Fatal(err)
	}
	added := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(phone.PublicKey())))
	if !strings.HasPrefix(string(after), original+"\n") || strings.Count(string(after), added) != 1 {
		t.Fatal("enrollment did not preserve the existing key and append the phone key once")
	}
	if code, err := endpoint.WorkerSSH(phone, nil).Run(ctx, "/usr/bin/true", nil, io.Discard, io.Discard); err != nil || code != 0 {
		t.Fatalf("phone could not log in with its authorized key: %v, exit %d", err, code)
	}
}
