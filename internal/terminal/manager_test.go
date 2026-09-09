package terminal

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ttyob/velin-web-ssh/internal/store"
)

func TestRingBufferTruncatesOldOutput(t *testing.T) {
	r := newRingBuffer(8)
	r.Write([]byte("12345"))
	r.Write([]byte("67890"))
	got, truncated := r.Bytes()
	if !truncated {
		t.Fatal("expected truncated flag")
	}
	if !bytes.Equal(got, []byte("34567890")) {
		t.Fatalf("unexpected data %q", got)
	}
}

func TestRingBufferSnapshotsRemainStableAfterEviction(t *testing.T) {
	r := newRingBuffer(8)
	r.Write([]byte("12345678"))
	segments, _ := r.Segments()
	r.Write([]byte("90"))
	if got := string(bytes.Join(segments, nil)); got != "12345678" {
		t.Fatalf("snapshot changed after eviction: %q", got)
	}
	if got, _ := r.Bytes(); string(got) != "34567890" {
		t.Fatalf("unexpected retained bytes: %q", got)
	}
}

func TestRingBufferTailLinesAcrossSegments(t *testing.T) {
	r := newRingBuffer(1 << 20)
	r.Write([]byte("one\r\ntwo\r\n"))
	r.Write([]byte("three\r\nfour\r\n"))
	if got := string(r.TailLines(2, 1<<20)); got != "three\r\nfour\r\n" {
		t.Fatalf("unexpected tail: %q", got)
	}
}

func TestRingBufferTailLinesLimitsBytes(t *testing.T) {
	r := newRingBuffer(1 << 20)
	r.Write(bytes.Repeat([]byte("x"), 1024))
	if got := r.TailLines(200, 128); len(got) != 128 {
		t.Fatalf("tail length=%d, want 128", len(got))
	}
}

func TestBroadcastDoesNotDropOutputWhenSubscriberIsBusy(t *testing.T) {
	s := &Session{subs: make(map[string]*terminalSubscriber), buffer: newRingBuffer(1 << 20)}
	events, _, _ := s.Subscribe("client", "", 0)
	const count = 300
	done := make(chan struct{})
	go func() {
		for index := 0; index < count; index++ {
			s.broadcast(Event{Type: "output", Data: fmt.Sprintf("output-%03d", index)})
		}
		close(done)
	}()
	for index := 0; index < count; index++ {
		event := <-events
		expected := fmt.Sprintf("output-%03d", index)
		if event.Data != expected {
			t.Fatalf("event %d=%q, want %q", index, event.Data, expected)
		}
	}
	<-done
}

func TestSubscribeResumesFromOutputOffset(t *testing.T) {
	s := &Session{streamID: "stream", subs: make(map[string]*terminalSubscriber), buffer: newRingBuffer(8)}
	s.buffer.Write([]byte("one"))
	_, _, initial := s.Subscribe("first", "", 0)
	if string(bytes.Join(initial.Segments, nil)) != "one" || initial.Offset != 3 || initial.Truncated {
		t.Fatalf("unexpected initial replay: %+v", initial)
	}
	s.Unsubscribe("first")
	s.buffer.Write([]byte("two"))
	_, _, resumed := s.Subscribe("second", "stream", initial.Offset)
	if string(bytes.Join(resumed.Segments, nil)) != "two" || resumed.Offset != 6 || resumed.Truncated {
		t.Fatalf("unexpected resumed replay: %+v", resumed)
	}
	s.Unsubscribe("second")
	s.buffer.Write([]byte("34567890"))
	_, _, truncated := s.Subscribe("third", "stream", initial.Offset)
	if string(bytes.Join(truncated.Segments, nil)) != "34567890" || !truncated.Truncated {
		t.Fatalf("unexpected truncated replay: %+v", truncated)
	}
}

func TestNormalizeTerminalLines(t *testing.T) {
	got := normalizeTerminalLines([]byte("one\ntwo\r\nthree"))
	if want := "one\r\ntwo\r\nthree"; string(got) != want {
		t.Fatalf("normalizeTerminalLines()=%q, want %q", got, want)
	}
}

func TestPrepareTerminalSnapshotResetsAndNormalizes(t *testing.T) {
	got := prepareTerminalSnapshot([]byte("\x1b[31mone\ntwo"))
	want := "\x1b[0m\x1b[2J\x1b[H\x1b[31mone\r\ntwo\x1b[0m"
	if string(got) != want {
		t.Fatalf("prepareTerminalSnapshot()=%q, want %q", got, want)
	}
}

