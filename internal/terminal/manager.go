package terminal

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/ttyob/velin-web-ssh/internal/netdial"
	"github.com/ttyob/velin-web-ssh/internal/security"
	"github.com/ttyob/velin-web-ssh/internal/store"
	"golang.org/x/crypto/ssh"
)

var (
	ErrHostKeyUnknown     = errors.New("host key is not trusted")
	ErrHostKeyChanged     = errors.New("host key changed")
	ErrNotController      = errors.New("terminal is controlled by another client")
	ErrNormalSessionEnded = errors.New("normal SSH sessions cannot be restored after the VelinWebSSH service restarts")
)

const (
	tmuxHistoryLimit       = 100000
	terminalOutputBufferMB = 64
)

type HostKeyError struct{ Kind, Fingerprint, PublicKey, HostName, Address string }

func (e *HostKeyError) Error() string { return e.Kind + ": " + e.Fingerprint }

type CreateRequest struct {
	Host               store.Host
	Credential         store.Credential
	Secret, Passphrase string
	TrustFingerprint   string
	Name               string
	SessionMode        string
}
type TestResult struct {
	LatencyMS    int64  `json:"latencyMs"`
	TmuxVersion  string `json:"tmuxVersion"`
	SessionMode  string `json:"sessionMode"`
	Platform     string `json:"platform"`
	Distribution string `json:"distribution"`
	Fingerprint  string `json:"fingerprint,omitempty"`
}

type Manager struct {
	store        *store.Store
	vault        *security.Vault
	deploymentID string
	recordingDir string
	ffmpegBinary string
	dialer       netdial.Dialer
	mu           sync.RWMutex
	tmuxLocks    sync.Map
	sessions     map[string]*Session
}

type activeRecording struct {
	meta      store.TerminalRecording
	bytes     int64
	startedAt time.Time
	file      *os.File
}

func (m *Manager) DialSaved(ctx context.Context, userID, hostID string) (*ssh.Client, store.Host, error) {
	host, err := m.store.Host(userID, hostID)
	if err != nil {
		return nil, host, err
	}
	var credential store.Credential
	if host.CredentialID != "" {
		credential, err = m.store.Credential(userID, host.CredentialID)
		if err != nil {
			return nil, host, err
		}
	}
	client, err := m.dial(ctx, userID, host, credential, "", "", "")
	return client, host, err
}

type Session struct {
	meta                  store.TerminalSession
	manager               *Manager
	mu                    sync.RWMutex
	broadcastMu           sync.Mutex
	historyScrollMu       sync.Mutex
	commandMu             sync.Mutex
	sshClient             *ssh.Client
	sshSession            *ssh.Session
	commandRunner         *commandRunner
	recordingMu           sync.Mutex
	recording             *activeRecording
	stdin                 io.WriteCloser
	subs                  map[string]*terminalSubscriber
	controller            string
	controllerSeen        time.Time
	pendingControl        string
	buffer                *ringBuffer
	streamID              string
	tmuxCaptureSafe       bool
	tmuxHistoryMode       bool
	tmuxHistoryPos        int
	tmuxHistoryKnown      bool
	historyScrollPending  bool
	historyScrollRunning  bool
	historyScrollClient   string
	historyScrollPosition int
	historyScrollEnd      int
	historyScrollSequence uint64
	historySize           int
	historySizeUpdated    time.Time
	closed                bool
	connectCancel         context.CancelFunc
}

type terminalSubscriber struct {
	events       chan Event
	done         chan struct{}
	doneOnce     sync.Once
	reconnectKey string
	canControl   bool
}

func newTerminalSubscriber(reconnectKey string, canControl bool) *terminalSubscriber {
	return &terminalSubscriber{events: make(chan Event, 256), done: make(chan struct{}), reconnectKey: reconnectKey, canControl: canControl}
}

func (s *terminalSubscriber) close() {
	s.doneOnce.Do(func() { close(s.done) })
}

