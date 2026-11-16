package transport

import (
	"errors"
	"os/exec"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"testing"
)

func TestRecoveryTransportKeepsTypedNegativeReceiptAndConflict(t *testing.T) {
	_, err := recoveryCommand(exec.Command("/usr/bin/printf", "%s", `{"deviceId":"D","error":"missing","errorCode":"notFound"}`), domain.WorkerRecoveryInput{DeviceID: "D", ID: "old"})
	var remote *protocol.RemoteError
	if !errors.As(err, &remote) || remote.Code != "notFound" {
		t.Fatal("missing definitive receipt", err)
	}
	_, err = recoveryCommand(exec.Command("/usr/bin/printf", "%s", `{"deviceId":"D","id":"existing","conflict":true,"phase":"stopping"}`), domain.WorkerRecoveryInput{DeviceID: "D", ID: "new"})
	if err != nil {
		t.Fatal("existing device action inaccessible", err)
	}
}
