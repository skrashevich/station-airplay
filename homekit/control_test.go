package homekit

import (
	"bytes"
	"io"
	"testing"
)

func TestControlSessionUsesFullSharedSecret(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 64)
	server, err := NewControlSession(key, false)
	if err != nil {
		t.Fatal(err)
	}
	client, _ := NewControlSession(key, true)
	data := bytes.Repeat([]byte("x"), 2500)
	wire, err := client.Encrypt(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	got, err := server.Decrypt(wire)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(got)
	if !bytes.Equal(b, data) {
		t.Fatal("control data changed")
	}
	wire, _ = server.Encrypt(bytes.NewReader([]byte("reply")))
	got, err = client.Decrypt(wire)
	if err != nil {
		t.Fatal(err)
	}
	b, _ = io.ReadAll(got)
	if string(b) != "reply" {
		t.Fatal("reverse control data changed")
	}
}
