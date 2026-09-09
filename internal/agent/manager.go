package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ttyob/velin-web-ssh/internal/terminal"
	"golang.org/x/crypto/ssh"
)

const probeCommand = `printf 'system\t'; uname -s 2>/dev/null || printf unknown; printf '\narch\t'; uname -m 2>/dev/null || printf unknown; printf '\nkernel\t'; uname -r 2>/dev/null || true; printf '\nhostname\t'; hostname 2>/dev/null || printf unknown; printf '\n'`

const snapshotCommand = `LC_ALL=C
printf 'hostname\t'; hostname 2>/dev/null || printf unknown; printf '\n'
printf 'system\t'; uname -s 2>/dev/null || printf unknown; printf '\n'
printf 'arch\t'; uname -m 2>/dev/null || printf unknown; printf '\n'
printf 'kernel\t'; uname -r 2>/dev/null || true; printf '\n'
if [ -r /proc/uptime ]; then awk '{printf "uptime\t%s\n", $1}' /proc/uptime; else printf 'uptime\t0\n'; fi
if [ -r /proc/loadavg ]; then awk '{printf "load\t%s\t%s\t%s\n", $1, $2, $3}' /proc/loadavg; else printf 'load\t0\t0\t0\n'; fi
if [ -r /proc/meminfo ]; then awk '/^MemTotal:/ {t=$2} /^MemAvailable:/ {a=$2} /^MemFree:/ {f=$2} /^Buffers:/ {b=$2} /^Cached:/ {c=$2} END {if (a==0) a=f+b+c; printf "memory\t%.0f\t%.0f\n", t, a}' /proc/meminfo; else printf 'memory\t0\t0\n'; fi
df -Pk / 2>/dev/null | awk 'NR==2 {printf "disk\t%s\t%s\t%s\n", $2, $3, $4}'`

type Status struct {
	HostID       string     `json:"hostID"`
	State        string     `json:"state"`
	Backend      string     `json:"backend"`
	Hostname     string     `json:"hostname,omitempty"`
	OS           string     `json:"os,omitempty"`
	Arch         string     `json:"arch,omitempty"`
	Kernel       string     `json:"kernel,omitempty"`
	ConnectedAt  *time.Time `json:"connectedAt,omitempty"`
	LastSeenAt   *time.Time `json:"lastSeenAt,omitempty"`
	LastError    string     `json:"lastError,omitempty"`
	AIConfigured bool       `json:"aiConfigured"`
	Model        string     `json:"model,omitempty"`
}

type SystemInfo struct {
	Hostname string `json:"hostname"`
	OS       string `json:"os"`
	Arch     string `json:"arch"`
	Kernel   string `json:"kernel"`
}

type Memory struct {
	TotalBytes     uint64  `json:"totalBytes"`
	AvailableBytes uint64  `json:"availableBytes"`
	UsedBytes      uint64  `json:"usedBytes"`
	UsedPercent    float64 `json:"usedPercent"`
}

type Disk struct {
	Path        string  `json:"path"`
	TotalBytes  uint64  `json:"totalBytes"`
	FreeBytes   uint64  `json:"freeBytes"`
	UsedBytes   uint64  `json:"usedBytes"`
	UsedPercent float64 `json:"usedPercent"`
}

type Snapshot struct {
	System        SystemInfo `json:"system"`
	UptimeSeconds float64    `json:"uptimeSeconds"`
	Load1         float64    `json:"load1"`
	Load5         float64    `json:"load5"`
	Load15        float64    `json:"load15"`
	Memory        Memory     `json:"memory"`
	Disks         []Disk     `json:"disks"`
	CollectedAt   time.Time  `json:"collectedAt"`
}

type Process struct {
	PID         int    `json:"pid"`
	User        string `json:"user"`
	State       string `json:"state"`
	MemoryBytes uint64 `json:"memoryBytes"`
	Command     string `json:"command"`
}

type Manager struct {
	terminals *terminal.Manager
	aiMu      sync.RWMutex
	ai        AIConfig
	mu        sync.RWMutex
	conns     map[string]*connection
	statuses  map[string]Status
}

type connection struct {
	manager   *Manager
	key       string
	userID    string
	hostID    string
	client    *ssh.Client
	os        string
	closeOnce sync.Once
	done      chan struct{}
}

