package tailnet

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type nodeIntent struct {
	Forwarding *bool  `json:"forwarding,omitempty"`
	Mode       string `json:"mode"`
	Hostname   string `json:"hostname"`
}

func readIntent(dir string) (nodeIntent, error) {
	var in nodeIntent
	raw, err := os.ReadFile(filepath.Join(dir, "intent.json"))
	if os.IsNotExist(err) {
		return in, nil
	}
	if err != nil {
		return in, err
	}
	err = json.Unmarshal(raw, &in)
	return in, err
}
func writeIntent(dir string, in nodeIntent) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".intent-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(raw); err == nil {
		err = f.Sync()
	}
	e := f.Close()
	if err != nil {
		return err
	}
	if e != nil {
		return e
	}
	return os.Rename(f.Name(), filepath.Join(dir, "intent.json"))
}
func (n *Node) closeServer() {
	n.mu.Lock()
	s := n.server
	n.server = nil
	for _, b := range n.bridges {
		b.Drop()
	}
	if n.inbound != nil {
		n.inbound.Close()
		n.inbound = nil
	}
	n.mu.Unlock()
	if s != nil {
		s.Close()
	}
}

// Only provisional credentials are discarded. Existing enrolled keys are untouched.
func (n *Node) retirePending() error {
	n.closeServer()
	n.staging = false
	return os.RemoveAll(filepath.Join(n.directory, "pending"))
}
func (s *encryptedStore) promote(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.Rename(s.path, path); err != nil {
		return err
	}
	s.path = path
	return nil
}

func (in nodeIntent) forwardingEnabled() bool { return in.Forwarding == nil || *in.Forwarding }
