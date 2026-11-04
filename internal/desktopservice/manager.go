// Package desktopservice manages the web service owned by the Windows launcher.
// The worker is the same EXE, with its lifetime tied to a private stdin pipe.
package desktopservice

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"
)

type Credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Reset    bool   `json:"reset"`
}

type Ready struct {
	URL         string       `json:"url"`
	Credentials *Credentials `json:"credentials,omitempty"`
	Error       string       `json:"error,omitempty"`
}

type Status struct {
	Mode      string    `json:"mode"`
	State     string    `json:"state"`
	URL       string    `json:"url"`
	StartedAt time.Time `json:"startedAt"`
	LastError string    `json:"lastError"`
}

type Options struct {
	Command      func(mode string) *exec.Cmd
	StartTimeout time.Duration
	StopTimeout  time.Duration
}

type worker struct {
	cmd   *exec.Cmd
	input io.WriteCloser
	done  chan struct{}
}

type Manager struct {
	op          sync.Mutex
	mu          sync.Mutex
	options     Options
	worker      *worker
	status      Status
	credentials *Credentials
	closed      bool
}

func New(options Options) *Manager {
	if options.StartTimeout == 0 {
		options.StartTimeout = 45 * time.Second
	}
	if options.StopTimeout == 0 {
		options.StopTimeout = 20 * time.Second
	}
	return &Manager{options: options, status: Status{State: "stopped"}}
}

func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.status
}

func (m *Manager) Credentials() *Credentials {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.credentials == nil {
		return nil
	}
	value := *m.credentials
	return &value
}

func (m *Manager) ClearCredentials() {
	m.mu.Lock()
	m.credentials = nil
	m.mu.Unlock()
}

func (m *Manager) Start(mode string) (Status, error) {
	if mode != "window" && mode != "service" {
		return m.Status(), errors.New("请选择窗口模式或服务模式")
	}
	m.op.Lock()
	defer m.op.Unlock()
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return m.Status(), errors.New("应用正在退出")
	}
	if m.worker != nil {
		sameMode := m.status.Mode == mode
		m.mu.Unlock()
		if sameMode {
			return m.Status(), nil
		}
		return m.Status(), errors.New("请先停止当前服务，再切换模式")
	}
	m.status = Status{Mode: mode, State: "starting"}
	m.mu.Unlock()
	cmd := m.options.Command(mode)
	input, err := cmd.StdinPipe()
	if err != nil {
		return m.failed(err)
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		_ = input.Close()
		return m.failed(err)
	}
	if err = cmd.Start(); err != nil {
		_ = input.Close()
		_ = output.Close()
		return m.failed(err)
	}
	w := &worker{cmd: cmd, input: input, done: make(chan struct{})}
	m.mu.Lock()
	m.worker = w
	m.mu.Unlock()
	ready := make(chan Ready, 1)
	decoded := make(chan struct{})
	go func() {
		defer close(decoded)
		var value Ready
		if err := json.NewDecoder(io.LimitReader(output, 64<<10)).Decode(&value); err != nil {
			value.Error = "服务未能启动，请查看 velin-web-ssh.log"
		}
		ready <- value
	}()
	go func() {
		// Wait closes StdoutPipe, so let the startup packet finish reading
		// first, including when a worker reports an error and exits at once.
		<-decoded
		err := cmd.Wait()
		_ = input.Close()
		m.mu.Lock()
		if m.worker == w {
			m.worker = nil
			if m.status.State != "stopping" {
				m.status.State = "error"
				m.status.LastError = "服务意外退出，请查看日志后重试"
				if err != nil {
					m.status.LastError += ": " + err.Error()
				}
			}
		}
		m.mu.Unlock()
		close(w.done)
	}()
	var value Ready
	select {
	case value = <-ready:
	case <-time.After(m.options.StartTimeout):
		value.Error = "服务启动超时，请查看日志后重试"
	}
	if value.Error != "" || value.URL == "" {
		if value.Error == "" {
			value.Error = "服务没有返回访问地址"
		}
		_ = cmd.Process.Kill()
		<-w.done
		return m.failed(errors.New(value.Error))
	}
	m.mu.Lock()
	if m.worker != w {
		m.mu.Unlock()
		return m.Status(), errors.New("服务在启动时退出，请查看日志")
	}
	m.credentials = value.Credentials
	m.status.State, m.status.URL, m.status.StartedAt = "running", value.URL, time.Now()
	m.mu.Unlock()
	return m.Status(), nil
}

func (m *Manager) failed(err error) (Status, error) {
	m.mu.Lock()
	m.status.State, m.status.LastError = "error", err.Error()
	m.mu.Unlock()
	return m.Status(), err
}

func (m *Manager) Stop() (Status, error) {
	m.op.Lock()
	defer m.op.Unlock()
	return m.stop()
}

func (m *Manager) stop() (Status, error) {
	m.mu.Lock()
	w := m.worker
	if w == nil {
		m.status.State, m.status.LastError = "stopped", ""
		m.mu.Unlock()
		return m.Status(), nil
	}
	m.status.State = "stopping"
	m.mu.Unlock()
	_, _ = io.WriteString(w.input, "stop\n")
	_ = w.input.Close()
	select {
	case <-w.done:
	case <-time.After(m.options.StopTimeout):
		if err := w.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return m.failed(fmt.Errorf("停止服务失败: %w", err))
		}
		<-w.done
	}
	m.mu.Lock()
	m.status.State, m.status.LastError = "stopped", ""
	m.status.URL, m.status.StartedAt = "", time.Time{}
	m.mu.Unlock()
	return m.Status(), nil
}

func (m *Manager) Close() {
	m.op.Lock()
	defer m.op.Unlock()
	m.mu.Lock()
	m.closed = true
	m.mu.Unlock()
	_, _ = m.stop()
}