func NewManager(terminals *terminal.Manager, ai AIConfig) *Manager {
	return &Manager{terminals: terminals, ai: ai, conns: make(map[string]*connection), statuses: make(map[string]Status)}
}

func (m *Manager) AIConfig() AIConfig {
	m.aiMu.RLock()
	defer m.aiMu.RUnlock()
	return m.ai
}

func (m *Manager) UpdateAI(value AIConfig) {
	m.aiMu.Lock()
	m.ai = value
	m.aiMu.Unlock()
}

func (m *Manager) Status(userID, hostID string) Status {
	key := connectionKey(userID, hostID)
	m.mu.RLock()
	status, ok := m.statuses[key]
	_, connected := m.conns[key]
	m.mu.RUnlock()
	if !ok {
		return m.withAI(Status{HostID: hostID, State: "disconnected", Backend: "velin"})
	}
	if !connected && status.State == "connected" {
		status.State = "disconnected"
	}
	return m.withAI(status)
}

func (m *Manager) Connect(ctx context.Context, userID, hostID string) (Status, error) {
	key := connectionKey(userID, hostID)
	m.mu.Lock()
	if m.conns[key] != nil {
		status := m.statuses[key]
		m.mu.Unlock()
		return status, nil
	}
	if m.statuses[key].State == "connecting" {
		m.mu.Unlock()
		return Status{}, errors.New("agent connection is already being established")
	}
	m.statuses[key] = Status{HostID: hostID, State: "connecting", Backend: "velin"}
	m.mu.Unlock()

	client, _, err := m.terminals.DialSaved(ctx, userID, hostID)
	if err != nil {
		return m.setError(userID, hostID, err), err
	}
	output, err := runSSHCommand(ctx, client, probeCommand)
	if err != nil {
		client.Close()
		return m.setError(userID, hostID, fmt.Errorf("probe SSH host: %w", err)), err
	}
	values := parseKeyValues(output)
	now := time.Now().UTC()
	status := Status{
		HostID: hostID, State: "connected", Backend: "velin",
		Hostname: values["hostname"], OS: normalizeOS(values["system"]), Arch: normalizeArch(values["arch"]), Kernel: values["kernel"],
		ConnectedAt: &now, LastSeenAt: &now,
	}
	status = m.withAI(status)
	conn := &connection{manager: m, key: key, userID: userID, hostID: hostID, client: client, os: status.OS, done: make(chan struct{})}
	m.mu.Lock()
	if m.statuses[key].State != "connecting" {
		m.mu.Unlock()
		client.Close()
		return Status{}, errors.New("agent connection was cancelled")
	}
	m.conns[key] = conn
	m.statuses[key] = status
	m.mu.Unlock()
	go conn.heartbeat()
	return status, nil
}

func (m *Manager) Snapshot(ctx context.Context, userID, hostID string) (Snapshot, error) {
	conn, err := m.connection(userID, hostID)
	if err != nil {
		return Snapshot{}, err
	}
	output, err := conn.run(ctx, snapshotCommand)
	if err != nil {
		return Snapshot{}, fmt.Errorf("collect system snapshot: %w", err)
	}
	value, err := parseSnapshot(output)
	if err != nil {
		return Snapshot{}, err
	}
	m.markSeen(userID, hostID)
	return value, nil
}

func (m *Manager) Processes(ctx context.Context, userID, hostID string) ([]Process, error) {
	conn, err := m.connection(userID, hostID)
	if err != nil {
		return nil, err
	}
	command := `LC_ALL=C
if ps -eo pid=,user=,stat=,rss=,args= --sort=-rss >/dev/null 2>&1; then
  ps -eo pid=,user=,stat=,rss=,args= --sort=-rss 2>/dev/null | head -n 2000
else
  raw=$(ps w 2>/dev/null || ps 2>/dev/null || true)
  printf '%s\n' "$raw" | awk 'NR > 1 {
    pid=$1; user=$2; state=""; start=3
    if ($3 ~ /^[0-9]+$/) { state=$4; start=5 }
    command=""
    for (i=start; i<=NF; i++) command = command (command ? " " : "") $i
    if (pid ~ /^[0-9]+$/ && command != "") printf "%s %s %s 0 %s\n", pid, user, state, command
  }' | head -n 2000
fi`
	if conn.os != "linux" {
		command = `LC_ALL=C ps -axo pid=,user=,state=,rss=,command= 2>/dev/null | head -n 2000`
	}
	output, err := conn.run(ctx, command)
	if err != nil {
		return nil, fmt.Errorf("collect process list: %w", err)
	}
	m.markSeen(userID, hostID)
	return parseProcesses(output), nil
}

