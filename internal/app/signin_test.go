package app

import (
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"testing"
)

func TestSignInReceiptsCannotCrossHarnessOrAuthorization(t *testing.T) {
	m := NewModel()
	m.DeviceStates["local"] = &DeviceState{}
	job := Job{Method: "runtime.signin.status", Input: protocol.SignInInput{ID: "one", HarnessID: "custom"}}
	for _, s := range []domain.SignInStatus{{ID: "other", HarnessID: "custom"}, {ID: "one", HarnessID: "other"}} {
		b, _ := json.Marshal(s)
		if m.validateDeviceReply("local", job, b) == nil {
			t.Fatal("wrong login receipt accepted")
		}
	}
	b, _ := json.Marshal(domain.SignInStatus{ID: "one", HarnessID: "custom", Phase: domain.SignInWaiting})
	if err := m.validateDeviceReply("local", job, b); err != nil {
		t.Fatal(err)
	}
}
func TestSignInIsUnavailableOnOldWorkersBeforeAnyRequest(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	_, err := executeJob(context.Background(), &protocol.Client{Epoch: "epoch", Minor: 18}, Job{epoch: "epoch", Method: "runtime.signin.start", Input: protocol.SignInInput{ID: "one", HarnessID: "custom"}})
	if err == nil {
		t.Fatal("old worker received unsupported login")
	}
}

func TestSignInRecoveryPersistsOnlyOpaqueReferences(t *testing.T) {
	m := NewModel()
	m.SignInIDs = map[string]map[string]string{"air": {"custom": "auth-one"}}
	path := filepath.Join(t.TempDir(), "client.json")
	if err := m.Save(path); err != nil {
		t.Fatal(err)
	}
	next := NewModel()
	if err := next.Load(path); err != nil {
		t.Fatal(err)
	}
	if next.SignInIDs["air"]["custom"] != "auth-one" {
		t.Fatal("lost query identity across restart")
	}
}

func TestRemoteBrowserIsRejectedBeforeSendingToLegacyWorker(t *testing.T) {
	_, err := executeJob(context.Background(), &protocol.Client{Epoch: "epoch", Minor: 19}, Job{ConnectionID: "air", epoch: "epoch", Method: "runtime.signin.start", Input: protocol.SignInInput{ID: "browser", HarnessID: "custom", Method: domain.SignInBrowser}})
	if err == nil {
		t.Fatal("browser started without remote callback contract")
	}
}