func TestTailTerminalLines(t *testing.T) {
	for _, input := range []string{"one\r\ntwo\r\nthree\r\nfour", "one\r\ntwo\r\nthree\r\nfour\r\n"} {
		got := tailTerminalLines([]byte(input), 2)
		want := "three\r\nfour"
		if strings.HasSuffix(input, "\r\n") {
			want += "\r\n"
		}
		if string(got) != want {
			t.Fatalf("tailTerminalLines(%q)=%q, want %q", input, got, want)
		}
	}
}

func TestTmuxCaptureSupportedRejectsDevelopmentBuild(t *testing.T) {
	if tmuxCaptureSupported("tmux next-3.4") {
		t.Fatal("development tmux build must not use capture-pane")
	}
	if !tmuxCaptureSupported("tmux 3.4") {
		t.Fatal("stable tmux build should support capture-pane")
	}
	if tmuxCaptureSupported("") {
		t.Fatal("unknown tmux version must use the safe fallback")
	}
}

func TestSnapshotFallsBackToBufferedOutput(t *testing.T) {
	s := &Session{meta: store.TerminalSession{SessionMode: "tmux"}, buffer: newRingBuffer(1024)}
	s.buffer.Write([]byte("one\ntwo\nthree"))
	got := s.Snapshot(2)
	if !bytes.Contains(got, []byte("two\r\nthree")) {
		t.Fatalf("buffered snapshot missing tail: %q", got)
	}
}

func TestIsTmuxTargetMissing(t *testing.T) {
	for _, message := range []string{
		"tmux session not found: no server running on /tmp/tmux-0/velin",
		"can't find session: ws_missing",
		"tmux session not found: no such session: ws_missing",
		"tmux session not found: error connecting to /tmp/tmux-0/velin-webssh-04c27398055f (No such file or directory)",
	} {
		if !isTmuxTargetMissing(message) {
			t.Fatalf("isTmuxTargetMissing(%q)=false", message)
		}
	}
	for _, message := range []string{
		"tmux is required on the remote host: bash: line 1: tmux: command not found",
		"bash: tmux: not found",
		"command not found: tmux",
	} {
		if !isTmuxMissing(message) {
			t.Fatalf("isTmuxMissing(%q)=false", message)
		}
		if isTmuxTargetMissing(message) {
			t.Fatalf("isTmuxTargetMissing(%q)=true", message)
		}
	}
	for _, message := range []string{
		"tmux ownership marker mismatch",
		"permission denied",
		"connection timed out",
	} {
		if isTmuxTargetMissing(message) {
			t.Fatalf("isTmuxTargetMissing(%q)=true", message)
		}
	}
}

func TestControlTransferRequiresApproval(t *testing.T) {
	s := &Session{subs: make(map[string]*terminalSubscriber), buffer: newRingBuffer(8)}
	firstEvents, _, _ := s.Subscribe("first", "", 0)
	s.Subscribe("second", "", 0)
	if s.RequestControl("second") {
		t.Fatal("online controller was silently replaced")
	}
	select {
	case event := <-firstEvents:
		if event.Type != "control_request" || event.Requester != "second" {
			t.Fatalf("unexpected request event: %+v", event)
		}
	default:
		t.Fatal("controller did not receive transfer request")
	}
	if !s.RespondControl("first", "second", true) || !s.IsController("second") {
		t.Fatal("approved control transfer failed")
	}
}

func TestSubscribeReclaimsControlWithMatchingReconnectKey(t *testing.T) {
	s := &Session{subs: make(map[string]*terminalSubscriber), buffer: newRingBuffer(8)}
	_, firstDone, _ := s.Subscribe("first", "", 0, "same-tab")
	s.Subscribe("second", "", 0, "same-tab")
	if !s.IsController("second") {
		t.Fatal("matching reconnect key did not reclaim control")
	}
	select {
	case <-firstDone:
	default:
		t.Fatal("replaced subscriber was not closed")
	}
}

func TestSubscribeDoesNotReclaimControlWithDifferentReconnectKey(t *testing.T) {
	s := &Session{subs: make(map[string]*terminalSubscriber), buffer: newRingBuffer(8)}
	_, firstDone, _ := s.Subscribe("first", "", 0, "first-tab")
	s.Subscribe("second", "", 0, "second-tab")
	if !s.IsController("first") || s.IsController("second") {
		t.Fatal("different reconnect key replaced the active controller")
	}
	select {
	case <-firstDone:
		t.Fatal("different reconnect key closed the active subscriber")
	default:
	}
}