func (m *Manager) Disconnect(userID, hostID string) Status {
	key := connectionKey(userID, hostID)
	m.mu.Lock()
	conn := m.conns[key]
	delete(m.conns, key)
	status := m.statuses[key]
	status.HostID = hostID
	status.State = "disconnected"
	status.Backend = "velin"
	status.LastError = ""
	m.statuses[key] = status
	m.mu.Unlock()
	if conn != nil {
		conn.close(nil)
	}
	return m.withAI(status)
}

func (m *Manager) Command(ctx context.Context, userID, hostID, command string) (string, error) {
	command, err := validateCommand(command)
	if err != nil {
		return "", errors.New("invalid SSH command")
	}
	conn, err := m.connection(userID, hostID)
	if err != nil {
		return "", err
	}
	output, err := conn.run(ctx, command)
	if len(output) > 128*1024 {
		output = output[:128*1024] + "\n[output truncated]"
	}
	if err == nil {
		m.markSeen(userID, hostID)
	}
	return output, err
}

func (m *Manager) CommandStream(ctx context.Context, userID, hostID, command string, onDelta func(string) error) (string, error) {
	command, err := validateCommand(command)
	if err != nil {
		return "", err
	}
	conn, err := m.connection(userID, hostID)
	if err != nil {
		return "", err
	}
	output, err := runSSHCommandStream(ctx, conn.client, command, onDelta)
	if err == nil {
		m.markSeen(userID, hostID)
	}
	return output, err
}

func validateCommand(command string) (string, error) {
	command = strings.TrimSpace(command)
	if command == "" || len(command) > 6000 || strings.ContainsRune(command, '\x00') {
		return "", errors.New("invalid SSH command")
	}
	return command, nil
}

// DockerLogin authenticates Docker on the selected SSH host without putting
// the registry password in the remote command line.
func (m *Manager) DockerLogin(ctx context.Context, userID, hostID, registry, username, password string) (string, error) {
	registry = strings.TrimSpace(registry)
	username = strings.TrimSpace(username)
	if username == "" || password == "" {
		return "", errors.New("Docker 登录需要用户名和密码或访问令牌")
	}
	if len(registry) > 512 || len(username) > 256 || len(password) > 8192 || strings.ContainsAny(registry+username, "\x00\r\n") || strings.ContainsRune(password, '\x00') {
		return "", errors.New("Docker 登录信息无效")
	}
	conn, err := m.connection(userID, hostID)
	if err != nil {
		return "", err
	}
	output, err := runDockerLogin(ctx, conn.client, registry, username, password)
	if err == nil {
		m.markSeen(userID, hostID)
	}
	return output, err
}

func (m *Manager) Close() {
	m.mu.Lock()
	connections := make([]*connection, 0, len(m.conns))
	for _, conn := range m.conns {
		connections = append(connections, conn)
	}
	m.conns = make(map[string]*connection)
	m.mu.Unlock()
	for _, conn := range connections {
		conn.close(nil)
	}
}

func (m *Manager) connection(userID, hostID string) (*connection, error) {
	m.mu.RLock()
	conn := m.conns[connectionKey(userID, hostID)]
	m.mu.RUnlock()
	if conn == nil {
		return nil, errors.New("agent is not connected")
	}
	return conn, nil
}

func (m *Manager) setError(userID, hostID string, err error) Status {
	status := m.Status(userID, hostID)
	status.HostID = hostID
	status.State = "error"
	status.Backend = "velin"
	status.LastError = err.Error()
	m.mu.Lock()
	m.statuses[connectionKey(userID, hostID)] = status
	m.mu.Unlock()
	return m.withAI(status)
}

