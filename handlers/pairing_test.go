package handlers

import (
	"bytes"
	"context"
	"github.com/brutella/hc/hap/pair"
	"github.com/brutella/hc/util"
	"github.com/skrashevich/station-airplay/audio"
	"github.com/skrashevich/station-airplay/codec"
	"github.com/skrashevich/station-airplay/homekit"
	"github.com/skrashevich/station-airplay/ptp"
	"github.com/skrashevich/station-airplay/rtsp"
	"io"
	"net"
	"testing"
)

func TestTransientPairSetupActivatesEncryptionAfterPlaintextM4(t *testing.T) {
	var err error
	homekit.Server, err = homekit.NewServer("AA:BB:CC:DD:EE:FF", t.TempDir(), []string{"127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	raw, peer := net.Pipe()
	defer peer.Close()
	conn := new(rtsp.Conn)
	conn.SetNetConn(homekit.NewConnection(raw, homekit.Server.Context))
	defer conn.Close()
	r, _ := NewRstpHandler("test", audio.NewPlayer(context.Background(), ptp.NewVirtualClock(0), codec.OutputOptions{}))
	in := util.NewTLV8Container()
	in.SetByte(pair.TagSequence, 1)
	in.SetByte(pair.TagPairingMethod, 0)
	in.SetByte(19, 16)
	response, err := r.OnPairSetup(conn, &rtsp.Request{Body: in.BytesBuffer().Bytes()})
	if err != nil {
		t.Fatal(err)
	}
	out, err := util.NewTLV8ContainerFromReader(bytes.NewReader(response.Body))
	if err != nil {
		t.Fatal(err)
	}
	client := pair.NewSetupClientSession("Pair-Setup", "3939")
	if err := client.GenerateKeys(out.GetBytes(pair.TagSalt), out.GetBytes(pair.TagPublicKey)); err != nil {
		t.Fatal(err)
	}
	in = util.NewTLV8Container()
	in.SetByte(pair.TagSequence, 3)
	in.SetBytes(pair.TagPublicKey, client.PublicKey)
	in.SetBytes(pair.TagProof, client.Proof)
	response, err = r.OnPairSetup(conn, &rtsp.Request{Body: in.BytesBuffer().Bytes()})
	if err != nil {
		t.Fatal(err)
	}
	out, err = util.NewTLV8ContainerFromReader(bytes.NewReader(response.Body))
	if err != nil || !client.IsServerProofValid(out.GetBytes(pair.TagProof)) {
		t.Fatalf("bad server proof: %v", err)
	}
	session := homekit.Server.Context.GetSessionForConnection(conn.NetConn())
	if session.Encrypter() != nil {
		t.Fatal("M4 must remain plaintext")
	}
	if conn.AfterResponse == nil {
		t.Fatal("transient pairing did not schedule encryption")
	}
	conn.AfterResponse()
	crypto, _ := homekit.NewControlSession(client.PrivateKey, true)
	wire, _ := crypto.Encrypt(bytes.NewReader([]byte("hello")))
	decoded, err := session.Decrypter().Decrypt(wire)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(decoded)
	if string(b) != "hello" {
		t.Fatal("wrong control key")
	}
}

func TestStreamNegotiationRejectsUnsupportedModes(t *testing.T) {
	good := setupStreamRequest{Type: 103, SharedKey: make([]byte, 32), CompressionType: 4, AudioFormat: 1 << 22, Spf: 1024}
	rate, err := streamRate(good)
	if err != nil || rate != 44100 {
		t.Fatalf("rate %d: %v", rate, err)
	}
	for _, change := range []func(*setupStreamRequest){func(v *setupStreamRequest) { v.Type = 96 }, func(v *setupStreamRequest) { v.CompressionType = 2 }, func(v *setupStreamRequest) { v.AudioFormat = 0 }, func(v *setupStreamRequest) { v.SharedKey = nil }, func(v *setupStreamRequest) { v.SampleRate = 48000 }} {
		v := good
		change(&v)
		if _, err := streamRate(v); err == nil {
			t.Fatal("accepted unsupported stream")
		}
	}
}
