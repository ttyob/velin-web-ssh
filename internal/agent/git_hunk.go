package agent

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"errors"
	"fmt"
	"path"
	"strings"
	"unicode/utf8"
)

type GitIndexEdit struct {
	Repository string `json:"repository"`
	Path       string `json:"path"`
	Expected   string `json:"expected"`
	Exists     bool   `json:"exists"`
	Content    string `json:"content"`
	Remove     bool   `json:"remove"`
}

func gitPatchPath(name string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, c := range []byte(name) {
		if c == '"' || c == '\\' {
			b.WriteByte('\\')
			b.WriteByte(c)
		} else if c < 32 || c >= 127 {
			fmt.Fprintf(&b, "\\%03o", c)
		} else {
			b.WriteByte(c)
		}
	}
	b.WriteByte('"')
	return b.String()
}
func gitPatchLines(text, prefix string) string {
	if text == "" {
		return ""
	}
	lines := strings.SplitAfter(text, "\n")
	var b strings.Builder
	for _, line := range lines {
		if line == "" {
			continue
		}
		b.WriteString(prefix)
		b.WriteString(line)
		if !strings.HasSuffix(line, "\n") {
			b.WriteString("\n\\ No newline at end of file\n")
		}
	}
	return b.String()
}
func gitSnapshotPatch(file, before, after string, exists, remove bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "diff --git %s %s\n", gitPatchPath("a/"+file), gitPatchPath("b/"+file))
	if !exists {
		b.WriteString("new file mode 100644\n")
	} else if remove {
		b.WriteString("deleted file mode 100644\n")
	}
	if before == "" && after == "" && (!exists || remove) {
		return b.String()
	}
	oldName, newName := gitPatchPath("a/"+file), gitPatchPath("b/"+file)
	if !exists {
		oldName = "/dev/null"
	}
	if remove {
		newName = "/dev/null"
	}
	fmt.Fprintf(&b, "--- %s\n+++ %s\n", oldName, newName)
	count := func(text string) int {
		if text == "" {
			return 0
		}
		n := strings.Count(text, "\n")
		if !strings.HasSuffix(text, "\n") {
			n++
		}
		return n
	}
	a, c := count(before), count(after)
	aStart, cStart := 1, 1
	if a == 0 {
		aStart = 0
	}
	if c == 0 {
		cStart = 0
	}
	fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@\n", aStart, a, cStart, c)
	b.WriteString(gitPatchLines(before, "-"))
	b.WriteString(gitPatchLines(after, "+"))
	return b.String()
}

func gitIndexApplyCommand(repository, file, expected string, exists bool) string {
	prefix := "git -C " + shellQuote(repository) + " --literal-pathspecs "
	blob := []byte(fmt.Sprintf("blob %d\x00", len(expected)) + expected)
	shaOne, shaTwo := sha1.Sum(blob), sha256.Sum256(blob)
	expectedCheck := `[ -z "$current" ]`
	if exists {
		expectedCheck = fmt.Sprintf(`[ "$current" = %s ] || [ "$current" = %s ]`, shellQuote(fmt.Sprintf("%x", shaOne)), shellQuote(fmt.Sprintf("%x", shaTwo)))
	}
	// Reserve Git's real index.lock, then apply to a private index. This keeps
	// external Git commands from changing the index between validation and commit.
	return `set -eu
index=$(` + prefix + `rev-parse --path-format=absolute --git-path index)
lock="$index.lock"
(set -C; : > "$lock") 2>/dev/null || { echo 'Git 正在进行其他操作，请稍后重试' >&2; exit 1; }
tmp=''
trap 'rm -f "$lock"; if [ -n "$tmp" ]; then rm -f "$tmp" "$tmp.lock"; fi' EXIT
trap 'exit 130' HUP INT TERM
tmp=$(mktemp "${index}.velin-XXXXXXXX")
if [ -f "$index" ]; then cp "$index" "$tmp"; else rm -f "$tmp"; fi
export GIT_INDEX_FILE="$tmp"
if [ ! -f "$tmp" ]; then ` + prefix + `read-tree --empty; fi
if [ -n "$(` + prefix + `ls-files --unmerged -- ` + shellQuote(file) + `)" ]; then echo '请先处理合并冲突' >&2; exit 1; fi
current=$(` + prefix + `rev-parse --verify --end-of-options ` + shellQuote(":"+file) + ` 2>/dev/null || :)
if ! { ` + expectedCheck + `; }; then echo '暂存区版本已变化，请刷新对比后重试' >&2; exit 1; fi
` + prefix + `apply --cached --unidiff-zero --whitespace=nowarn -
mv "$tmp" "$index"
`
}