func (m *Manager) withAI(status Status) Status {
	value := m.AIConfig()
	status.AIConfigured = value.Configured()
	status.Model = value.Model
	return status
}

func (m *Manager) markSeen(userID, hostID string) {
	key := connectionKey(userID, hostID)
	now := time.Now().UTC()
	m.mu.Lock()
	status := m.statuses[key]
	status.LastSeenAt = &now
	m.statuses[key] = status
	m.mu.Unlock()
}

func (c *connection) run(ctx context.Context, command string) (string, error) {
	return runSSHCommand(ctx, c.client, command)
}

func (c *connection) heartbeat() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			_, err := c.run(ctx, "true")
			cancel()
			if err != nil {
				c.close(fmt.Errorf("agent SSH heartbeat: %w", err))
				return
			}
			c.manager.markSeen(c.userID, c.hostID)
		case <-c.done:
			return
		}
	}
}

func (c *connection) close(err error) {
	c.closeOnce.Do(func() {
		close(c.done)
		_ = c.client.Close()
		c.manager.connectionClosed(c, err)
	})
}

func (m *Manager) connectionClosed(conn *connection, err error) {
	m.mu.Lock()
	if m.conns[conn.key] == conn {
		delete(m.conns, conn.key)
		status := m.statuses[conn.key]
		status.State = "disconnected"
		if err != nil {
			status.State = "error"
			status.LastError = err.Error()
		}
		m.statuses[conn.key] = status
	}
	m.mu.Unlock()
}

func runSSHCommand(ctx context.Context, client *ssh.Client, command string) (string, error) {
	session, err := client.NewSession()
	if err != nil {
		return "", err
	}
	defer session.Close()
	type result struct {
		output []byte
		err    error
	}
	done := make(chan result, 1)
	go func() {
		output, runErr := session.CombinedOutput(command)
		done <- result{output: output, err: runErr}
	}()
	select {
	case value := <-done:
		if value.err != nil {
			message := strings.TrimSpace(string(value.output))
			if message != "" {
				return string(value.output), fmt.Errorf("%w: %s", value.err, message)
			}
			return "", value.err
		}
		return string(value.output), nil
	case <-ctx.Done():
		_ = session.Close()
		return "", ctx.Err()
	}
}

type commandStreamWriter struct {
	mu        sync.Mutex
	output    bytes.Buffer
	onDelta   func(string) error
	err       error
	truncated bool
}

func (w *commandStreamWriter) Write(value []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err != nil {
		return 0, w.err
	}
	remaining := 128*1024 - w.output.Len()
	chunk := value
	if len(chunk) > remaining {
		chunk = chunk[:max(0, remaining)]
		w.truncated = true
	}
	if len(chunk) > 0 {
		_, _ = w.output.Write(chunk)
		if w.onDelta != nil {
			if w.err = w.onDelta(string(chunk)); w.err != nil {
				return 0, w.err
			}
		}
	}
	return len(value), nil
}

func (w *commandStreamWriter) result() (string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	output := w.output.String()
	if w.truncated {
		output += "\n[output truncated]"
	}
	return output, w.err
}

func runSSHCommandStream(ctx context.Context, client *ssh.Client, command string, onDelta func(string) error) (string, error) {
	session, err := client.NewSession()
	if err != nil {
		return "", err
	}
	defer session.Close()
	writer := &commandStreamWriter{onDelta: onDelta}
	session.Stdout = writer
	session.Stderr = writer
	if err = session.Start(command); err != nil {
		return "", err
	}
	done := make(chan error, 1)
	go func() { done <- session.Wait() }()
	select {
	case waitErr := <-done:
		output, writeErr := writer.result()
		if writeErr != nil {
			return output, writeErr
		}
		return output, waitErr
	case <-ctx.Done():
		_ = session.Close()
		output, _ := writer.result()
		return output, ctx.Err()
	}
}