type Event struct {
	Type        string `json:"type"`
	Data        string `json:"data,omitempty"`
	Status      string `json:"status,omitempty"`
	Message     string `json:"message,omitempty"`
	ClientID    string `json:"clientID,omitempty"`
	Controller  string `json:"controller,omitempty"`
	Truncated   bool   `json:"truncated,omitempty"`
	Requester   string `json:"requester,omitempty"`
	ReplayFinal bool   `json:"replayFinal,omitempty"`
	StreamID    string `json:"streamID,omitempty"`
	Offset      uint64 `json:"offset,omitempty"`
	HistorySize int    `json:"historySize,omitempty"`
	Position    int    `json:"position,omitempty"`
	Sequence    uint64 `json:"sequence,omitempty"`
	DurationMS  int64  `json:"durationMs,omitempty"`
	Error       string `json:"error,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
	HostName    string `json:"hostName,omitempty"`
	HostAddress string `json:"hostAddress,omitempty"`
}

type Replay struct {
	StreamID  string
	Offset    uint64
	Segments  [][]byte
	Truncated bool
}

func NewManager(s *store.Store, vault *security.Vault, deploymentID string, recordingDirs ...string) *Manager {
	_ = s.EndStaleNormalSessions(ErrNormalSessionEnded.Error())
	recordingDir := "data/recordings"
	if len(recordingDirs) > 0 && recordingDirs[0] != "" {
		recordingDir = recordingDirs[0]
	}
	return &Manager{store: s, vault: vault, deploymentID: deploymentID, recordingDir: recordingDir, ffmpegBinary: "ffmpeg", dialer: netdial.Direct{}, sessions: make(map[string]*Session)}
}

func NewManagerWithFFmpeg(s *store.Store, vault *security.Vault, deploymentID, recordingDir, ffmpegBinary string, dialers ...netdial.Dialer) *Manager {
	manager := NewManager(s, vault, deploymentID, recordingDir)
	if strings.TrimSpace(ffmpegBinary) != "" {
		manager.ffmpegBinary = ffmpegBinary
	}
	if len(dialers) > 0 && dialers[0] != nil {
		manager.dialer = dialers[0]
	}
	return manager
}

func (m *Manager) tmuxLock(meta store.TerminalSession) *sync.Mutex {
	key := meta.HostID + "\x00" + meta.TmuxSocket
	lock, _ := m.tmuxLocks.LoadOrStore(key, &sync.Mutex{})
	return lock.(*sync.Mutex)
}

func (s *Session) runCommand(client *ssh.Client, command string) ([]byte, error) {
	s.commandMu.Lock()
	runner := s.commandRunner
	if runner == nil && client != nil {
		var err error
		runner, err = newCommandRunner(client)
		if err == nil {
			s.commandRunner = runner
		}
	}
	s.commandMu.Unlock()
	if runner == nil {
		return run(client, command)
	}
	out, err := runner.Run(command)
	if err != nil {
		s.commandMu.Lock()
		if s.commandRunner == runner {
			s.commandRunner = nil
		}
		s.commandMu.Unlock()
		_ = runner.Close()
	}
	return out, err
}

func normalizeSessionMode(preferred, fallback string) string {
	if preferred == "normal" || (preferred == "" && fallback == "normal") {
		return "normal"
	}
	return "tmux"
}

func (m *Manager) ActiveCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.sessions)
}

func (m *Manager) CloseAll() {
	m.mu.RLock()
	sessions := make([]*Session, 0, len(m.sessions))
	for _, session := range m.sessions {
		sessions = append(sessions, session)
	}
	m.mu.RUnlock()
	for _, session := range sessions {
		session.close("background", "service operation")
	}
}

func (m *Manager) Create(ctx context.Context, userID string, req CreateRequest) (store.TerminalSession, error) {
	id := uuid.NewString()
	mode := normalizeSessionMode(req.SessionMode, req.Host.SessionMode)
	meta := store.TerminalSession{ID: id, UserID: userID, HostID: req.Host.ID, CredentialID: req.Credential.ID, Name: req.Name, RemoteUser: req.Host.Username, SessionMode: mode, TmuxSocket: "velin-webssh-" + m.deploymentID, TmuxName: "ws_" + strings.ReplaceAll(id, "-", ""), OwnerMarker: m.deploymentID + ":" + userID + ":" + id, Status: "creating"}
	if meta.Name == "" {
		meta.Name = req.Host.Name
	}
	if err := m.store.SaveTerminal(meta); err != nil {
		return meta, err
	}
	sess := &Session{meta: meta, manager: m, subs: make(map[string]*terminalSubscriber), buffer: newRingBuffer(terminalOutputBufferMB << 20), streamID: uuid.NewString()}
	connectCtx, cancel := context.WithCancel(context.Background())
	sess.connectCancel = cancel
	m.mu.Lock()
	m.sessions[id] = sess
	m.mu.Unlock()
	go func() {
		if err := sess.connect(connectCtx, req.Host, req.Credential, req.Secret, req.Passphrase, req.TrustFingerprint, true); err != nil {
			if sess.Meta().Status == "ended" && errors.Is(err, context.Canceled) {
				return
			}
			status := "unreachable"
			var hostKeyErr *HostKeyError
			switch {
			case errors.As(err, &hostKeyErr):
				status = "host_key_required"
			case strings.Contains(strings.ToLower(err.Error()), "credential required"):
				status = "auth_required"
			}
			sess.closeWithDetails(status, err.Error(), hostKeyErr)
		}
	}()
	return sess.Meta(), nil
}

func (m *Manager) Test(ctx context.Context, userID string, host store.Host, credential store.Credential, secret, passphrase, trust string) (TestResult, error) {
	started := time.Now()
	client, err := m.dial(ctx, userID, host, credential, secret, passphrase, trust)
	if err != nil {
		return TestResult{}, err
	}
	defer client.Close()
	platform, distribution := detectHostSystem(client)
	if platform != "" {
		_ = m.store.UpdateHostSystem(userID, host.ID, platform, distribution)
	}
	mode := normalizeSessionMode("", host.SessionMode)
	if mode == "normal" {
		return TestResult{LatencyMS: time.Since(started).Milliseconds(), SessionMode: mode, Platform: platform, Distribution: distribution}, nil
	}
	out, err := run(client, "command -v tmux >/dev/null 2>&1 && tmux -V")
	if err != nil {
		return TestResult{}, fmt.Errorf("tmux is required on the remote host: %s", cleanOutput(out, err))
	}
	return TestResult{LatencyMS: time.Since(started).Milliseconds(), TmuxVersion: strings.TrimSpace(string(out)), SessionMode: mode, Platform: platform, Distribution: distribution}, nil
}

func (m *Manager) Restore(ctx context.Context, userID, id, secret, passphrase, trust string) (*Session, error) {
	m.mu.RLock()
	existing := m.sessions[id]
	m.mu.RUnlock()
	if existing != nil {
		if existing.meta.UserID != userID {
			return nil, sql.ErrNoRows
		}
		return existing, nil
	}
	meta, err := m.store.Terminal(userID, id)
	if err != nil {
		return nil, err
	}
	if meta.Status == "ended" {
		return nil, errors.New("terminal session has ended")
	}
	if meta.SessionMode == "normal" {
		_ = m.store.UpdateTerminalStatus(userID, id, "ended", ErrNormalSessionEnded.Error())
		return nil, ErrNormalSessionEnded
	}
	host, err := m.store.Host(userID, meta.HostID)
	if err != nil {
		return nil, err
	}
	var cred store.Credential
	if meta.CredentialID != "" {
		cred, err = m.store.Credential(userID, meta.CredentialID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
	}
	sess := &Session{meta: meta, manager: m, subs: make(map[string]*terminalSubscriber), buffer: newRingBuffer(terminalOutputBufferMB << 20), streamID: uuid.NewString()}
	m.mu.Lock()
	if prior := m.sessions[id]; prior != nil {
		m.mu.Unlock()
		if prior.meta.UserID != userID {
			return nil, sql.ErrNoRows
		}
		return prior, nil
	}
	m.sessions[id] = sess
	m.mu.Unlock()
	if err = sess.connect(ctx, host, cred, secret, passphrase, trust, false); err != nil {
		m.mu.Lock()
		delete(m.sessions, id)
		m.mu.Unlock()
		status := "unreachable"
		if secret == "" && cred.ID == "" {
			status = "auth_required"
		}
		_ = m.store.UpdateTerminalStatus(userID, id, status, err.Error())
		return nil, err
	}
	return sess, nil
}

func (m *Manager) Get(userID, id string) (*Session, error) {
	m.mu.RLock()
	s := m.sessions[id]
	m.mu.RUnlock()
	if s == nil || s.meta.UserID != userID {
		return nil, sql.ErrNoRows
	}
	return s, nil
}

func (m *Manager) StartRecording(userID, id string) (store.TerminalRecording, error) {
	s, err := m.Get(userID, id)
	if err != nil {
		return store.TerminalRecording{}, err
	}
	s.recordingMu.Lock()
	defer s.recordingMu.Unlock()
	if s.recording != nil {
		if s.recording.file != nil {
			return store.TerminalRecording{}, errors.New("分享录屏正在进行")
		}
		return s.recording.meta, nil
	}
	if err = os.MkdirAll(m.recordingDir, 0o700); err != nil {
		return store.TerminalRecording{}, err
	}
	recordingID := uuid.NewString()
	path := filepath.Join(m.recordingDir, recordingID+".mp4")
	meta := store.TerminalRecording{ID: recordingID, UserID: userID, SessionID: id, SessionName: s.Meta().Name, Path: path, Status: "recording", StartedAt: time.Now().UTC()}
	if err = m.store.CreateRecording(meta); err != nil {
		return store.TerminalRecording{}, err
	}
	s.recording = &activeRecording{meta: meta}
	return meta, nil
}

func (m *Manager) StopRecording(userID, id string) (store.TerminalRecording, error) {
	s, err := m.Get(userID, id)
	if err != nil {
		return store.TerminalRecording{}, err
	}
	return s.stopRecording("stopped"), nil
}

func (m *Manager) StartShareRecording(userID, id string) (store.TerminalRecording, error) {
	s, err := m.Get(userID, id)
	if err != nil {
		return store.TerminalRecording{}, err
	}
	s.recordingMu.Lock()
	defer s.recordingMu.Unlock()
	if s.recording != nil {
		return store.TerminalRecording{}, errors.New("当前会话已有进行中的录制")
	}
	if err = os.MkdirAll(m.recordingDir, 0o700); err != nil {
		return store.TerminalRecording{}, err
	}
	recordingID := uuid.NewString()
	path := filepath.Join(m.recordingDir, recordingID+".cast")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		return store.TerminalRecording{}, err
	}
	startedAt := time.Now().UTC()
	header, _ := json.Marshal(map[string]any{
		"version": 2, "width": 120, "height": 30, "timestamp": startedAt.Unix(),
		"env": map[string]string{"TERM": "xterm-256color", "SHELL": "shell"},
	})
	header = append(header, '\n')
	if _, err = file.Write(header); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return store.TerminalRecording{}, err
	}
	meta := store.TerminalRecording{ID: recordingID, UserID: userID, SessionID: id, SessionName: s.Meta().Name, Path: path, Status: "recording", Bytes: int64(len(header)), StartedAt: startedAt}
	if err = m.store.CreateRecording(meta); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return store.TerminalRecording{}, err
	}
	s.recording = &activeRecording{meta: meta, bytes: int64(len(header)), startedAt: startedAt, file: file}
	return meta, nil
}

func (m *Manager) StopRecordingIfID(userID, id, recordingID string) store.TerminalRecording {
	s, err := m.Get(userID, id)
	if err != nil {
		return store.TerminalRecording{}
	}
	s.recordingMu.Lock()
	matches := s.recording != nil && s.recording.meta.ID == recordingID
	s.recordingMu.Unlock()
	if !matches {
		return store.TerminalRecording{}
	}
	return s.stopRecording("stopped")
}

func (s *Session) stopRecording(status string) store.TerminalRecording {
	s.recordingMu.Lock()
	defer s.recordingMu.Unlock()
	if s.recording == nil {
		return store.TerminalRecording{}
	}
	active := s.recording
	s.recording = nil
	if active.file != nil {
		_ = active.file.Sync()
		_ = active.file.Close()
	}
	finished := time.Now().UTC()
	active.meta.Status = status
	active.meta.Bytes = active.bytes
	active.meta.FinishedAt = &finished
	_ = s.manager.store.FinishRecording(active.meta.UserID, active.meta.ID, status, active.bytes, finished)
	return active.meta
}

func (s *Session) recordOutput(raw []byte) {
	if len(raw) == 0 {
		return
	}
	s.recordingMu.Lock()
	defer s.recordingMu.Unlock()
	active := s.recording
	if active == nil || active.file == nil {
		return
	}
	event, err := json.Marshal([]any{time.Since(active.startedAt).Seconds(), "o", string(raw)})
	if err != nil {
		return
	}
	event = append(event, '\n')
	n, writeErr := active.file.Write(event)
	active.bytes += int64(n)
	active.meta.Bytes = active.bytes
	if writeErr != nil {
		_ = active.file.Close()
		active.file = nil
	}
}

const maxRecordingUploadBytes int64 = 1 << 30

func (m *Manager) UploadRecording(ctx context.Context, userID, recordingID string, source io.Reader) (store.TerminalRecording, error) {
	value, err := m.store.Recording(userID, recordingID)
	if err != nil {
		return store.TerminalRecording{}, err
	}
	if value.Status != "recording" {
		return store.TerminalRecording{}, errors.New("录制已经结束")
	}
	s, err := m.Get(userID, value.SessionID)
	if err != nil {
		return store.TerminalRecording{}, err
	}
	s.recordingMu.Lock()
	active := s.recording
	if active == nil || active.meta.ID != recordingID {
		s.recordingMu.Unlock()
		return store.TerminalRecording{}, errors.New("当前录制已失效")
	}
	s.recording = nil
	s.recordingMu.Unlock()

	tempInput := value.Path + ".upload"
	tempOutput := value.Path + ".transcode"
	defer os.Remove(tempInput)
	defer os.Remove(tempOutput)
	file, err := os.OpenFile(tempInput, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		return m.finishUploadedRecording(value, "error", 0, err)
	}
	written, copyErr := io.Copy(file, io.LimitReader(source, maxRecordingUploadBytes+1))
	closeErr := file.Close()
	if copyErr != nil {
		return m.finishUploadedRecording(value, "error", 0, copyErr)
	}
	if closeErr != nil {
		return m.finishUploadedRecording(value, "error", 0, closeErr)
	}
	if written == 0 || written > maxRecordingUploadBytes {
		return m.finishUploadedRecording(value, "error", written, errors.New("录制文件大小无效"))
	}

	command := exec.CommandContext(ctx, m.ffmpegBinary,
		"-hide_banner", "-loglevel", "error", "-y",
		"-i", tempInput,
		"-an", "-c:v", "libx264", "-preset", "slow", "-crf", "16",
		"-pix_fmt", "yuv420p", "-movflags", "+faststart",
		"-f", "mp4",
		tempOutput,
	)
	if output, commandErr := command.CombinedOutput(); commandErr != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			message = commandErr.Error()
		}
		return m.finishUploadedRecording(value, "error", written, fmt.Errorf("MP4 转码失败: %s", message))
	}
	if err = os.Rename(tempOutput, value.Path); err != nil {
		return m.finishUploadedRecording(value, "error", written, err)
	}
	if err = os.Chmod(value.Path, 0o600); err != nil {
		return m.finishUploadedRecording(value, "error", written, err)
	}
	info, err := os.Stat(value.Path)
	if err != nil {
		return m.finishUploadedRecording(value, "error", written, err)
	}
	finished := time.Now().UTC()
	value.Status = "stopped"
	value.Bytes = info.Size()
	value.FinishedAt = &finished
	if err = m.store.FinishRecording(userID, recordingID, value.Status, value.Bytes, finished); err != nil {
		return store.TerminalRecording{}, err
	}
	return value, nil
}

func (m *Manager) finishUploadedRecording(value store.TerminalRecording, status string, bytes int64, cause error) (store.TerminalRecording, error) {
	finished := time.Now().UTC()
	_ = m.store.FinishRecording(value.UserID, value.ID, status, bytes, finished)
	return store.TerminalRecording{}, cause
}

func (s *Session) WriteTask(data []byte) error {
	s.mu.RLock()
	w, closed := s.stdin, s.closed
	s.mu.RUnlock()
	if closed || w == nil {
		return errors.New("terminal not attached")
	}
	_, err := w.Write(data)
	return err
}

func (m *Manager) Rename(userID, id, name string) error {
	if err := m.store.RenameTerminal(userID, id, name); err != nil {
		return err
	}
	m.mu.RLock()
	s := m.sessions[id]
	m.mu.RUnlock()
	if s != nil && s.meta.UserID == userID {
		s.mu.Lock()
		s.meta.Name = name
		s.mu.Unlock()
	}
	return nil
}

func (m *Manager) Terminate(ctx context.Context, userID, id string, secret, passphrase string) error {
	meta, err := m.store.Terminal(userID, id)
	if err != nil {
		return err
	}
	s, err := m.Get(userID, id)
	if err == nil {
		if err = s.killRemote(ctx); err != nil {
			return err
		}
		s.close("ended", "Terminated by user")
		return nil
	}
	if meta.SessionMode == "normal" {
		return m.store.UpdateTerminalStatus(userID, id, "ended", "Terminated by user")
	}
	host, err := m.store.Host(userID, meta.HostID)
	if err != nil {
		return err
	}
	var cred store.Credential
	if meta.CredentialID != "" {
		cred, _ = m.store.Credential(userID, meta.CredentialID)
	}
	client, err := m.dial(ctx, userID, host, cred, secret, passphrase, "")
	if err != nil {
		return err
	}
	defer client.Close()
	if err = validateOwnership(client, meta); err != nil {
		if isTmuxTargetMissing(err.Error()) || isTmuxMissing(err.Error()) {
			return m.store.UpdateTerminalStatus(userID, id, "ended", "Terminated by user")
		}
		return err
	}
	cmd := fmt.Sprintf("tmux -L %s kill-session -t %s", meta.TmuxSocket, meta.TmuxName)
	if out, e := run(client, cmd); e != nil && !isTmuxTargetMissing(cleanOutput(out, e)) {
		return fmt.Errorf("terminate tmux: %s", cleanOutput(out, e))
	}
	return m.store.UpdateTerminalStatus(userID, id, "ended", "Terminated by user")
}

func (s *Session) Meta() store.TerminalSession { s.mu.RLock(); defer s.mu.RUnlock(); return s.meta }

func (s *Session) SSHClient() *ssh.Client {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sshClient
}

func (s *Session) CurrentDirectory() (string, error) {
	s.mu.RLock()
	client, meta := s.sshClient, s.meta
	s.mu.RUnlock()
	if client == nil {
		return "", errors.New("terminal connection is not available")
	}
	if meta.SessionMode == "normal" {
		return "", errors.New("current directory is unavailable in normal SSH mode")
	}
	out, err := s.runCommand(client, fmt.Sprintf("tmux -L %s display-message -p -t %s '#{pane_current_path}'", meta.TmuxSocket, meta.TmuxName))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (m *Manager) DockerInstalled(ctx context.Context, userID, sessionID string) (bool, error) {
	session, err := m.Get(userID, sessionID)
	if err != nil {
		return false, err
	}
	session.mu.RLock()
	client := session.sshClient
	session.mu.RUnlock()
	if client == nil {
		return false, errors.New("terminal connection is not available")
	}
	out, err := session.runCommand(client, "command -v docker 2>/dev/null || true")
	if err != nil {
		return false, fmt.Errorf("check Docker installation: %w", err)
	}
	return strings.TrimSpace(string(out)) != "", nil
}

func (s *Session) ForegroundCommand() (string, error) {
	s.mu.RLock()
	client, meta := s.sshClient, s.meta
	s.mu.RUnlock()
	if client == nil {
		return "", errors.New("terminal connection is not available")
	}
	if meta.SessionMode == "normal" {
		return "", errors.New("foreground command is unavailable in normal SSH mode")
	}
	out, err := s.runCommand(client, fmt.Sprintf("tmux -L %s display-message -p -t %s '#{pane_current_command}'", meta.TmuxSocket, meta.TmuxName))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (s *Session) Snapshot(maxLines int) []byte {
	if maxLines < 1 {
		return nil
	}
	if maxLines > 500 {
		maxLines = 500
	}
	s.mu.RLock()
	client, meta, closed, captureSafe := s.sshClient, s.meta, s.closed, s.tmuxCaptureSafe
	buffered := s.buffer.TailLines(maxLines, terminalOutputBufferMB<<20)
	s.mu.RUnlock()
	if len(buffered) > 0 {
		return prepareTerminalSnapshot(buffered)
	}
	if closed || meta.SessionMode == "normal" {
		return nil
	}
	if !captureSafe {
		return nil
	}
	if client == nil {
		return nil
	}
	cmd := fmt.Sprintf("tmux -L %s capture-pane -p -e -t %s -S -%d", meta.TmuxSocket, meta.TmuxName, maxLines)
	lock := s.manager.tmuxLock(meta)
	lock.Lock()
	data, err := s.runCommand(client, cmd)
	lock.Unlock()
	if err != nil || len(data) == 0 {
		return nil
	}
	return prepareTerminalSnapshot(data)
}

func prepareTerminalSnapshot(data []byte) []byte {
	if len(data) == 0 {
		return nil
	}
	data = normalizeTerminalLines(data)
	out := make([]byte, 0, len(data)+32)
	out = append(out, []byte("\x1b[0m\x1b[2J\x1b[H")...)
	out = append(out, data...)
	out = append(out, []byte("\x1b[0m")...)
	return out
}

func tailTerminalLines(data []byte, maxLines int) []byte {
	if maxLines < 1 || len(data) == 0 {
		return nil
	}
	end := len(data)
	index := end - 1
	if data[index] == '\n' {
		index--
		if index >= 0 && data[index] == '\r' {
			index--
		}
	}
	lines := 0
	for ; index >= 0; index-- {
		if data[index] != '\n' {
			continue
		}
		lines++
		if lines == maxLines {
			return bytes.Clone(data[index+1 : end])
		}
	}
	return bytes.Clone(data[:end])
}

func tmuxCaptureSupported(version string) bool {
	version = strings.ToLower(strings.TrimSpace(version))
	return strings.HasPrefix(version, "tmux ") && !strings.Contains(version, "next-")
}

func (s *Session) connect(ctx context.Context, host store.Host, cred store.Credential, secret, passphrase, trust string, create bool) error {
	client, err := s.manager.dial(ctx, s.meta.UserID, host, cred, secret, passphrase, trust)
	if err != nil {
		return err
	}
	platform, distribution := detectHostSystem(client)
	if platform != "" {
		_ = s.manager.store.UpdateHostSystem(s.meta.UserID, host.ID, platform, distribution)
	}
	tmuxMode := s.meta.SessionMode != "normal"
	if tmuxMode {
		// Startup restores several workspace tabs concurrently. Serialize tmux
		// server mutations and capture-pane so older remote tmux versions do not
		// process competing configuration and history requests.
		lock := s.manager.tmuxLock(s.meta)
		lock.Lock()
		defer lock.Unlock()
		out, versionErr := run(client, "command -v tmux >/dev/null 2>&1 && tmux -V")
		if versionErr != nil {
			client.Close()
			return fmt.Errorf("tmux is required on the remote host: %s", cleanOutput(out, versionErr))
		}
		s.mu.Lock()
		s.tmuxCaptureSafe = tmuxCaptureSupported(strings.TrimSpace(string(out)))
		s.mu.Unlock()
		if create {
			startDir := ""
			if strings.TrimSpace(host.InitialDir) != "" {
				startDir = " -c " + shellQuote(strings.TrimSpace(host.InitialDir))
			}
			cmd := fmt.Sprintf("tmux -L %s new-session -d -s %s%s \\; set-option -t %s @velin_owner %s \\; set-option -t %s history-limit %d \\; set-option -t %s status off \\; set-window-option -t %s alternate-screen off", s.meta.TmuxSocket, s.meta.TmuxName, startDir, s.meta.TmuxName, s.meta.OwnerMarker, s.meta.TmuxName, tmuxHistoryLimit, s.meta.TmuxName, s.meta.TmuxName)
			if out, err := run(client, cmd); err != nil {
				client.Close()
				return fmt.Errorf("create tmux session: %s", cleanOutput(out, err))
			}
		} else if err := validateOwnership(client, s.meta); err != nil {
			client.Close()
			return err
		}
		if out, err := run(client, fmt.Sprintf("tmux -L %s set-option -t %s status off", s.meta.TmuxSocket, s.meta.TmuxName)); err != nil {
			client.Close()
			return fmt.Errorf("configure tmux session: %s", cleanOutput(out, err))
		}
		if out, err := run(client, fmt.Sprintf("tmux -L %s set-option -t %s history-limit %d", s.meta.TmuxSocket, s.meta.TmuxName, tmuxHistoryLimit)); err != nil {
			client.Close()
			return fmt.Errorf("configure tmux history: %s", cleanOutput(out, err))
		}
		// Full-screen TUIs such as Codex redraw their alternate buffer in place.
		// Keeping them on tmux's normal buffer makes those lines part of history.
		if out, err := run(client, fmt.Sprintf("tmux -L %s set-window-option -t %s alternate-screen off", s.meta.TmuxSocket, s.meta.TmuxName)); err != nil {
			client.Close()
			return fmt.Errorf("configure tmux scrollback: %s", cleanOutput(out, err))
		}
		// Keep browser xterm.js on its normal buffer so tmux history remains
		// available through the browser viewport scrollbar.
		// Do not expose the partial-scroll-region capability to full-screen TUIs.
		// Lines that leave a partial region are overwritten in place and never
		// enter terminal scrollback, which creates gaps in long Codex responses.
		if out, err := run(client, fmt.Sprintf("tmux -L %s set-option -as terminal-overrides ',xterm-256color:smcup@:rmcup@:csr@'", s.meta.TmuxSocket)); err != nil {
			client.Close()
			return fmt.Errorf("configure tmux terminal: %s", cleanOutput(out, err))
		}
	}
	sshSess, err := client.NewSession()
	if err != nil {
		client.Close()
		return err
	}
	modes := ssh.TerminalModes{ssh.ECHO: 1, ssh.TTY_OP_ISPEED: 14400, ssh.TTY_OP_OSPEED: 14400}
	terminalType := host.TerminalType
	if terminalType == "" {
		terminalType = "xterm-256color"
	}
	if err = sshSess.RequestPty(terminalType, 30, 120, modes); err != nil {
		sshSess.Close()
		client.Close()
		return err
	}
	stdin, err := sshSess.StdinPipe()
	if err != nil {
		sshSess.Close()
		client.Close()
		return err
	}
	stdout, err := sshSess.StdoutPipe()
	if err != nil {
		sshSess.Close()
		client.Close()
		return err
	}
	sshSess.Stderr = sshSess.Stdout
	if tmuxMode {
		attach := fmt.Sprintf("exec tmux -L %s attach-session -t %s", s.meta.TmuxSocket, s.meta.TmuxName)
		err = sshSess.Start(attach)
	} else if strings.TrimSpace(host.InitialDir) != "" && platform != "windows" {
		start := fmt.Sprintf("cd %s && exec ${SHELL:-/bin/sh} -l", shellQuote(strings.TrimSpace(host.InitialDir)))
		err = sshSess.Start(start)
	} else {
		err = sshSess.Shell()
	}
	if err != nil {
		sshSess.Close()
		client.Close()
		return err
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		sshSess.Close()
		client.Close()
		return context.Canceled
	}
	s.sshClient = client
	s.sshSession = sshSess
	s.stdin = stdin
	s.closed = false
	s.meta.Status = "attached"
	s.meta.LastError = ""
	s.mu.Unlock()
	_ = s.manager.store.UpdateTerminalStatus(s.meta.UserID, s.meta.ID, "attached", "")
	go s.readLoop(stdout)
	if host.KeepaliveInterval > 0 {
		go keepalive(client, time.Duration(host.KeepaliveInterval)*time.Second)
	}
	return nil
}

func (m *Manager) dial(ctx context.Context, userID string, host store.Host, cred store.Credential, secret, passphrase, trust string) (*ssh.Client, error) {
	return m.dialHost(ctx, userID, host, cred, secret, passphrase, trust, make(map[string]bool), 0)
}

func (m *Manager) dialHost(ctx context.Context, userID string, host store.Host, cred store.Credential, secret, passphrase, trust string, visited map[string]bool, depth int) (*ssh.Client, error) {
	if depth > 8 {
		return nil, errors.New("跳板机链路不能超过 8 层")
	}
	hostKey := host.ID
	if hostKey == "" {
		hostKey = net.JoinHostPort(host.Address, fmt.Sprint(host.Port))
	}
	if visited[hostKey] {
		return nil, errors.New("检测到跳板机循环引用")
	}
	visited[hostKey] = true
	defer delete(visited, hostKey)

	if cred.ID != "" {
		var err error
		secret, err = m.vault.Decrypt(cred.Secret)
		if err != nil {
			return nil, err
		}
		if cred.Passphrase != "" {
			passphrase, err = m.vault.Decrypt(cred.Passphrase)
			if err != nil {
				return nil, err
			}
		}
	} else if secret == "" && host.PasswordEnc != "" {
		var err error
		secret, err = m.vault.Decrypt(host.PasswordEnc)
		if err != nil {
			return nil, err
		}
	}
	if secret == "" {
		return nil, errors.New("SSH credential required")
	}
	var auth ssh.AuthMethod
	if cred.Kind == "key" || strings.Contains(secret, "PRIVATE KEY") {
		signer, err := parseKey([]byte(secret), []byte(passphrase))
		if err != nil {
			return nil, fmt.Errorf("invalid private key: %w", err)
		}
		auth = ssh.PublicKeys(signer)
	} else {
		auth = ssh.Password(secret)
	}
	callback := func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		fp := ssh.FingerprintSHA256(key)
		_, stored, err := m.store.KnownHost(userID, host.Address, host.Port)
		if errors.Is(err, sql.ErrNoRows) {
			if trust != fp {
				return &HostKeyError{Kind: "unknown_host_key", Fingerprint: fp, PublicKey: base64.StdEncoding.EncodeToString(key.Marshal()), HostName: host.Name, Address: net.JoinHostPort(host.Address, fmt.Sprint(host.Port))}
			}
			return m.store.TrustHost(userID, host.Address, host.Port, fp, base64.StdEncoding.EncodeToString(key.Marshal()))
		}
		if err != nil {
			return err
		}
		if stored != base64.StdEncoding.EncodeToString(key.Marshal()) {
			if trust == fp {
				return m.store.TrustHost(userID, host.Address, host.Port, fp, base64.StdEncoding.EncodeToString(key.Marshal()))
			}
			return &HostKeyError{Kind: "host_key_changed", Fingerprint: fp, PublicKey: base64.StdEncoding.EncodeToString(key.Marshal()), HostName: host.Name, Address: net.JoinHostPort(host.Address, fmt.Sprint(host.Port))}
		}
		return nil
	}
	timeout := time.Duration(host.ConnectTimeout) * time.Second
	if timeout <= 0 {
		timeout = 12 * time.Second
	}
	dialCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	config := &ssh.ClientConfig{User: host.Username, Auth: []ssh.AuthMethod{auth}, HostKeyCallback: callback, Timeout: timeout}
	addr := net.JoinHostPort(host.Address, fmt.Sprint(host.Port))
	var (
		conn       net.Conn
		jumpClient *ssh.Client
		err        error
	)
	if host.JumpHostID != "" {
		jumpHost, loadErr := m.store.Host(userID, host.JumpHostID)
		if loadErr != nil {
			return nil, errors.New("跳板机不存在或无权访问")
		}
		var jumpCredential store.Credential
		if jumpHost.CredentialID != "" {
			jumpCredential, loadErr = m.store.Credential(userID, jumpHost.CredentialID)
			if loadErr != nil {
				return nil, fmt.Errorf("读取跳板机“%s”的凭据失败: %w", jumpHost.Name, loadErr)
			}
		} else if jumpHost.PasswordEnc == "" {
			return nil, fmt.Errorf("跳板机“%s”需要绑定已保存的凭据或主机密码", jumpHost.Name)
		}
		jumpClient, err = m.dialHost(dialCtx, userID, jumpHost, jumpCredential, "", "", trust, visited, depth+1)
		if err != nil {
			return nil, fmt.Errorf("连接跳板机“%s”失败: %w", jumpHost.Name, err)
		}
		conn, err = dialThroughJump(dialCtx, jumpClient, addr)
	} else {
		conn, err = m.dialer.DialContext(dialCtx, "tcp", addr)
	}
	if err != nil {
		if jumpClient != nil {
			_ = jumpClient.Close()
		}
		return nil, err
	}
	if deadline, ok := dialCtx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	c, chs, reqs, err := ssh.NewClientConn(conn, addr, config)
	if err != nil {
		_ = conn.Close()
		if jumpClient != nil {
			_ = jumpClient.Close()
		}
		return nil, err
	}
	_ = conn.SetDeadline(time.Time{})
	client := ssh.NewClient(c, chs, reqs)
	if jumpClient != nil {
		go func() {
			_ = client.Wait()
			_ = jumpClient.Close()
		}()
	}
	return client, nil
}

func dialThroughJump(ctx context.Context, client *ssh.Client, address string) (net.Conn, error) {
	type result struct {
		conn net.Conn
		err  error
	}
	ready := make(chan result, 1)
	go func() {
		conn, err := client.Dial("tcp", address)
		ready <- result{conn: conn, err: err}
	}()
	select {
	case <-ctx.Done():
		_ = client.Close()
		return nil, ctx.Err()
	case value := <-ready:
		return value.conn, value.err
	}
}

func keepalive(client *ssh.Client, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		if _, _, err := client.SendRequest("keepalive@openssh.com", true, nil); err != nil {
			return
		}
	}
}

func shellQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }

func parseKey(key, pass []byte) (ssh.Signer, error) {
	if len(pass) > 0 {
		return ssh.ParsePrivateKeyWithPassphrase(key, pass)
	}
	return ssh.ParsePrivateKey(key)
}

func validateOwnership(client *ssh.Client, meta store.TerminalSession) error {
	cmd := fmt.Sprintf("tmux -L %s show-options -t %s -v @velin_owner", meta.TmuxSocket, meta.TmuxName)
	out, err := run(client, cmd)
	if err != nil {
		message := cleanOutput(out, err)
		if isTmuxMissing(message) {
			return fmt.Errorf("tmux is required on the remote host: %s", message)
		}
		return fmt.Errorf("tmux session not found: %s", message)
	}
	if strings.TrimSpace(string(out)) != meta.OwnerMarker {
		return errors.New("tmux ownership marker mismatch")
	}
	return nil
}

func isTmuxTargetMissing(message string) bool {
	message = strings.ToLower(message)
	return strings.Contains(message, "can't find session") ||
		strings.Contains(message, "no such session") ||
		strings.Contains(message, "no server running on") ||
		(strings.Contains(message, "error connecting to ") && strings.Contains(message, "no such file or directory"))
}

func isTmuxMissing(message string) bool {
	message = strings.ToLower(message)
	return strings.Contains(message, "tmux: command not found") ||
		strings.Contains(message, "tmux: not found") ||
		strings.Contains(message, "command not found: tmux")
}

func run(client *ssh.Client, cmd string) ([]byte, error) {
	s, err := client.NewSession()
	if err != nil {
		return nil, err
	}
	defer s.Close()
	return s.CombinedOutput(cmd)
}

func (s *Session) restoreTmuxHistory(client *ssh.Client) {
	cmd := fmt.Sprintf("tmux -L %s capture-pane -p -e -t %s -S - -E -1", s.meta.TmuxSocket, s.meta.TmuxName)
	data, err := run(client, cmd)
	if err != nil || len(data) == 0 {
		return
	}
	data = normalizeTerminalLines(data)
	s.mu.Lock()
	s.buffer.Write(data)
	s.mu.Unlock()
}

func normalizeTerminalLines(data []byte) []byte {
	data = bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
	return bytes.ReplaceAll(data, []byte("\n"), []byte("\r\n"))
}

func detectHostSystem(client *ssh.Client) (string, string) {
	if strings.Contains(strings.ToLower(string(client.ServerVersion())), "windows") {
		return "windows", ""
	}
	out, err := run(client, `uname -s 2>/dev/null || true; if [ -r /etc/os-release ]; then . /etc/os-release; printf '%s\n%s\n' "$ID" "$ID_LIKE"; fi`)
	if err != nil {
		return "", ""
	}
	lines := strings.Split(strings.ReplaceAll(string(out), "\r\n", "\n"), "\n")
	platform := platformFromName(valueAt(lines, 0))
	if platform != "linux" {
		return platform, ""
	}
	return platform, distributionFromRelease(valueAt(lines, 1), valueAt(lines, 2))
}

func valueAt(values []string, index int) string {
	if index >= len(values) {
		return ""
	}
	return strings.TrimSpace(values[index])
}

func distributionFromRelease(id, idLike string) string {
	id = strings.ToLower(strings.Trim(strings.TrimSpace(id), `"'`))
	idLike = strings.ToLower(strings.Trim(strings.TrimSpace(idLike), `"'`))
	aliases := map[string]string{
		"amzn": "amazon", "ol": "oracle", "opensuse-leap": "opensuse",
		"opensuse-tumbleweed": "opensuse", "sles": "opensuse",
		"rhel": "rhel", "redhat": "rhel", "redhatenterpriseserver": "rhel",
	}
	if canonical := aliases[id]; canonical != "" {
		return canonical
	}
	known := map[string]bool{
		"ubuntu": true, "debian": true, "linuxmint": true, "pop": true,
		"elementary": true, "centos": true, "rocky": true, "almalinux": true,
		"fedora": true, "arch": true, "manjaro": true, "endeavouros": true,
		"alpine": true, "opensuse": true, "gentoo": true, "kali": true,
		"raspbian": true, "nixos": true, "void": true, "amazon": true,
		"oracle": true, "proxmox": true,
	}
	if known[id] {
		return id
	}
	for _, family := range strings.Fields(idLike) {
		family = strings.Trim(family, `"'`)
		if canonical := aliases[family]; canonical != "" {
			return canonical
		}
		if known[family] {
			return family
		}
	}
	if id != "" && len(id) <= 32 {
		return id
	}
	return ""
}

