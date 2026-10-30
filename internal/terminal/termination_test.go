package terminal

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"errors"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ttyob/velin-web-ssh/internal/store"
	"golang.org/x/crypto/ssh"
)

type terminationDialer struct {
	calls int
	wait  bool
}

func (d *terminationDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	d.calls++
	if d.wait {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return nil, &net.OpError{Op: "dial", Net: network, Err: context.DeadlineExceeded}
}

func TestTerminateUnreachableSessionEndsLocalState(t *testing.T) {
	for _, scenario := range []string{"saved", "canceled-dial", "disconnected", "creating", "normal"} {
		t.Run(scenario, func(t *testing.T) {
			database, err := store.Open(filepath.Join(t.TempDir(), "termination.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer database.Close()
			if err = database.CreateUser("owner", "owner", "hash", "user"); err != nil {
				t.Fatal(err)
			}
			manager := NewManager(database, nil, "test")
			dialer := &terminationDialer{wait: scenario == "canceled-dial"}
			manager.dialer = dialer
			host := store.Host{ID: "host", UserID: "owner", Name: "unreachable", Address: "192.0.2.1", Port: 22, Username: "test"}
			if err = database.SaveHost(host); err != nil {
				t.Fatal(err)
			}
			meta := store.TerminalSession{ID: "session", UserID: "owner", HostID: host.ID, Name: "stale terminal", SessionMode: "tmux", Status: "unreachable", TmuxSocket: "test", TmuxName: "test", OwnerMarker: "owner"}
			if scenario == "creating" {
				meta.Status = "creating"
			}
			if scenario == "normal" {
				meta.SessionMode = "normal"
			}
			if err = database.SaveTerminal(meta); err != nil {
				t.Fatal(err)
			}
			var done <-chan struct{}
			var connectCtx context.Context
			if scenario == "disconnected" || scenario == "creating" {
				var cancel context.CancelFunc
				connectCtx, cancel = context.WithCancel(context.Background())
				session := &Session{meta: meta, manager: manager, buffer: newRingBuffer(1024), subs: make(map[string]*terminalSubscriber), connectCancel: cancel}
				manager.sessions[meta.ID] = session
				_, done, _ = session.SubscribeReadOnly("viewer", "", 0)
			}
			ctx := context.Background()
			if scenario == "canceled-dial" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 20*time.Millisecond)
				defer cancel()
			}
			result, err := manager.Terminate(ctx, "owner", meta.ID, "test-only", "")
			if err != nil || !result.OK {
				t.Fatalf("termination result=%+v error=%v", result, err)
			}
			remoteEnded := scenario == "normal" || scenario == "creating"
			if result.RemoteTerminated != remoteEnded || (!remoteEnded && result.Warning == "") {
				t.Fatalf("remote cleanup result=%+v, want remoteTerminated=%v", result, remoteEnded)
			}
			stored, err := database.Terminal("owner", meta.ID)
			if err != nil || stored.Status != "ended" {
				t.Fatalf("stored terminal=%+v error=%v", stored, err)
			}
			if !remoteEnded && !strings.HasPrefix(stored.LastError, remoteTerminationWarning) {
				t.Fatalf("remote cleanup warning was lost: %q", stored.LastError)
			}
			if _, err = manager.Get("owner", meta.ID); !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("terminated session is still attached: %v", err)
			}
			if done != nil {
				select {
				case <-done:
				default:
					t.Fatal("viewer remained subscribed after local termination")
				}
				if connectCtx.Err() == nil {
					t.Fatal("in-progress connection was not canceled")
				}
			}
			// Retrying an ended session is idempotent and preserves an honest
			// remote-cleanup result without contacting the unreachable host.
			calls := dialer.calls
			again, err := manager.Terminate(context.Background(), "owner", meta.ID, "test-only", "")
			if err != nil || again != result || dialer.calls != calls {
				t.Fatalf("retry=%+v error=%v dial calls=%d (was %d)", again, err, dialer.calls, calls)
			}
		})
	}
}

func TestTerminateDoesNotAffectAnotherUsersSession(t *testing.T) {
	database, err := store.Open(filepath.Join(t.TempDir(), "termination.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err = database.CreateUser("owner", "owner", "hash", "user"); err != nil {
		t.Fatal(err)
	}
	meta := store.TerminalSession{ID: "session", UserID: "owner", Name: "terminal", SessionMode: "tmux", Status: "unreachable"}
	if err = database.SaveTerminal(meta); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(database, nil, "test")
	if _, err = manager.Terminate(context.Background(), "another-user", meta.ID, "", ""); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("another user termination error=%v", err)
	}
	stored, err := database.Terminal("owner", meta.ID)
	if err != nil || stored.Status != "unreachable" {
		t.Fatalf("another user's terminal was modified: %+v error=%v", stored, err)
	}
}

func TestTerminateClosesStalledSSHTransport(t *testing.T) {
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
	defer listener.Close()
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		server, _, requests, err := ssh.NewServerConn(conn, config)
		if err != nil {
			return
		}
		defer server.Close()
		go ssh.DiscardRequests(requests)
		// Deliberately never accept session channels: simulate a transport
		// that is connected but no longer responds to remote commands.
		<-stop
	}()
	client, err := ssh.Dial("tcp", listener.Addr().String(), &ssh.ClientConfig{User: "test", HostKeyCallback: ssh.InsecureIgnoreHostKey(), Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	database, err := store.Open(filepath.Join(t.TempDir(), "termination.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err = database.CreateUser("owner", "owner", "hash", "user"); err != nil {
		t.Fatal(err)
	}
	meta := store.TerminalSession{ID: "session", UserID: "owner", Name: "terminal", SessionMode: "tmux", Status: "attached"}
	if err = database.SaveTerminal(meta); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(database, nil, "test")
	manager.sessions[meta.ID] = &Session{meta: meta, manager: manager, sshClient: client, subs: make(map[string]*terminalSubscriber)}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	completed := make(chan struct{})
	go func() {
		defer close(completed)
		result, err := manager.Terminate(ctx, "owner", meta.ID, "", "")
		if err != nil || !result.OK || result.RemoteTerminated {
			t.Errorf("termination=%+v error=%v", result, err)
		}
	}()
	select {
	case <-completed:
	case <-time.After(2 * time.Second):
		t.Fatal("stalled SSH transport blocked local termination past its deadline")
	}
	stored, err := database.Terminal("owner", meta.ID)
	if err != nil || stored.Status != "ended" {
		t.Fatalf("stored terminal=%+v error=%v", stored, err)
	}
}