func TestControlReleaseGrantsWaitingClient(t *testing.T) {
	s := &Session{subs: make(map[string]*terminalSubscriber), buffer: newRingBuffer(8)}
	s.Subscribe("first", "", 0)
	s.Subscribe("second", "", 0)
	s.RequestControl("second")
	if !s.ReleaseControl("first") || !s.IsController("second") {
		t.Fatal("releasing control did not grant waiting client")
	}
}

func TestReadOnlySubscriberCannotTakeControl(t *testing.T) {
	s := &Session{subs: make(map[string]*terminalSubscriber), buffer: newRingBuffer(8)}
	s.SubscribeReadOnly("viewer", "", 0)
	if s.RequestControl("viewer") || s.IsController("viewer") {
		t.Fatal("read-only subscriber obtained terminal control")
	}
	s.Subscribe("operator", "", 0)
	if !s.IsController("operator") {
		t.Fatal("controllable subscriber did not obtain available control")
	}
}

func TestShareRecordingCapturesServerOutput(t *testing.T) {
	database, err := store.Open(filepath.Join(t.TempDir(), "recording.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err = database.CreateUser("user-1", "user", "hash", "user"); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(database, nil, "test", t.TempDir())
	session := &Session{
		meta:    store.TerminalSession{ID: "session-1", UserID: "user-1", Name: "shell", Status: "attached"},
		manager: manager, subs: make(map[string]*terminalSubscriber), buffer: newRingBuffer(1024),
	}
	manager.sessions[session.meta.ID] = session
	recording, err := manager.StartShareRecording("user-1", session.meta.ID)
	if err != nil || !strings.HasSuffix(recording.Path, ".cast") {
		t.Fatalf("recording=%+v err=%v", recording, err)
	}
	session.broadcastOutput([]byte("hello\r\n"))
	stopped := manager.StopRecordingIfID("user-1", session.meta.ID, recording.ID)
	data, readErr := os.ReadFile(recording.Path)
	if readErr != nil || stopped.Bytes == 0 || !bytes.Contains(data, []byte("hello")) || !bytes.Contains(data, []byte(`"version":2`)) {
		t.Fatalf("stopped=%+v data=%q err=%v", stopped, data, readErr)
	}
}

func TestValidTerminalColor(t *testing.T) {
	for _, value := range []string{"#111416", "#D8DED9", "#000000", "#ffffff"} {
		if !validTerminalColor(value) {
			t.Fatalf("expected valid terminal color %q", value)
		}
	}
	for _, value := range []string{"111416", "#fff", "#1234567", "#12gg45", "#12345; echo bad"} {
		if validTerminalColor(value) {
			t.Fatalf("expected invalid terminal color %q", value)
		}
	}
}

func TestNormalizeSessionMode(t *testing.T) {
	for name, test := range map[string]struct {
		preferred, fallback, expected string
	}{
		"default":       {"", "", "tmux"},
		"host normal":   {"", "normal", "normal"},
		"override tmux": {"tmux", "normal", "tmux"},
		"fallback once": {"normal", "tmux", "normal"},
	} {
		t.Run(name, func(t *testing.T) {
			if actual := normalizeSessionMode(test.preferred, test.fallback); actual != test.expected {
				t.Fatalf("mode=%q, want %q", actual, test.expected)
			}
		})
	}
}

func TestPlatformFromName(t *testing.T) {
	for input, expected := range map[string]string{
		"Linux\n":       "linux",
		"Darwin":        "macos",
		"MINGW64_NT":    "windows",
		"FreeBSD":       "bsd",
		"SunOS":         "unix",
		"  OpenBSD  \n": "bsd",
		"":              "",
	} {
		if actual := platformFromName(input); actual != expected {
			t.Errorf("platformFromName(%q)=%q, want %q", input, actual, expected)
		}
	}
}

func TestDistributionFromRelease(t *testing.T) {
	for name, test := range map[string]struct {
		id, like, expected string
	}{
		"ubuntu":       {"ubuntu", "debian", "ubuntu"},
		"rocky":        {"rocky", "rhel centos fedora", "rocky"},
		"oracle alias": {"ol", "fedora", "oracle"},
		"suse alias":   {"opensuse-leap", "suse", "opensuse"},
		"family":       {"custom", "rhel fedora", "rhel"},
		"unknown":      {"my-distro", "", "my-distro"},
		"empty":        {"", "", ""},
	} {
		t.Run(name, func(t *testing.T) {
			if actual := distributionFromRelease(test.id, test.like); actual != test.expected {
				t.Fatalf("distribution=%q, want %q", actual, test.expected)
			}
		})
	}
}