func platformFromName(value string) string {
	name := strings.ToLower(strings.TrimSpace(value))
	switch {
	case strings.Contains(name, "darwin"):
		return "macos"
	case strings.Contains(name, "linux"):
		return "linux"
	case strings.Contains(name, "mingw"), strings.Contains(name, "msys"), strings.Contains(name, "cygwin"), strings.Contains(name, "windows"):
		return "windows"
	case strings.Contains(name, "freebsd"), strings.Contains(name, "openbsd"), strings.Contains(name, "netbsd"), strings.Contains(name, "dragonfly"):
		return "bsd"
	case name != "":
		return "unix"
	default:
		return ""
	}
}
func cleanOutput(out []byte, err error) string {
	msg := strings.TrimSpace(string(out))
	if msg == "" && err != nil {
		msg = err.Error()
	}
	if len(msg) > 300 {
		msg = msg[:300]
	}
	return msg
}

func (s *Session) readLoop(r io.Reader) {
	buf := make([]byte, 32*1024)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			s.broadcastOutput(buf[:n])
		}
		if err != nil {
			status := "background"
			if s.Meta().SessionMode == "normal" {
				status = "ended"
			}
			s.close(status, err.Error())
			return
		}
	}
}
func (s *Session) broadcastOutput(raw []byte) {
	s.broadcastEvent(Event{Type: "output", Data: base64.StdEncoding.EncodeToString(raw)}, raw)
}
func (s *Session) broadcast(ev Event) {
	var raw []byte
	if ev.Type == "output" {
		raw, _ = base64.StdEncoding.DecodeString(ev.Data)
	}
	s.broadcastEvent(ev, raw)
}
func (s *Session) broadcastEvent(ev Event, raw []byte) {
	s.broadcastMu.Lock()
	defer s.broadcastMu.Unlock()
	s.mu.Lock()
	if ev.Type == "output" {
		ev.Offset = s.buffer.Write(raw)
	}
	subs := make([]*terminalSubscriber, 0, len(s.subs))
	for _, sub := range s.subs {
		subs = append(subs, sub)
	}
	s.mu.Unlock()
	s.recordOutput(raw)
	for _, sub := range subs {
		select {
		case sub.events <- ev:
		case <-sub.done:
		}
	}
}
func (s *Session) Subscribe(clientID, resumeStreamID string, resumeOffset uint64, reconnectKeys ...string) (<-chan Event, <-chan struct{}, Replay) {
	return s.subscribe(clientID, resumeStreamID, resumeOffset, true, reconnectKeys...)
}