func runDockerLogin(ctx context.Context, client *ssh.Client, registry, username, password string) (string, error) {
	session, err := client.NewSession()
	if err != nil {
		return "", err
	}
	defer session.Close()
	stdin, err := session.StdinPipe()
	if err != nil {
		return "", err
	}
	var stdout, stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr
	if err := session.Start(dockerLoginCommand(registry, username)); err != nil {
		_ = stdin.Close()
		return "", err
	}
	done := make(chan error, 1)
	go func() { done <- session.Wait() }()
	if _, err := io.WriteString(stdin, password+"\n"); err != nil {
		_ = stdin.Close()
		_ = session.Close()
		return "", err
	}
	if err := stdin.Close(); err != nil {
		_ = session.Close()
		return "", err
	}
	select {
	case waitErr := <-done:
		output := strings.TrimSpace(strings.TrimSpace(stdout.String()) + "\n" + strings.TrimSpace(stderr.String()))
		if waitErr != nil {
			if output != "" {
				return output, fmt.Errorf("docker login 失败: %s", output)
			}
			return "", fmt.Errorf("docker login 失败: %w", waitErr)
		}
		return output, nil
	case <-ctx.Done():
		_ = session.Close()
		return "", ctx.Err()
	}
}

func dockerLoginCommand(registry, username string) string {
	if registry == "" {
		return "docker login --username " + shellQuote(username) + " --password-stdin"
	}
	return "docker login " + shellQuote(registry) + " --username " + shellQuote(username) + " --password-stdin"
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func parseKeyValues(output string) map[string]string {
	values := make(map[string]string)
	for _, line := range strings.Split(output, "\n") {
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) == 2 {
			values[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
		}
	}
	return values
}

func parseSnapshot(output string) (Snapshot, error) {
	values := make(map[string][]string)
	for _, line := range strings.Split(output, "\n") {
		parts := strings.Split(line, "\t")
		if len(parts) >= 2 {
			values[parts[0]] = parts[1:]
		}
	}
	parseFloat := func(key string, index int) float64 {
		fields := values[key]
		if index >= len(fields) {
			return 0
		}
		value, _ := strconv.ParseFloat(strings.TrimSpace(fields[index]), 64)
		return value
	}
	parseKB := func(key string, index int) uint64 {
		value := parseFloat(key, index)
		if value <= 0 {
			return 0
		}
		return uint64(value * 1024)
	}
	totalMemory := parseKB("memory", 0)
	availableMemory := parseKB("memory", 1)
	if availableMemory > totalMemory {
		availableMemory = totalMemory
	}
	usedMemory := totalMemory - availableMemory
	totalDisk := parseKB("disk", 0)
	usedDisk := parseKB("disk", 1)
	freeDisk := parseKB("disk", 2)
	value := Snapshot{
		System:        SystemInfo{Hostname: first(values["hostname"]), OS: normalizeOS(first(values["system"])), Arch: normalizeArch(first(values["arch"])), Kernel: first(values["kernel"])},
		UptimeSeconds: parseFloat("uptime", 0), Load1: parseFloat("load", 0), Load5: parseFloat("load", 1), Load15: parseFloat("load", 2),
		Memory:      Memory{TotalBytes: totalMemory, AvailableBytes: availableMemory, UsedBytes: usedMemory, UsedPercent: percent(usedMemory, totalMemory)},
		Disks:       []Disk{{Path: "/", TotalBytes: totalDisk, FreeBytes: freeDisk, UsedBytes: usedDisk, UsedPercent: percent(usedDisk, totalDisk)}},
		CollectedAt: time.Now().UTC(),
	}
	if value.System.Hostname == "" {
		return Snapshot{}, errors.New("remote host returned an invalid system snapshot")
	}
	return value, nil
}

func parseProcesses(output string) []Process {
	processes := make([]Process, 0)
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		memoryKB, _ := strconv.ParseUint(fields[3], 10, 64)
		processes = append(processes, Process{PID: pid, User: fields[1], State: fields[2], MemoryBytes: memoryKB * 1024, Command: strings.Join(fields[4:], " ")})
	}
	return processes
}

func first(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return strings.TrimSpace(values[0])
}

func normalizeOS(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "darwin":
		return "macos"
	case "freebsd", "openbsd", "netbsd":
		return "bsd"
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

func normalizeArch(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "x86_64":
		return "amd64"
	case "aarch64":
		return "arm64"
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

func percent(value, total uint64) float64 {
	if total == 0 {
		return 0
	}
	return float64(value) / float64(total) * 100
}

func connectionKey(userID, hostID string) string { return userID + "\x00" + hostID }
