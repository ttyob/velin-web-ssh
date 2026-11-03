package sessionbridge

import (
	"io"
	"strings"
	"unicode/utf8"

	vt "github.com/charmbracelet/x/vt"
)

const maxTextBytes = 96 << 10

// RenderText interprets cursor movement, erases, alternate screens and wide
// characters. Stripping escape codes alone would duplicate every TUI repaint.
func RenderText(raw []byte, cols, rows, maxLines int) string {
	text, _ := renderText(raw, cols, rows, maxLines)
	return text
}

func renderText(raw []byte, cols, rows, maxLines int) (string, bool) {
	cols = max(2, min(1000, cols))
	rows = max(2, min(500, rows))
	emulator := vt.NewEmulator(cols, rows)
	emulator.SetScrollbackSize(maxLines)
	// Replies are irrelevant for an observer. Close the private input pipe
	// before parsing, so device-status queries cannot block or reach SSH.
	_ = emulator.InputPipe().(io.Closer).Close()
	defer emulator.Close()
	alternate := false
	emulator.SetCallbacks(vt.Callbacks{AltScreen: func(on bool) { alternate = on }})
	_, _ = emulator.Write(raw)
	lines := make([]string, 0, maxLines+rows)
	if !alternate {
		for row := 0; row < emulator.ScrollbackLen(); row++ {
			var line strings.Builder
			for col := 0; col < cols; col++ {
				cell := emulator.ScrollbackCellAt(col, row)
				if cell == nil {
					line.WriteByte(' ')
				} else if cell.Width > 0 {
					if cell.Content == "" {
						line.WriteByte(' ')
					} else {
						line.WriteString(cell.Content)
					}
				}
			}
			lines = append(lines, strings.TrimRight(line.String(), " \t"))
		}
	}
	for row := 0; row < rows; row++ {
		var line strings.Builder
		for col := 0; col < cols; col++ {
			cell := emulator.CellAt(col, row)
			if cell == nil {
				line.WriteByte(' ')
			} else if cell.Width > 0 {
				if cell.Content == "" {
					line.WriteByte(' ')
				} else {
					line.WriteString(cell.Content)
				}
			}
		}
		lines = append(lines, strings.TrimRight(line.String(), " \t"))
	}
	full := strings.TrimRight(strings.Join(lines, "\n"), "\n")
	return tailText(full, maxLines), strings.Count(full, "\n")+1 > maxLines
}

func tailText(text string, maxLines int) string {
	text = strings.TrimRight(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	lines := strings.Split(text, "\n")
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	return strings.Join(lines, "\n")
}

func boundText(text string) (string, bool) {
	if len(text) <= maxTextBytes {
		return text, false
	}
	text = text[len(text)-maxTextBytes:]
	for len(text) > 0 && !utf8.RuneStart(text[0]) {
		text = text[1:]
	}
	return text, true
}