func (s *Session) SubscribeReadOnly(clientID, resumeStreamID string, resumeOffset uint64) (<-chan Event, <-chan struct{}, Replay) {
	return s.subscribe(clientID, resumeStreamID, resumeOffset, false)
}

func (s *Session) subscribe(clientID, resumeStreamID string, resumeOffset uint64, canControl bool, reconnectKeys ...string) (<-chan Event, <-chan struct{}, Replay) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.streamID == "" {
		s.streamID = uuid.NewString()
	}
	reconnectKey := ""
	if len(reconnectKeys) > 0 {
		reconnectKey = reconnectKeys[0]
	}
	subscriber := newTerminalSubscriber(reconnectKey, canControl)
	s.subs[clientID] = subscriber
	controllerSubscriber := s.subs[s.controller]
	if canControl && reconnectKey != "" && controllerSubscriber != nil && controllerSubscriber.reconnectKey == reconnectKey {
		controllerSubscriber.close()
		s.controller = clientID
		s.controllerSeen = time.Now()
		s.pendingControl = ""
	} else if canControl && (s.controller == "" || time.Since(s.controllerSeen) > 20*time.Second) {
		s.controller = clientID
		s.controllerSeen = time.Now()
	}
	segments, truncated := s.buffer.Segments()
	offset := s.buffer.End()
	if resumeStreamID == s.streamID {
		segments, offset, truncated = s.buffer.SegmentsFrom(resumeOffset)
	}
	replay := Replay{StreamID: s.streamID, Offset: offset, Segments: segments, Truncated: truncated}
	return subscriber.events, subscriber.done, replay
}
func (s *Session) Unsubscribe(clientID string) {
	s.mu.Lock()
	if subscriber := s.subs[clientID]; subscriber != nil {
		delete(s.subs, clientID)
		subscriber.close()
	}
	if s.pendingControl == clientID {
		s.pendingControl = ""
	}
	grant := ""
	if s.controller == clientID {
		s.controller = ""
		if _, waiting := s.subs[s.pendingControl]; waiting {
			grant = s.pendingControl
			s.pendingControl = ""
			s.controller = grant
			s.controllerSeen = time.Now()
		}
	}
	s.mu.Unlock()
	if grant != "" {
		s.broadcast(Event{Type: "controller", Controller: grant})
	}
}
func (s *Session) RequestControl(clientID string) bool {
	s.mu.Lock()
	subscriber := s.subs[clientID]
	if subscriber == nil || !subscriber.canControl {
		s.mu.Unlock()
		return false
	}
	if s.controller == clientID {
		s.controllerSeen = time.Now()
		s.mu.Unlock()
		return true
	}
	if s.controller == "" || time.Since(s.controllerSeen) > 30*time.Second {
		s.controller = clientID
		s.controllerSeen = time.Now()
		s.pendingControl = ""
		s.mu.Unlock()
		s.broadcast(Event{Type: "controller", Controller: clientID})
		return true
	}
	controllerSubscriber := s.subs[s.controller]
	s.pendingControl = clientID
	if controllerSubscriber != nil {
		select {
		case controllerSubscriber.events <- Event{Type: "control_request", Requester: clientID}:
		case <-controllerSubscriber.done:
		default:
		}
	}
	s.mu.Unlock()
	return false
}
func (s *Session) RespondControl(clientID, requester string, approved bool) bool {
	s.mu.Lock()
	if s.controller != clientID || s.pendingControl != requester {
		s.mu.Unlock()
		return false
	}
	s.pendingControl = ""
	requesterSubscriber := s.subs[requester]
	if requesterSubscriber != nil && !requesterSubscriber.canControl {
		requesterSubscriber = nil
	}
	if approved && requesterSubscriber != nil {
		s.controller = requester
		s.controllerSeen = time.Now()
	}
	if approved && requesterSubscriber != nil {
		s.mu.Unlock()
		s.broadcast(Event{Type: "controller", Controller: requester})
		return true
	}
	if requesterSubscriber != nil {
		select {
		case requesterSubscriber.events <- Event{Type: "control_denied"}:
		case <-requesterSubscriber.done:
		default:
		}
	}
	s.mu.Unlock()
	return false
}
func (s *Session) ReleaseControl(clientID string) bool {
	s.mu.Lock()
	if s.controller != clientID {
		s.mu.Unlock()
		return false
	}
	s.controller = ""
	grant := s.pendingControl
	if _, exists := s.subs[grant]; !exists {
		grant = ""
	}
	s.pendingControl = ""
	if grant != "" {
		s.controller = grant
		s.controllerSeen = time.Now()
	}
	s.mu.Unlock()
	s.broadcast(Event{Type: "controller", Controller: grant})
	return true
}
func (s *Session) TouchControl(clientID string) {
	s.mu.Lock()
	if s.controller == clientID {
		s.controllerSeen = time.Now()
	}
	s.mu.Unlock()
}
func (s *Session) IsController(clientID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.controller == clientID {
		s.controllerSeen = time.Now()
		return true
	}
	return false
}
func (s *Session) Write(clientID string, data []byte) error {
	if !s.IsController(clientID) {
		return ErrNotController
	}
	if err := s.leaveTmuxHistoryMode(); err != nil {
		return err
	}
	s.mu.RLock()
	w := s.stdin
	closed := s.closed
	s.mu.RUnlock()
	if closed || w == nil {
		return errors.New("terminal not attached")
	}
	_, err := w.Write(data)
	return err
}

