package agent

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestIndexSnapshotEdits(t *testing.T) {
	for _, format := range []string{"sha1", "sha256"} {
		t.Run(format, func(t *testing.T) {
			repository := t.TempDir()
			git := func(args ...string) string {
				t.Helper()
				out, err := exec.Command("git", append([]string{"-C", repository}, args...)...).CombinedOutput()
				if err != nil {
					t.Fatalf("git %v: %v %s", args, err, out)
				}
				return string(out)
			}
			git("init", "--object-format="+format, "-b", "main")
			name := "目录/odd ' $[x] file.txt"
			os.MkdirAll(filepath.Join(repository, "目录"), 0700)
			before := "first\nkeep\nlast"
			os.WriteFile(filepath.Join(repository, name), []byte(before), 0600)
			git("add", "--", name)
			apply := func(expected, next string, exists, remove bool) error {
				command := exec.Command("sh", "-c", gitIndexApplyCommand(repository, name, expected, exists))
				command.Stdin = strings.NewReader(gitSnapshotPatch(name, expected, next, exists, remove))
				out, err := command.CombinedOutput()
				if err != nil {
					t.Logf("apply: %s", out)
				}
				return err
			}
			next := "changed\nkeep\nlast"
			if err := apply(before, next, true, false); err != nil {
				t.Fatal(err)
			}
			if got := git("show", ":"+name); got != next {
				t.Fatalf("index: %q", got)
			}
			work, _ := os.ReadFile(filepath.Join(repository, name))
			if string(work) != before {
				t.Fatal("worktree changed")
			}
			// A concurrent append cannot be lost or silently preserved in an operation
			// claiming to apply to an earlier snapshot.
			external := next + "\nexternal\n"
			os.WriteFile(filepath.Join(repository, name), []byte(external), 0600)
			git("add", "--", name)
			if err := apply(next, "discard", true, false); err == nil {
				t.Fatal("stale index accepted")
			}
			if git("show", ":"+name) != external {
				t.Fatal("concurrent index overwritten")
			}
			if err := apply(external, "", true, true); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(git("ls-files", "--", name), name) {
				t.Fatal("not removed")
			}
			if err := apply("", "added without newline", false, false); err != nil {
				t.Fatal(err)
			}
			if git("show", ":"+name) != "added without newline" {
				t.Fatal("new file differs")
			}
			if err := apply("added without newline", "", true, true); err != nil {
				t.Fatal(err)
			}
			if err := apply("", "", false, false); err != nil {
				t.Fatal("empty file:", err)
			}
			if git("show", ":"+name) != "" {
				t.Fatal("empty new file differs")
			}
			if _, err := os.Stat(filepath.Join(repository, ".git/index.lock")); !os.IsNotExist(err) {
				t.Fatal("lock leaked")
			}
		})
	}
}
