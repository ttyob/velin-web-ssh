package forward

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"

	"github.com/armon/go-socks5"
	"github.com/google/uuid"
	"github.com/ttyob/velin-web-ssh/internal/netdial"
	"github.com/ttyob/velin-web-ssh/internal/store"
	"github.com/ttyob/velin-web-ssh/internal/terminal"
	"golang.org/x/crypto/ssh"
)

type running struct {
	listener net.Listener
	ssh      *ssh.Client
}
type Manager struct {
	store     *store.Store
	terminals *terminal.Manager
	dialer    netdial.Dialer
	mu        sync.Mutex
	active    map[string]*running
}

func NewManager(s *store.Store, terminals *terminal.Manager) *Manager {
	_, _ = s.DB.Exec(`UPDATE port_forwards SET status='stopped',last_error='' WHERE status='running'`)
	return &Manager{store: s, terminals: terminals, dialer: netdial.Direct{}, active: make(map[string]*running)}
}

func (m *Manager) SetDialer(dialer netdial.Dialer) {
	if dialer != nil {
		m.dialer = dialer
	}
}
func (m *Manager) ActiveCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.active)
}
func (m *Manager) List(userID string) ([]store.PortForward, error) {
	return m.store.PortForwards(userID)
}
func validate(value store.PortForward) error {
	if value.Name == "" || value.HostID == "" || value.ListenAddress != "127.0.0.1" || value.ListenPort < 1 || value.ListenPort > 65535 {
		return errors.New("invalid forward configuration")
	}
	if value.Kind != "local" && value.Kind != "remote" && value.Kind != "dynamic" {
		return errors.New("unsupported forward type")
	}
	if value.Kind != "dynamic" && (value.TargetHost == "" || value.TargetPort < 1 || value.TargetPort > 65535) {
		return errors.New("target is required")
	}
	return nil
}
func (m *Manager) Save(userID string, value store.PortForward) (store.PortForward, error) {
	value.UserID = userID
	if value.ID == "" {
		value.ID = uuid.NewString()
	}
	value.Status = "stopped"
	if err := validate(value); err != nil {
		return value, err
	}
	if _, err := m.store.Host(userID, value.HostID); err != nil {
		return value, errors.New("host does not exist or is not owned by user")
	}
	if err := m.store.SavePortForward(value); err != nil {
		return value, err
	}
	return m.store.PortForward(userID, value.ID)
}
func (m *Manager) Start(ctx context.Context, userID, id string) error {
	value, err := m.store.PortForward(userID, id)
	if err != nil {
		return err
	}
	if err = validate(value); err != nil {
		return err
	}
	m.mu.Lock()
	if m.active[id] != nil {
		m.mu.Unlock()
		return nil
	}
	m.mu.Unlock()
	client, _, err := m.terminals.DialSaved(ctx, userID, value.HostID)
	if err != nil {
		_ = m.store.UpdatePortForward(userID, id, "error", err.Error())
		return err
	}
	address := net.JoinHostPort(value.ListenAddress, fmt.Sprint(value.ListenPort))
	var listener net.Listener
	if value.Kind == "remote" {
		listener, err = client.Listen("tcp", address)
	} else {
		listener, err = net.Listen("tcp4", address)
	}
	if err != nil {
		client.Close()
		_ = m.store.UpdatePortForward(userID, id, "error", err.Error())
		return err
	}
	m.mu.Lock()
	if prior := m.active[id]; prior != nil {
		m.mu.Unlock()
		listener.Close()
		client.Close()
		return nil
	}
	m.active[id] = &running{listener: listener, ssh: client}
	m.mu.Unlock()
	_ = m.store.UpdatePortForward(userID, id, "running", "")
	if value.Kind == "dynamic" {
		server, _ := socks5.New(&socks5.Config{Dial: func(ctx context.Context, network, addr string) (net.Conn, error) { return client.Dial(network, addr) }})
		go func() {
			if err := server.Serve(listener); err != nil && !errors.Is(err, net.ErrClosed) {
				m.failed(userID, id, err)
			}
		}()
		return nil
	}
	go m.accept(userID, value, listener, client)
	return nil
}
func (m *Manager) accept(userID string, value store.PortForward, listener net.Listener, client *ssh.Client) {
	for {
		incoming, err := listener.Accept()
		if err != nil {
			if !errors.Is(err, net.ErrClosed) {
				m.failed(userID, value.ID, err)
			}
			return
		}
		go func() {
			var outgoing net.Conn
			var err error
			target := net.JoinHostPort(value.TargetHost, fmt.Sprint(value.TargetPort))
			if value.Kind == "remote" {
				outgoing, err = m.dialer.DialContext(context.Background(), "tcp", target)
			} else {
				outgoing, err = client.Dial("tcp", target)
			}
			if err != nil {
				incoming.Close()
				return
			}
			go func() { _, _ = io.Copy(outgoing, incoming); _ = outgoing.Close() }()
			_, _ = io.Copy(incoming, outgoing)
			_ = incoming.Close()
		}()
	}
}
func (m *Manager) failed(userID, id string, err error) {
	m.mu.Lock()
	run := m.active[id]
	delete(m.active, id)
	m.mu.Unlock()
	if run != nil {
		run.listener.Close()
		run.ssh.Close()
	}
	_ = m.store.UpdatePortForward(userID, id, "error", err.Error())
}
func (m *Manager) Stop(userID, id string) error {
	value, err := m.store.PortForward(userID, id)
	if err != nil {
		return err
	}
	m.mu.Lock()
	run := m.active[id]
	delete(m.active, id)
	m.mu.Unlock()
	if run != nil {
		_ = run.listener.Close()
		_ = run.ssh.Close()
	}
	return m.store.UpdatePortForward(value.UserID, id, "stopped", "")
}
func (m *Manager) Delete(userID, id string) error {
	_ = m.Stop(userID, id)
	return m.store.DeletePortForward(userID, id)
}