func (s *Session) ScrollHistory(clientID string, lines int) error {
	if !s.IsController(clientID) {
		return ErrNotController
	}
	if lines < -100 || lines > 100 {
		return errors.New("invalid history scroll distance")
	}
	s.mu.RLock()
	mode := s.meta.SessionMode
	s.mu.RUnlock()
	if mode == "normal" {
		return nil
	}
	lock := s.manager.tmuxLock(s.Meta())
	lock.Lock()
	defer lock.Unlock()
	s.mu.RLock()
	client, meta, closed, inHistory := s.sshClient, s.meta, s.closed, s.tmuxHistoryMode
	s.mu.RUnlock()
	if closed || client == nil {
		return errors.New("terminal not attached")
	}
	if lines == 0 {
		if !inHistory {
			return nil
		}
		return s.leaveTmuxHistoryModeLocked(client, meta)
	}
	if lines < 0 {
		if !inHistory {
			if out, err := s.runCommand(client, fmt.Sprintf("tmux -L %s copy-mode -t %s", meta.TmuxSocket, meta.TmuxName)); err != nil {
				return fmt.Errorf("enter tmux history: %s", cleanOutput(out, err))
			}
			s.mu.Lock()
			s.tmuxHistoryMode = true
			s.mu.Unlock()
		}
		lines = -lines
		if out, err := s.runCommand(client, fmt.Sprintf("tmux -L %s send-keys -X -N %d -t %s scroll-up", meta.TmuxSocket, lines, meta.TmuxName)); err != nil {
			return fmt.Errorf("scroll tmux history: %s", cleanOutput(out, err))
		}
		s.mu.Lock()
		s.tmuxHistoryKnown = false
		s.mu.Unlock()
		return nil
	}
	if !inHistory {
		return nil
	}
	if out, err := s.runCommand(client, fmt.Sprintf("tmux -L %s send-keys -X -N %d -t %s scroll-down", meta.TmuxSocket, lines, meta.TmuxName)); err != nil {
		if strings.Contains(strings.ToLower(cleanOutput(out, err)), "not in a mode") {
			s.mu.Lock()
			s.tmuxHistoryMode = false
			s.tmuxHistoryPos = 0
			s.tmuxHistoryKnown = false
			s.mu.Unlock()
			return nil
		}
		return fmt.Errorf("scroll tmux history: %s", cleanOutput(out, err))
	}
	s.mu.Lock()
	s.tmuxHistoryKnown = false
	s.mu.Unlock()
	return nil
}

