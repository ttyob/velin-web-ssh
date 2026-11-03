package terminal

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

type bridgeWriter struct{ bytes.Buffer }

func (*bridgeWriter) Close() error { return nil }
func TestBridgeObservationAndSerializedInput(t *testing.T) {
	writer := &bridgeWriter{}
	s := &Session{buffer: newRingBuffer(1024), streamID: "stream", gridRows: 40, gridCols: 100, controller: "browser", stdin: writer}
	s.broadcastOutput([]byte("hello 中文\r\n\x1b[?2004h"))
	before := s.Geometry()
	snapshot, err := s.BridgeSnapshot(context.Background(), 10)
	if err != nil || snapshot.Offset == 0 || snapshot.StreamID != "stream" || snapshot.LastOutputAt.IsZero() || !strings.Contains(string(snapshot.Raw), "中文") {
		t.Fatal(snapshot, err)
	}
	if snapshot.Cols != 100 || snapshot.Rows != 40 || s.controller != "browser" || s.Geometry() != before {
		t.Fatal("observation changed terminal state")
	}
	cursor, err := s.BridgeSnapshot(context.Background(), 0)
	if err != nil || cursor.Offset != snapshot.Offset || cursor.Raw != nil || cursor.Capture != nil {
		t.Fatal(cursor, err)
	}
	var wg sync.WaitGroup
	for range 10 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, err := s.WriteExternal(context.Background(), []byte("A"))
			if err != nil {
				t.Error(err)
			}
		}()
		go func() {
			defer wg.Done()
			if err := s.Write("browser", []byte("B")); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if writer.Len() != 20 || s.controller != "browser" {
		t.Fatal("inputs lost or control changed", writer.Len())
	}
	if _, err = s.WriteExternal(context.Background(), []byte("\x1b[200~a\nb\x1b[201~")); err != nil {
		t.Fatal(err)
	}
	s.broadcastOutput([]byte("\x1b[?2004l"))
	beforeLength := writer.Len()
	if _, err = s.WriteExternal(context.Background(), []byte("\x1b[200~a\nb\x1b[201~")); err == nil || writer.Len() != beforeLength {
		t.Fatal("unsafe multiline input accepted")
	}
}

type blockingBridgeWriter struct {
	started, release chan struct{}
	once             sync.Once
}

func (w *blockingBridgeWriter) Write(data []byte) (int, error) {
	w.once.Do(func() { close(w.started) })
	<-w.release
	return len(data), nil
}
func (*blockingBridgeWriter) Close() error { return nil }
func TestBridgeWriteCancellationDoesNotCloseSharedInput(t *testing.T) {
	writer := &blockingBridgeWriter{started: make(chan struct{}), release: make(chan struct{})}
	s := &Session{buffer: newRingBuffer(32), stdin: writer, controller: "browser"}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := s.WriteExternal(ctx, []byte("hello")); done <- err }()
	<-writer.started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("write did not return on cancellation")
	}
	close(writer.release)
	s.inputMu.Lock()
	s.inputMu.Unlock()
	if s.closed || s.controller != "browser" {
		t.Fatal("cancelled write disconnected terminal")
	}
}
func TestBridgeTailWriterAndPasteDetection(t *testing.T) {
	writer := &bridgeTailWriter{limit: 8}
	_, _ = io.WriteString(writer, "abcde")
	_, _ = io.WriteString(writer, "fghijk")
	if string(writer.data) != "defghijk" || !writer.truncated {
		t.Fatal(string(writer.data))
	}
	_, _ = io.WriteString(writer, "1234567890")
	if string(writer.data) != "34567890" {
		t.Fatal(string(writer.data))
	}
	for _, tc := range []struct {
		raw  string
		want bool
	}{{"", false}, {"\x1b[?2004h", true}, {"\x1b[?1;2004h", true}, {"\x1b[?2004h\x1b[?2004l", false}} {
		if bridgePasteEnabled([]byte(tc.raw)) != tc.want {
			t.Fatal(tc)
		}
	}
}
