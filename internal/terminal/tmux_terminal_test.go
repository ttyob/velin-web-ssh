package terminal

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ttyob/velin-web-ssh/internal/store"
)

func TestTmuxApplicationMouseReachesPaneAndStopsAtShell(t *testing.T) {
	for _, program := range []string{"tmux", "python3"} {
		if _, err := exec.LookPath(program); err != nil {
			t.Skip("requires " + program)
		}
	}
	dir := t.TempDir()
	socket := fmt.Sprintf("velin-mouse-test-%d", time.Now().UnixNano())
	logFile := filepath.Join(dir, "input")
	script := `import os,sys,tty
tty.setraw(0)
os.write(1,b'\x1b[?1049hready')
with open(sys.argv[1],'wb',buffering=0) as log:
    while True:
        data=os.read(0,4096)
        if data==b'q':
            os.write(1,b'\x1b[?1049l')
        else:
            log.write(data)
`
	scriptFile := filepath.Join(dir, "pane.py")
	if err := os.WriteFile(scriptFile, []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	tmux := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("tmux", append([]string{"-L", socket}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("tmux %v: %s: %v", args, out, err)
		}
		return strings.TrimSpace(string(out))
	}
	tmux("-f", "/dev/null", "new-session", "-d", "-s", "test", "python3 "+shellQuote(scriptFile)+" "+shellQuote(logFile))
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", socket, "kill-server").Run() })
	wait := func(check func() bool) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for !check() {
			if time.Now().After(deadline) {
				t.Fatal("pane did not reach the expected state")
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	wait(func() bool {
		return tmux("display-message", "-p", "-t", "test", "#{alternate_on}|#{mouse_any_flag}") == "1|0"
	})
	meta := store.TerminalSession{TmuxSocket: socket, TmuxName: "test"}
	var expected []byte
	for _, action := range []string{"scroll", "click"} {
		data, err := applicationMouseInput(action, -1, 5, 40)
		if err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command("sh", "-c", tmuxApplicationMouseCommand(meta, data)).CombinedOutput(); err != nil {
			t.Fatalf("send mouse: %s: %v", out, err)
		}
		expected = append(expected, data...)
		wait(func() bool { got, _ := os.ReadFile(logFile); return bytes.Equal(got, expected) })
	}
	tmux("send-keys", "-l", "-t", "test", "q")
	wait(func() bool { return tmux("display-message", "-p", "-t", "test", "#{alternate_on}") == "0" })
	data, _ := applicationMouseInput("click", 0, 5, 40)
	if out, err := exec.Command("sh", "-c", tmuxApplicationMouseCommand(meta, data)).CombinedOutput(); err != nil {
		t.Fatalf("guard click: %s: %v", out, err)
	}
	// Wait for a subsequent literal marker to reach the pane before checking
	// that the guarded mouse input was discarded after leaving the TUI.
	tmux("send-keys", "-l", "-t", "test", "marker")
	expected = append(expected, []byte("marker")...)
	wait(func() bool { got, _ := os.ReadFile(logFile); return bytes.Equal(got, expected) })
}

func TestApplicationMouseRejectsInvalidInputAndViewers(t *testing.T) {
	for _, test := range []struct {
		action             string
		lines, row, column int
	}{
		{"input", 0, 1, 1}, {"scroll", 0, 1, 1}, {"scroll", 4, 1, 1},
		{"scroll", -4, 1, 1}, {"click", 0, 0, 1}, {"click", 0, 1, 1001},
	} {
		if _, err := applicationMouseInput(test.action, test.lines, test.row, test.column); err == nil {
			t.Errorf("invalid mouse event accepted: %+v", test)
		}
	}
	session := &Session{controller: "owner"}
	if err := session.SendApplicationMouse("viewer", "click", 0, 1, 1); err != ErrNotController {
		t.Fatalf("viewer click = %v, want ErrNotController", err)
	}
}

func TestLegacyTmuxOverrideOptions(t *testing.T) {
	output := "terminal-overrides[0] " + legacyTmuxTerminalOverride + "\n" +
		"terminal-overrides[1] xterm*:Tc\n" +
		"terminal-overrides[7] " + legacyTmuxTerminalOverride + "\n" +
		"terminal-overrides[8] " + legacyTmuxTerminalOverride + ":colors=256\n" +
		"terminal-overrides[invalid] " + legacyTmuxTerminalOverride + "\n"
	if got, want := legacyTmuxOverrideOptions(output), []string{"terminal-overrides[0]", "terminal-overrides[7]"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("legacy options = %v, want %v", got, want)
	}
}

func TestParseTmuxHistoryState(t *testing.T) {
	for _, test := range []struct {
		output    string
		size      int
		alternate bool
	}{
		{"420 0\n", 420, false},
		{"0 1\n", 0, true},
		{"23 0\r\n", 23, false},
	} {
		size, alternate, err := parseTmuxHistoryState(test.output)
		if err != nil || size != test.size || alternate != test.alternate {
			t.Errorf("parseTmuxHistoryState(%q) = %d, %t, %v", test.output, size, alternate, err)
		}
	}
	for _, output := range []string{"", "0", "-1 0", "invalid 1", "0 invalid"} {
		if _, _, err := parseTmuxHistoryState(output); err == nil {
			t.Errorf("invalid history state accepted: %q", output)
		}
	}
}

func TestTmuxTerminalRestorationKeepsRedrawsOutOfHistory(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("requires tmux")
	}
	dir := t.TempDir()
	socket := fmt.Sprintf("velin-terminal-test-%d", time.Now().UnixNano())
	tmux := func(args ...string) []byte {
		t.Helper()
		out, err := exec.Command("tmux", append([]string{"-L", socket}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("tmux %v: %s: %v", args, out, err)
		}
		return out
	}
	start, done := filepath.Join(dir, "start"), filepath.Join(dir, "done")
	frameFile := filepath.Join(dir, "frames")
	var frames strings.Builder
	for line := 0; line < 80; line++ {
		fmt.Fprintf(&frames, "original-%03d\r\n", line)
	}
	frames.WriteString("\x1b[?1049h\x1b[2J\x1b[H")
	for frame := 0; frame < 3; frame++ {
		frames.WriteString("\x1b[H")
		for line := 0; line < 30; line++ {
			fmt.Fprintf(&frames, "temporary-frame-%d-row-%02d\r\n", frame, line)
		}
	}
	frames.WriteString("\x1b[?1049l")
	if err := os.WriteFile(frameFile, []byte(frames.String()), 0600); err != nil {
		t.Fatal(err)
	}
	command := "while [ ! -e " + shellQuote(start) + " ]; do sleep 0.01; done; cat " + shellQuote(frameFile) + "; touch " + shellQuote(done) + "; exec sleep 60"
	tmux("-f", "/dev/null", "new-session", "-d", "-s", "test", "-x", "80", "-y", "24", command)
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", socket, "kill-server").Run() })
	tmux("set-window-option", "-t", "test", "alternate-screen", "off")
	for _, override := range []string{legacyTmuxTerminalOverride, "xterm*:Tc", legacyTmuxTerminalOverride, "screen*:colors=256"} {
		tmux("set-option", "-as", "terminal-overrides", ","+override)
	}
	runCommand := func(command string) ([]byte, error) {
		return exec.Command("sh", "-c", command).CombinedOutput()
	}
	meta := store.TerminalSession{TmuxSocket: socket, TmuxName: "test"}
	// Restoration must be safe to repeat and preserve custom terminal options.
	for attempt := 0; attempt < 2; attempt++ {
		if err := configureTmuxTerminal(meta, runCommand); err != nil {
			t.Fatal(err)
		}
	}
	overrides := string(tmux("show-options", "-s", "-v", "terminal-overrides"))
	if strings.Contains(overrides, legacyTmuxTerminalOverride) || !strings.Contains(overrides, "xterm*:Tc") || !strings.Contains(overrides, "screen*:colors=256") {
		t.Fatalf("unexpected restored terminal overrides: %q", overrides)
	}
	if got := strings.TrimSpace(string(tmux("show-window-options", "-v", "-t", "test", "alternate-screen"))); got != "on" {
		t.Fatalf("alternate-screen = %q, want on", got)
	}
	if err := os.WriteFile(start, nil, 0600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(done); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("terminal fixture did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
	capture := string(tmux("capture-pane", "-p", "-t", "test", "-S", "-"))
	if strings.Contains(capture, "temporary-frame-") {
		t.Fatalf("TUI redraws leaked into history: %s", capture)
	}
	for line := 0; line < 80; line++ {
		if token := fmt.Sprintf("original-%03d", line); strings.Count(capture, token) != 1 {
			t.Fatalf("history must contain %s exactly once: %s", token, capture)
		}
	}
}