func (s *Session) HistorySize() int {
	s.mu.RLock()
	client, meta, closed := s.sshClient, s.meta, s.closed
	cached, updated := s.historySize, s.historySizeUpdated
	s.mu.RUnlock()
	if closed || client == nil || meta.SessionMode == "normal" {
		return 0
	}
	if !updated.IsZero() && time.Since(updated) < time.Second {
		return cached
	}
	lock := s.manager.tmuxLock(meta)
	lock.Lock()
	defer lock.Unlock()
	s.mu.RLock()
	cached, updated = s.historySize, s.historySizeUpdated
	s.mu.RUnlock()
	if !updated.IsZero() && time.Since(updated) < time.Second {
		return cached
	}
	out, err := s.runCommand(client, fmt.Sprintf("tmux -L %s display-message -p -t %s '#{history_size}'", meta.TmuxSocket, meta.TmuxName))
	if err != nil {
		return 0
	}
	size, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil || size < 0 {
		return 0
	}
	s.mu.Lock()
	s.historySize = size
	s.historySizeUpdated = time.Now()
	s.mu.Unlock()
	return size
}

func (s *Session) CachedHistorySize() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed || s.meta.SessionMode == "normal" {
		return 0
	}
	return s.historySize
}

func (s *Session) SendHistoryState(clientID string) {
	size := s.HistorySize()
	s.mu.RLock()
	subscriber := s.subs[clientID]
	s.mu.RUnlock()
	if subscriber == nil {
		return
	}
	select {
	case subscriber.events <- Event{Type: "history_state", HistorySize: size, Position: size}:
	case <-subscriber.done:
	default:
	}
}

