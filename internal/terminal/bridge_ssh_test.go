package terminal

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"net"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/ttyob/velin-web-ssh/internal/store"
	"golang.org/x/crypto/ssh"
)

// Isolated SSH command peer: only the fixture's local tmux socket is used.
func bridgeSSHPeer(t *testing.T) *ssh.Client {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	config := &ssh.ServerConfig{NoClientAuth: true}
	config.AddHostKey(signer)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		server, channels, requests, err := ssh.NewServerConn(conn, config)
		if err != nil {
			return
		}
		defer server.Close()
		go ssh.DiscardRequests(requests)
		for incoming := range channels {
			channel, requests, err := incoming.Accept()
			if err != nil {
				continue
			}
			go func() {
				defer channel.Close()
				for request := range requests {
					if request.Type != "exec" {
						_ = request.Reply(false, nil)
						continue
					}
					var payload struct{ Command string }
					if ssh.Unmarshal(request.Payload, &payload) != nil {
						_ = request.Reply(false, nil)
						return
					}
					_ = request.Reply(true, nil)
					if payload.Command == "fixture-block" {
						_, _ = io.Copy(io.Discard, channel)
						return
					}
					command := exec.Command("sh", "-c", payload.Command)
					command.Stdout = channel
					command.Stderr = channel.Stderr()
					status := uint32(0)
					if command.Run() != nil {
						status = 1
					}
					_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{status}))
					return
				}
			}()
		}
	}()
	client, err := ssh.Dial("tcp", listener.Addr().String(), &ssh.ClientConfig{User: "fixture", HostKeyCallback: ssh.InsecureIgnoreHostKey(), Timeout: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}
func TestBridgeRealTmuxCapturePreservesViewAndSSH(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("requires tmux")
	}
	socket := "velin-bridge-" + time.Now().Format("150405.000000000")
	tmux := func(args ...string) string {
		t.Helper()
		data, err := exec.Command("tmux", append([]string{"-L", socket}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("tmux %v: %s %v", args, data, err)
		}
		return strings.TrimSpace(string(data))
	}
	tmux("-f", "/dev/null", "new-session", "-d", "-s", "test", "-x", "80", "-y", "12", "printf '真实中文输出\\n'; exec sleep 30")
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", socket, "kill-server").Run() })
	tmux("copy-mode", "-t", "test")
	state := func() string {
		return tmux("display-message", "-p", "-t", "test", "#{pane_width}|#{pane_height}|#{pane_in_mode}|#{scroll_position}")
	}
	before := state()
	s := &Session{sshClient: bridgeSSHPeer(t), meta: store.TerminalSession{SessionMode: "tmux", TmuxSocket: socket, TmuxName: "test"}, buffer: newRingBuffer(1024), streamID: "stream", tmuxCaptureSafe: true, controller: "browser", gridRows: 12, gridCols: 80}
	s.buffer.Write([]byte("wrong redraw from browser"))
	snapshot, err := s.BridgeSnapshot(context.Background(), 50)
	if err != nil || snapshot.Capture == nil || !strings.Contains(*snapshot.Capture, "真实中文输出") || snapshot.Source != "tmux-capture" {
		t.Fatal(snapshot, err)
	}
	if state() != before || s.controller != "browser" {
		t.Fatal("capture changed view or control", before, state())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, _, err = bridgeCommand(ctx, s.sshClient, "fixture-block"); err == nil {
		t.Fatal("blocked capture was not cancelled")
	}
	checkCtx, checkCancel := context.WithTimeout(context.Background(), time.Second)
	defer checkCancel()
	data, _, err := bridgeCommand(checkCtx, s.sshClient, "printf alive")
	if err != nil || string(data) != "alive" {
		t.Fatal("capture cancellation closed shared SSH", string(data), err)
	}
}