// EditGitIndex applies a complete expected snapshot through Git's index lock.
// Every old line is included, so a concurrent index change rejects the patch.
func (m *Manager) EditGitIndex(ctx context.Context, userID, hostID string, in GitIndexEdit) error {
	if in.Repository == "" || path.IsAbs(in.Path) || in.Path == "" || in.Path == "." || in.Path == ".." || path.Clean(in.Path) != in.Path || strings.HasPrefix(in.Path, "../") || strings.ContainsAny(in.Path, "\x00\n\r") || strings.Contains("/"+in.Path+"/", "/.git/") {
		return errors.New("invalid Git file path")
	}
	if len(in.Expected) > 2<<20 || len(in.Content) > 2<<20 || !utf8.ValidString(in.Content) || strings.ContainsRune(in.Content, 0) {
		return errors.New("只能按块操作 2 MiB 以内的文本文件")
	}
	if in.Remove && in.Content != "" {
		return errors.New("invalid deletion snapshot")
	}
	current, exists, err := m.GitText(ctx, userID, hostID, in.Repository, in.Path, "0")
	if err != nil {
		return err
	}
	if exists != in.Exists || current != in.Expected {
		return errors.New("暂存区版本已变化，请刷新对比后重试")
	}
	if current == in.Content && exists == !in.Remove {
		return nil
	}
	conn, err := m.connection(userID, hostID)
	if err != nil {
		return err
	}
	// Reject unmerged entries, executable metadata changes are left intact by a
	// content-only patch. Deletion must retain the actual file mode.
	prefix := "git -C " + shellQuote(in.Repository) + " --literal-pathspecs "
	stages, err := conn.run(ctx, prefix+"ls-files --stage -z -- "+shellQuote(in.Path))
	if err != nil {
		return err
	}
	mode := "100644"
	for _, record := range strings.Split(stages, "\x00") {
		header, name, ok := strings.Cut(record, "\t")
		fields := strings.Fields(header)
		if !ok || name != in.Path || len(fields) != 3 {
			continue
		}
		if fields[2] != "0" {
			return errors.New("请先处理该文件的合并冲突")
		}
		if fields[0] != "100644" && fields[0] != "100755" {
			return errors.New("只支持普通文本文件")
		}
		mode = fields[0]
	}
	patch := gitSnapshotPatch(in.Path, in.Expected, in.Content, in.Exists, in.Remove)
	if in.Remove && mode != "100644" {
		patch = strings.Replace(patch, "deleted file mode 100644", "deleted file mode "+mode, 1)
	}
	session, err := conn.client.NewSession()
	if err != nil {
		return err
	}
	defer session.Close()
	session.Stdin = strings.NewReader(patch)
	var stdout, stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr
	done := make(chan error, 1)
	go func() { done <- session.Run(gitIndexApplyCommand(in.Repository, in.Path, in.Expected, in.Exists)) }()
	select {
	case err = <-done:
		if err != nil {
			return fmt.Errorf("暂存区更新失败，请刷新后重试: %s", strings.TrimSpace(stdout.String()+stderr.String()))
		}
		return nil
	case <-ctx.Done():
		session.Close()
		return ctx.Err()
	}
}
