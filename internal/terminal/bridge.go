package terminal

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// BridgeSnapshot observes the output without subscribing, resizing or acquiring
// control. A zero line count reads only the cursor, so waits do not run SSH jobs.
type BridgeSnapshot struct {
	StreamID     string
	Offset       uint64
	Raw          []byte
	Capture      *string
	Truncated    bool
	Rows, Cols   int
	LastOutputAt time.Time
	Source       string
}

func (s *Session) BridgeSnapshot(ctx context.Context, maxLines int) (BridgeSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return BridgeSnapshot{}, err
	}
	s.mu.RLock()
	geometry := s.geometryLocked()
	result := BridgeSnapshot{StreamID: s.streamID, Offset: s.buffer.End(), Rows: geometry.Rows, Cols: geometry.Cols, LastOutputAt: s.lastOutputAt, Source: "terminal-replay"}
	client, meta, closed, safe := s.sshClient, s.meta, s.closed, s.tmuxCaptureSafe
	s.mu.RUnlock()
	if maxLines == 0 {
		return result, nil
	}
	if maxLines < 1 || maxLines > 1000 {
		return result, errors.New("invalid snapshot line count")
	}
	if !closed && client != nil && meta.SessionMode != "normal" && safe {
		captureCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		command := fmt.Sprintf("tmux -L %s capture-pane -p -J -t %s -S -%d", shellQuote(meta.TmuxSocket), shellQuote(meta.TmuxName), maxLines)
		out, truncated, err := bridgeCommand(captureCtx, client, command)
		if err == nil {
			text := string(out)
			result.Capture, result.Source, result.Truncated = &text, "tmux-capture", truncated
			return result, nil
		}
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
	}
	s.mu.RLock()
	// The ring is bounded and its chunks are immutable. Limit parsing work for
	// agents without changing the browser's larger replay buffer.
	start := uint64(0)
	const maxReplayBytes = 2 << 20
	if s.buffer.End() > maxReplayBytes {
		start = s.buffer.End() - maxReplayBytes
	}
	result.Raw, result.Offset, result.Truncated = s.buffer.BytesFrom(start)
	result.LastOutputAt = s.lastOutputAt
	result.Truncated = result.Truncated || start > 0
	s.mu.RUnlock()
	return result, nil
}

// bridgeCommand owns only its command channel. Cancellation never disconnects
// the shared SSH connection or another terminal's command runner.
func bridgeCommand(ctx context.Context, client *ssh.Client, command string) ([]byte, bool, error) {
	type opened struct {
		session *ssh.Session
		err     error
	}
	ch := make(chan opened)
	go func() {
		session, err := client.NewSession()
		select {
		case ch <- opened{session, err}:
		case <-ctx.Done():
			if session != nil {
				_ = session.Close()
			}
		}
	}()
	var session *ssh.Session
	select {
	case <-ctx.Done():
		return nil, false, ctx.Err()
	case value := <-ch:
		if value.err != nil {
			return nil, false, value.err
		}
		session = value.session
	}
	defer session.Close()
	output := &bridgeTailWriter{limit: 512 << 10}
	session.Stdout = output
	session.Stderr = io.Discard
	done := make(chan error, 1)
	go func() { done <- session.Run(command) }()
	select {
	case <-ctx.Done():
		return nil, false, ctx.Err()
	case err := <-done:
		output.mu.Lock()
		defer output.mu.Unlock()
		return bytes.Clone(output.data), output.truncated, err
	}
}

type bridgeTailWriter struct {
	mu        sync.Mutex
	data      []byte
	limit     int
	truncated bool
}

func (w *bridgeTailWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := len(data)
	if len(data) >= w.limit {
		w.truncated = w.truncated || len(w.data)+n > w.limit
		w.data = append(w.data[:0], data[n-w.limit:]...)
		return n, nil
	}
	if overflow := len(w.data) + n - w.limit; overflow > 0 {
		copy(w.data, w.data[overflow:])
		w.data = w.data[:len(w.data)-overflow]
		w.truncated = true
	}
	w.data = append(w.data, data...)
	return n, nil
}

var bridgePasteMode = regexp.MustCompile("\\x1b\\[\\?([0-9;]+)([hl])")
var ErrExternalInputRejected = errors.New("external input rejected")

func bridgePasteEnabled(raw []byte) bool {
	enabled := false
	for _, match := range bridgePasteMode.FindAllSubmatch(raw, -1) {
		for _, mode := range strings.Split(string(match[1]), ";") {
			if mode == "2004" {
				enabled = string(match[2]) == "h"
			}
		}
	}
	return enabled
}

// WriteExternal serializes with web/mobile input but leaves controller identity
// intact. On cancellation a pending SSH write may still finish; callers must
// persist the uncertain result and never automatically resend it.
func (s *Session) WriteExternal(ctx context.Context, data []byte) (int, error) {
	if len(data) == 0 || len(data) > 64<<10 {
		return 0, fmt.Errorf("%w: invalid input size", ErrExternalInputRejected)
	}
	data = bytes.Clone(data)
	type written struct {
		n   int
		err error
	}
	done := make(chan written, 1)
	go func() {
		s.inputMu.Lock()
		defer s.inputMu.Unlock()
		if err := ctx.Err(); err != nil {
			done <- written{err: err}
			return
		}
		s.mu.RLock()
		writer, closed := s.stdin, s.closed
		multiline := bytes.HasPrefix(data, []byte("\x1b[200~"))
		paste := true
		if multiline {
			raw, _ := s.buffer.Bytes()
			paste = bridgePasteEnabled(raw)
		}
		s.mu.RUnlock()
		if closed || writer == nil {
			done <- written{err: fmt.Errorf("%w: 终端尚未连接", ErrExternalInputRejected)}
			return
		}
		if !paste {
			done <- written{err: fmt.Errorf("%w: 终端尚未启用 bracketed paste，无法安全粘贴多行；请先在终端打开支持粘贴的 shell 或 CLI", ErrExternalInputRejected)}
			return
		}
		if err := s.leaveTmuxHistoryMode(); err != nil {
			done <- written{err: err}
			return
		}
		if err := ctx.Err(); err != nil {
			done <- written{err: err}
			return
		}
		n, err := writer.Write(data)
		if err == nil && n != len(data) {
			err = io.ErrShortWrite
		}
		done <- written{n, err}
	}()
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	case result := <-done:
		return result.n, result.err
	}
}
