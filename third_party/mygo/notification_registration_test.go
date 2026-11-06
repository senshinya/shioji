package mygo

import (
	"context"
	"errors"
	"testing"
)

func TestCanceledNotificationCannotRegisterAfterRetirement(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	const id = "retired-registration-test"
	notifications.Lock()
	delete(notifications.byID, id)
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() { close(started); _, _, err := registerNotification(ctx, &Notification{id: id}); done <- err }()
	<-started
	// Retirement cancels before Close takes the same registry lock.
	cancel()
	notifications.Unlock()
	err := <-done
	notifications.Lock()
	_, exists := notifications.byID[id]
	delete(notifications.byID, id)
	notifications.Unlock()
	if !errors.Is(err, context.Canceled) || exists {
		t.Fatal("retired notification reentered the registry", err, exists)
	}
}