func (s *Session) ScrollHistoryTo(clientID string, position, end int) error {
	if !s.IsController(clientID) {
		return ErrNotController
	}
	if position < 0 || end < 0 || position > tmuxHistoryLimit || end > tmuxHistoryLimit {
		return errors.New("invalid history position")
	}
	if position >= end {
		return s.leaveTmuxHistoryMode()
	}
	lock := s.manager.tmuxLock(s.Meta())
	lock.Lock()
	defer lock.Unlock()
	s.mu.RLock()
	client, meta, closed, inHistory := s.sshClient, s.meta, s.closed, s.tmuxHistoryMode
	currentPosition, positionKnown := s.tmuxHistoryPos, s.tmuxHistoryKnown
	s.mu.RUnlock()
	if closed || client == nil {
		return errors.New("terminal not attached")
	}
	if meta.SessionMode == "normal" {
		return nil
	}
	if !inHistory {
		return s.positionTmuxHistoryLocked(client, meta, position)
	}
	if !positionKnown {
		command := fmt.Sprintf("tmux -L %s send-keys -X -t %s history-top", meta.TmuxSocket, meta.TmuxName)
		if position > 0 {
			command += fmt.Sprintf(" && tmux -L %s send-keys -X -N %d -t %s scroll-down", meta.TmuxSocket, position, meta.TmuxName)
		}
		if out, err := s.runCommand(client, command); err != nil {
			if isTmuxModeMissing(cleanOutput(out, err)) {
				return s.positionTmuxHistoryLocked(client, meta, position)
			}
			return fmt.Errorf("position tmux history: %s", cleanOutput(out, err))
		}
		s.mu.Lock()
		s.tmuxHistoryPos = position
		s.tmuxHistoryKnown = true
		s.mu.Unlock()
		return nil
	}
	delta := position - currentPosition
	if delta == 0 {
		s.mu.Lock()
		s.tmuxHistoryPos = position
		s.tmuxHistoryKnown = true
		s.mu.Unlock()
		return nil
	}
	direction := "scroll-down"
	if delta < 0 {
		direction = "scroll-up"
		delta = -delta
	}
	if out, err := s.runCommand(client, fmt.Sprintf("tmux -L %s send-keys -X -N %d -t %s %s", meta.TmuxSocket, delta, meta.TmuxName, direction)); err != nil {
		if isTmuxModeMissing(cleanOutput(out, err)) {
			return s.positionTmuxHistoryLocked(client, meta, position)
		}
		return fmt.Errorf("position tmux history: %s", cleanOutput(out, err))
	}
	s.mu.Lock()
	s.tmuxHistoryPos = position
	s.tmuxHistoryKnown = true
	s.mu.Unlock()
	return nil
}

func (s *Session) positionTmuxHistoryLocked(client *ssh.Client, meta store.TerminalSession, position int) error {
	commands := []string{
		fmt.Sprintf("tmux -L %s copy-mode -t %s", meta.TmuxSocket, meta.TmuxName),
		fmt.Sprintf("tmux -L %s send-keys -X -t %s history-top", meta.TmuxSocket, meta.TmuxName),
	}
	if position > 0 {
		commands = append(commands, fmt.Sprintf("tmux -L %s send-keys -X -N %d -t %s scroll-down", meta.TmuxSocket, position, meta.TmuxName))
	}
	if out, err := s.runCommand(client, strings.Join(commands, " && ")); err != nil {
		s.mu.Lock()
		s.tmuxHistoryMode = false
		s.tmuxHistoryKnown = false
		s.mu.Unlock()
		return fmt.Errorf("enter tmux history: %s", cleanOutput(out, err))
	}
	s.mu.Lock()
	s.tmuxHistoryMode = true
	s.tmuxHistoryPos = position
	s.tmuxHistoryKnown = true
	s.mu.Unlock()
	return nil
}

func isTmuxModeMissing(message string) bool {
	return strings.Contains(strings.ToLower(message), "not in a mode")
}

func (s *Session) QueueHistoryPosition(clientID string, position, end int, sequence uint64) {
	s.historyScrollMu.Lock()
	s.historyScrollClient = clientID
	s.historyScrollPosition = position
	s.historyScrollEnd = end
	s.historyScrollSequence = sequence
	s.historyScrollPending = true
	if s.historyScrollRunning {
		s.historyScrollMu.Unlock()
		return
	}
	s.historyScrollRunning = true
	s.historyScrollMu.Unlock()
	go s.runHistoryPositionQueue()
}

func (s *Session) runHistoryPositionQueue() {
	for {
		s.historyScrollMu.Lock()
		if !s.historyScrollPending {
			s.historyScrollRunning = false
			s.historyScrollMu.Unlock()
			return
		}
		clientID := s.historyScrollClient
		position := s.historyScrollPosition
		end := s.historyScrollEnd
		sequence := s.historyScrollSequence
		s.historyScrollPending = false
		s.historyScrollMu.Unlock()
		started := time.Now()
		err := s.ScrollHistoryTo(clientID, position, end)
		actual := position
		if err != nil {
			s.mu.RLock()
			if s.tmuxHistoryKnown {
				actual = s.tmuxHistoryPos
			} else if !s.tmuxHistoryMode {
				actual = end
			}
			s.mu.RUnlock()
		}
		s.sendHistoryPosition(clientID, Event{
			Type:        "history_position",
			HistorySize: end,
			Position:    actual,
			Sequence:    sequence,
			DurationMS:  time.Since(started).Milliseconds(),
			Error:       cleanOutput(nil, err),
		})
	}
}

func (s *Session) sendHistoryPosition(clientID string, event Event) {
	s.mu.RLock()
	subscriber := s.subs[clientID]
	s.mu.RUnlock()
	if subscriber == nil {
		return
	}
	select {
	case subscriber.events <- event:
	case <-subscriber.done:
	default:
	}
}

func (s *Session) leaveTmuxHistoryMode() error {
	s.mu.RLock()
	inHistory, client, meta := s.tmuxHistoryMode, s.sshClient, s.meta
	s.mu.RUnlock()
	if !inHistory || client == nil || meta.SessionMode == "normal" {
		return nil
	}
	lock := s.manager.tmuxLock(meta)
	lock.Lock()
	defer lock.Unlock()
	return s.leaveTmuxHistoryModeLocked(client, meta)
}

func (s *Session) leaveTmuxHistoryModeLocked(client *ssh.Client, meta store.TerminalSession) error {
	out, err := s.runCommand(client, fmt.Sprintf("tmux -L %s send-keys -X -t %s cancel", meta.TmuxSocket, meta.TmuxName))
	if err != nil && !strings.Contains(strings.ToLower(cleanOutput(out, err)), "not in a mode") {
		return fmt.Errorf("leave tmux history: %s", cleanOutput(out, err))
	}
	s.mu.Lock()
	s.tmuxHistoryMode = false
	s.tmuxHistoryPos = 0
	s.tmuxHistoryKnown = false
	s.mu.Unlock()
	return nil
}

