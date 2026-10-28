package terminal

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"io"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/ttyob/velin-web-ssh/internal/store"
	"golang.org/x/crypto/ssh"
)

func TestInitialConnectionPublishesAttachedBeforeOutput(t *testing.T) {
	// Use an isolated SSH peer and pause shell startup until a browser has
	// subscribed to the still-creating session. No saved hosts are contacted.
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	config := &ssh.ServerConfig{NoClientAuth: true, ServerVersion: "SSH-2.0-test-windows"}
	config.AddHostKey(signer)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	allowShell, shellPending, stop := make(chan struct{}), make(chan struct{}), make(chan struct{})
	defer close(stop)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
		server, channels, requests, handshakeErr := ssh.NewServerConn(conn, config)
		if handshakeErr != nil {
			return
		}
		defer server.Close()
		go ssh.DiscardRequests(requests)
		for incoming := range channels {
			channel, requests, channelErr := incoming.Accept()
			if channelErr != nil {
				return
			}
			go func() {
				defer channel.Close()
				for request := range requests {
					switch request.Type {
					case "pty-req":
						_ = request.Reply(true, nil)
					case "shell":
						close(shellPending)
						select {
						case <-allowShell:
						case <-stop:
							return
						}
						_ = request.Reply(true, nil)
						_, _ = io.WriteString(channel, "ready$ ")
					default:
						_ = request.Reply(false, nil)
					}
				}
			}()
		}
	}()

	database, err := store.Open(filepath.Join(t.TempDir(), "connection.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err = database.CreateUser("user", "user", "hash", "user"); err != nil {
		t.Fatal(err)
	}
	host := store.Host{ID: "host", UserID: "user", Name: "isolated", Address: "127.0.0.1", Port: listener.Addr().(*net.TCPAddr).Port, Username: "test", SessionMode: "normal"}
	if err = database.SaveHost(host); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(database, nil, "test", t.TempDir())
	defer manager.CloseAll()
	meta, err := manager.Create(context.Background(), "user", CreateRequest{Host: host, Secret: "test-only", TrustFingerprint: ssh.FingerprintSHA256(signer.PublicKey())})
	if err != nil {
		t.Fatal(err)
	}
	session, err := manager.Get("user", meta.ID)
	if err != nil {
		t.Fatal(err)
	}
	events, _, _ := session.Subscribe("browser", "", 0)
	defer session.Unsubscribe("browser")
	select {
	case <-shellPending:
	case <-time.After(3 * time.Second):
		t.Fatal("SSH shell startup did not begin")
	}
	if got := session.Meta().Status; got != "creating" {
		t.Fatalf("initial status = %q, want creating", got)
	}
	close(allowShell)
	select {
	case event := <-events:
		if event.Type != "status" || event.Status != "attached" {
			t.Fatalf("first live event = %+v, want attached status before output", event)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("browser did not receive attachment status")
	}
	if got, err := database.Terminal("user", meta.ID); err != nil || got.Status != "attached" {
		t.Fatalf("stored status = %+v, error = %v", got, err)
	}
	select {
	case event := <-events:
		data, _ := base64.StdEncoding.DecodeString(event.Data)
		if event.Type != "output" || string(data) != "ready$ " {
			t.Fatalf("unexpected initial output: %+v (%q)", event, data)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("initial output did not arrive")
	}
}
