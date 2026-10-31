package homekit

import (
	"bytes"
	"github.com/brutella/hc/crypto"
	"github.com/brutella/hc/hap"
	"io"
	"net"
	"testing"
	"time"
)

func TestEncryptedControlPreservesMultipleRecords(t *testing.T) {
	raw, peer := net.Pipe()
	defer peer.Close()
	raw.SetDeadline(time.Now().Add(3 * time.Second))
	peer.SetDeadline(time.Now().Add(3 * time.Second))
	ctx := hap.NewContextForSecuredDevice(nil)
	conn := NewConnection(raw, ctx)
	defer conn.Close()
	var key [32]byte
	server, _ := crypto.NewSecureSessionFromSharedKey(key)
	client, _ := crypto.NewSecureClientSessionFromSharedKey(key)
	ctx.GetSessionForConnection(conn).SetCryptographer(server)
	want := append(bytes.Repeat([]byte("a"), 1024), []byte("next request")...)
	encrypted, err := client.Encrypt(bytes.NewReader(want))
	if err != nil {
		t.Fatal(err)
	}
	wire, _ := io.ReadAll(encrypted)
	done := make(chan error, 1)
	go func() { _, err := peer.Write(wire); done <- err }()
	got := make([]byte, len(want))
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("encrypted record lost or altered")
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
func TestEncryptedControlRejectsOversizeRecord(t *testing.T) {
	raw, peer := net.Pipe()
	defer peer.Close()
	ctx := hap.NewContextForSecuredDevice(nil)
	conn := NewConnection(raw, ctx)
	defer conn.Close()
	server, _ := crypto.NewSecureSessionFromSharedKey([32]byte{})
	ctx.GetSessionForConnection(conn).SetCryptographer(server)
	go peer.Write([]byte{1, 8})
	if _, err := conn.Read(make([]byte, 100)); err == nil {
		t.Fatal("accepted frame >1024 bytes")
	}
}