func validTerminalColor(value string) bool {
	if len(value) != 7 || value[0] != '#' {
		return false
	}
	for _, ch := range value[1:] {
		if !((ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F')) {
			return false
		}
	}
	return true
}

func (s *Session) SetTerminalColors(clientID, foreground, background string) error {
	if !s.IsController(clientID) {
		return ErrNotController
	}
	if !validTerminalColor(foreground) || !validTerminalColor(background) {
		return errors.New("invalid terminal colors")
	}
	s.mu.RLock()
	client, meta, closed := s.sshClient, s.meta, s.closed
	s.mu.RUnlock()
	if closed || client == nil {
		return errors.New("terminal not attached")
	}
	if meta.SessionMode == "normal" {
		return nil
	}
	style := shellQuote("fg=" + foreground + ",bg=" + background)
	cmd := fmt.Sprintf(
		"tmux -L %s set-option -t %s window-style %s \\; set-option -t %s window-active-style %s",
		meta.TmuxSocket, meta.TmuxName, style, meta.TmuxName, style,
	)
	lock := s.manager.tmuxLock(meta)
	lock.Lock()
	out, err := s.runCommand(client, cmd)
	lock.Unlock()
	if err != nil {
		return fmt.Errorf("configure terminal colors: %s", cleanOutput(out, err))
	}
	return nil
}

func (s *Session) Resize(clientID string, rows, cols int) error {
	if !s.IsController(clientID) {
		return ErrNotController
	}
	if rows < 2 || cols < 2 || rows > 500 || cols > 1000 {
		return errors.New("invalid terminal size")
	}
	s.mu.RLock()
	ss := s.sshSession
	s.mu.RUnlock()
	if ss == nil {
		return errors.New("terminal not attached")
	}
	return ss.WindowChange(rows, cols)
}
func (s *Session) killRemote(ctx context.Context) error {
	s.mu.RLock()
	client := s.sshClient
	meta := s.meta
	s.mu.RUnlock()
	if client == nil {
		if meta.Status == "creating" || meta.Status == "reconnecting" {
			return nil
		}
		return errors.New("SSH connection unavailable")
	}
	if meta.SessionMode == "normal" {
		return nil
	}
	if err := validateOwnership(client, meta); err != nil {
		if isTmuxTargetMissing(err.Error()) || isTmuxMissing(err.Error()) {
			return nil
		}
		return err
	}
	lock := s.manager.tmuxLock(meta)
	lock.Lock()
	out, err := s.runCommand(client, fmt.Sprintf("tmux -L %s kill-session -t %s", meta.TmuxSocket, meta.TmuxName))
	lock.Unlock()
	if err != nil && !isTmuxTargetMissing(cleanOutput(out, err)) {
		return fmt.Errorf("terminate tmux: %s", cleanOutput(out, err))
	}
	return nil
}
func (s *Session) close(status, msg string) {
	s.closeWithDetails(status, msg, nil)
}

func (s *Session) closeWithDetails(status, msg string, hostKeyErr *HostKeyError) {
	s.mu.Lock()
	if s.closed {
		if status == "ended" && s.meta.Status != "ended" {
			s.meta.Status = "ended"
			s.meta.LastError = msg
			s.mu.Unlock()
			_ = s.manager.store.UpdateTerminalStatus(s.meta.UserID, s.meta.ID, status, msg)
			s.manager.mu.Lock()
			delete(s.manager.sessions, s.meta.ID)
			s.manager.mu.Unlock()
			return
		}
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.meta.Status = status
	s.meta.LastError = msg
	s.commandMu.Lock()
	runner := s.commandRunner
	s.commandRunner = nil
	s.commandMu.Unlock()
	if runner != nil {
		_ = runner.Close()
	}
	if s.stdin != nil {
		_ = s.stdin.Close()
	}
	if s.sshSession != nil {
		_ = s.sshSession.Close()
	}
	if s.sshClient != nil {
		_ = s.sshClient.Close()
	}
	if s.connectCancel != nil {
		s.connectCancel()
		s.connectCancel = nil
	}
	event := Event{Type: "status", Status: status, Message: msg}
	if hostKeyErr != nil {
		event.Fingerprint = hostKeyErr.Fingerprint
		event.HostName = hostKeyErr.HostName
		event.HostAddress = hostKeyErr.Address
	}
	for id, subscriber := range s.subs {
		select {
		case subscriber.events <- event:
		case <-subscriber.done:
		default:
		}
		subscriber.close()
		delete(s.subs, id)
	}
	s.mu.Unlock()
	s.stopRecording(status)
	_ = s.manager.store.UpdateTerminalStatus(s.meta.UserID, s.meta.ID, status, msg)
	s.manager.mu.Lock()
	delete(s.manager.sessions, s.meta.ID)
	s.manager.mu.Unlock()
}

type ringBuffer struct {
	max       int
	segments  [][]byte
	size      int
	truncated bool
	end       uint64
}

func newRingBuffer(max int) *ringBuffer { return &ringBuffer{max: max} }
func (r *ringBuffer) Write(p []byte) uint64 {
	const chunkSize = 64 << 10
	r.end += uint64(len(p))
	for len(p) > 0 {
		take := len(p)
		if take > chunkSize {
			take = chunkSize
		}
		r.segments = append(r.segments, bytes.Clone(p[:take]))
		r.size += take
		p = p[take:]
	}
	overflow := r.size - r.max
	if overflow > 0 {
		r.truncated = true
		for overflow > 0 && len(r.segments) > 0 {
			first := r.segments[0]
			if len(first) <= overflow {
				overflow -= len(first)
				r.size -= len(first)
				r.segments[0] = nil
				r.segments = r.segments[1:]
				continue
			}
			r.segments[0] = first[overflow:]
			r.size -= overflow
			overflow = 0
		}
	}
	return r.end
}
func (r *ringBuffer) Bytes() ([]byte, bool) {
	return joinSegments(r.segments, r.size), r.truncated
}
func (r *ringBuffer) Segments() ([][]byte, bool) {
	return append([][]byte(nil), r.segments...), r.truncated
}
func (r *ringBuffer) End() uint64 { return r.end }
func (r *ringBuffer) BytesFrom(offset uint64) ([]byte, uint64, bool) {
	segments, end, truncated := r.SegmentsFrom(offset)
	size := 0
	for _, segment := range segments {
		size += len(segment)
	}
	return joinSegments(segments, size), end, truncated
}
func (r *ringBuffer) SegmentsFrom(offset uint64) ([][]byte, uint64, bool) {
	start := r.end - uint64(r.size)
	if offset < start || offset > r.end {
		segments, _ := r.Segments()
		return segments, r.end, true
	}
	skip := int(offset - start)
	segments := make([][]byte, 0, len(r.segments))
	for _, segment := range r.segments {
		if skip >= len(segment) {
			skip -= len(segment)
			continue
		}
		segments = append(segments, segment[skip:])
		skip = 0
	}
	return segments, r.end, false
}
func (r *ringBuffer) TailLines(maxLines, maxBytes int) []byte {
	if maxLines < 1 || maxBytes < 1 || r.size == 0 {
		return nil
	}
	remaining := maxLines
	scanned := 0
	startSegment, startOffset := 0, 0
	found := false
	skipTrailingNewline := len(r.segments) > 0 && len(r.segments[len(r.segments)-1]) > 0 && r.segments[len(r.segments)-1][len(r.segments[len(r.segments)-1])-1] == '\n'
	for segmentIndex := len(r.segments) - 1; segmentIndex >= 0; segmentIndex-- {
		segment := r.segments[segmentIndex]
		for index := len(segment) - 1; index >= 0; index-- {
			scanned++
			if segment[index] != '\n' {
				if scanned >= maxBytes {
					startSegment, startOffset, found = segmentIndex, index, true
					break
				}
				continue
			}
			if skipTrailingNewline {
				skipTrailingNewline = false
				continue
			}
			remaining--
			if remaining == 0 {
				startSegment, startOffset, found = segmentIndex, index+1, true
				break
			}
			if scanned >= maxBytes {
				startSegment, startOffset, found = segmentIndex, index, true
				break
			}
		}
		if found {
			break
		}
	}
	segments := r.segments
	if found {
		segments = append([][]byte{r.segments[startSegment][startOffset:]}, r.segments[startSegment+1:]...)
	}
	size := 0
	for _, segment := range segments {
		size += len(segment)
	}
	return joinSegments(segments, size)
}

func joinSegments(segments [][]byte, size int) []byte {
	if size == 0 {
		return nil
	}
	out := make([]byte, 0, size)
	for _, segment := range segments {
		out = append(out, segment...)
	}
	return out
}
