package protocol

import (
	"context"
	"net"
	"shioji/internal/portforward"
	"testing"
)

func TestTCPStreamRefusesOldWorkerBeforeSendingTarget(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()
	done := make(chan error, 1)
	go func() {
		codec := NewCodec(b, b)
		var h Frame
		if err := codec.Read(&h); err != nil {
			done <- err
			return
		}
		if err := codec.Write(Frame{Type: "welcome", Major: Major, Minor: 37, Nonce: h.Nonce, DeviceID: "device", Epoch: "epoch"}); err != nil {
			done <- err
			return
		}
		var extra Frame
		done <- codec.Read(&extra)
	}()
	conn, err := OpenTCP(context.Background(), func(context.Context) (net.Conn, error) { return a, nil }, "device", portforward.Target{Host: "localhost", Port: 3000})
	if err == nil {
		conn.Close()
		t.Fatal("old worker accepted")
	}
	if err := <-done; err == nil {
		t.Fatal("sent TCP target to old worker")
	}
}

func TestTCPStreamRequiresExplicitAdvertisementIndependentOfOtherMinorFeatures(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()
	done := make(chan error, 1)
	go func() {
		defer b.Close()
		codec := NewCodec(b, b)
		var h Frame
		if err := codec.Read(&h); err != nil {
			done <- err
			return
		}
		if err := codec.Write(Frame{Type: "welcome", Major: Major, Minor: 99, Nonce: h.Nonce, DeviceID: "device", Epoch: "epoch"}); err != nil {
			done <- err
			return
		}
		var extra Frame
		done <- codec.Read(&extra)
	}()
	conn, err := OpenTCP(context.Background(), func(context.Context) (net.Conn, error) { return a, nil }, "device", portforward.Target{Host: "localhost", Port: 3000})
	if err == nil {
		conn.Close()
		t.Fatal("worker without TCP stream capability accepted")
	}
	if err := <-done; err == nil {
		t.Fatal("minor version alone caused a TCP target to be sent")
	}
}
