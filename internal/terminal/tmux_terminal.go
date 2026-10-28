package terminal

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/ttyob/velin-web-ssh/internal/store"
)

const legacyTmuxTerminalOverride = "xterm-256color:smcup@:rmcup@:csr@"

// Some conversation UIs still accept SGR mouse input after disabling terminal
// mouse capture under tmux. Limit this bridge to complete clicks and a few
// wheel steps; never accept arbitrary escape sequences from the browser.
func applicationMouseInput(action string, lines, row, column int) ([]byte, error) {
	if row < 1 || row > 500 || column < 1 || column > 1000 {
		return nil, fmt.Errorf("invalid application mouse coordinates")
	}
	switch action {
	case "scroll":
		if lines == 0 || lines < -3 || lines > 3 {
			return nil, fmt.Errorf("invalid application scroll distance")
		}
		button := 65
		if lines < 0 {
			button, lines = 64, -lines
		}
		return []byte(strings.Repeat(fmt.Sprintf("\x1b[<%d;%d;%dM", button, column, row), lines)), nil
	case "click":
		return []byte(fmt.Sprintf("\x1b[<0;%d;%dM\x1b[<0;%d;%dm", column, row, column, row)), nil
	default:
		return nil, fmt.Errorf("invalid application mouse action")
	}
}

func tmuxApplicationMouseCommand(meta store.TerminalSession, data []byte) string {
	prefix := "tmux -L " + shellQuote(meta.TmuxSocket)
	var keys strings.Builder
	for _, b := range data {
		fmt.Fprintf(&keys, " %02x", b)
	}
	send := "send-keys -H -t " + shellQuote(meta.TmuxName) + keys.String()
	// Go straight to the pane: writing mouse reports to the attached client is
	// filtered by tmux when the application has not requested mouse capture.
	// Check the pane at execution time so queued clicks cannot reach its shell.
	return prefix + " if-shell -F -t " + shellQuote(meta.TmuxName) +
		" '#{&&:#{alternate_on},#{==:#{pane_in_mode},0}}' " + shellQuote(send)
}

func parseTmuxHistoryState(output string) (int, bool, error) {
	fields := strings.Fields(output)
	if len(fields) != 2 || (fields[1] != "0" && fields[1] != "1") {
		return 0, false, fmt.Errorf("invalid tmux history state: %q", output)
	}
	size, err := strconv.Atoi(fields[0])
	if err != nil || size < 0 {
		return 0, false, fmt.Errorf("invalid tmux history size: %q", fields[0])
	}
	return size, fields[1] == "1", nil
}

// Restore normal terminal semantics before attaching a client. In particular,
// disabling alternate-screen saves TUI redraws as history, while disabling csr
// makes copy-mode redraws pollute the browser's own scrollback. History is now
// browsed in tmux, so neither workaround is needed. Run this on restoration too:
// the private remote tmux server survives a Web service upgrade.
func configureTmuxTerminal(meta store.TerminalSession, runCommand func(string) ([]byte, error)) error {
	prefix := "tmux -L " + shellQuote(meta.TmuxSocket)
	out, err := runCommand(prefix + " show-options -s terminal-overrides")
	if err != nil {
		return fmt.Errorf("read tmux terminal configuration: %s", cleanOutput(out, err))
	}
	commands := []string{
		prefix + " set-window-option -t " + shellQuote(meta.TmuxName) + " alternate-screen on",
	}
	for _, option := range legacyTmuxOverrideOptions(string(out)) {
		commands = append(commands, prefix+" set-option -su "+shellQuote(option))
	}
	if out, err := runCommand(strings.Join(commands, " && ")); err != nil {
		return fmt.Errorf("configure tmux terminal: %s", cleanOutput(out, err))
	}
	return nil
}

func legacyTmuxOverrideOptions(output string) []string {
	var options []string
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || fields[1] != legacyTmuxTerminalOverride {
			continue
		}
		option := fields[0]
		if !strings.HasPrefix(option, "terminal-overrides[") || !strings.HasSuffix(option, "]") {
			continue
		}
		index := strings.TrimSuffix(strings.TrimPrefix(option, "terminal-overrides["), "]")
		if n, err := strconv.Atoi(index); err == nil && n >= 0 {
			options = append(options, option)
		}
	}
	return options
}
