package agent

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// Exercise the actual SSH transport and Git object reader, including blobs
// larger than terminal output limits and paths requiring shell quoting.
func TestGitTextCompleteSnapshots(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(private)
	if err != nil {
		t.Fatal(err)
	}
	config := &ssh.ServerConfig{NoClientAuth: true}
	config.AddHostKey(signer)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		raw, e := listener.Accept()
		if e != nil {
			return
		}
		conn, channels, requests, e := ssh.NewServerConn(raw, config)
		if e != nil {
			raw.Close()
			return
		}
		defer conn.Close()
		go ssh.DiscardRequests(requests)
		for incoming := range channels {
			ch, requests, e := incoming.Accept()
			if e != nil {
				continue
			}
			go func() {
				defer ch.Close()
				for request := range requests {
					if request.Type != "exec" {
						request.Reply(false, nil)
						continue
					}
					var payload struct{ Command string }
					if ssh.Unmarshal(request.Payload, &payload) != nil {
						request.Reply(false, nil)
						return
					}
					request.Reply(true, nil)
					command := exec.Command("sh", "-c", payload.Command)
					command.Stdin = ch
					command.Stdout = ch
					command.Stderr = ch.Stderr()
					status := uint32(0)
					if err := command.Run(); err != nil {
						status = 1
					}
					ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{status}))
					return
				}
			}()
		}
	}()
	client, err := ssh.Dial("tcp", listener.Addr().String(), &ssh.ClientConfig{User: "fixture", HostKeyCallback: ssh.InsecureIgnoreHostKey(), Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	manager := NewManager(nil, AIConfig{})
	manager.conns[connectionKey("user", "host")] = &connection{client: client}
	repository := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		out, e := exec.Command("git", append([]string{"-C", repository}, args...)...).CombinedOutput()
		if e != nil {
			t.Fatalf("git %v: %v %s", args, e, out)
		}
		return string(out)
	}
	git("init", "-b", "main")
	git("config", "user.email", "test@example.invalid")
	git("config", "user.name", "Fixture")
	name := "long ' 中文 [*].txt"
	content := strings.Repeat("完整文件\r\n", 18000) + "last line without newline"
	if e := os.WriteFile(filepath.Join(repository, name), []byte(content), 0600); e != nil {
		t.Fatal(e)
	}
	git("add", ".")
	git("commit", "-m", "base")
	read := func(revision, file, want string, exists bool) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		got, found, e := manager.GitText(ctx, "user", "host", repository, file, revision)
		if e != nil || got != want || found != exists {
			t.Fatalf("%s %q: len=%d want=%d exists=%v error=%v", revision, file, len(got), len(want), found, e)
		}
	}
	read("HEAD", name, content, true)
	read("0", name, content, true)
	read("HEAD", "missing.txt", "", false)
	next := strings.Replace(content, "完整文件", "新的修改", 1)
	edit := GitIndexEdit{Repository: repository, Path: name, Expected: content, Exists: true, Content: next}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := manager.EditGitIndex(ctx, "user", "host", edit); err != nil {
		t.Fatal(err)
	}
	read("0", name, next, true)
	read("HEAD", name, content, true)
	work, _ := os.ReadFile(filepath.Join(repository, name))
	if string(work) != content {
		t.Fatal("SSH index edit touched worktree")
	}
	if err := manager.EditGitIndex(ctx, "user", "host", edit); err == nil {
		t.Fatal("stale SSH index edit accepted")
	}
	edit.Expected = next
	edit.Content = content
	if err := manager.EditGitIndex(ctx, "user", "host", edit); err != nil {
		t.Fatal(err)
	}
	git("branch", "incoming")
	if e := os.WriteFile(filepath.Join(repository, name), []byte("ours\n"), 0600); e != nil {
		t.Fatal(e)
	}
	git("commit", "-am", "ours")
	git("switch", "incoming")
	if e := os.WriteFile(filepath.Join(repository, name), []byte("theirs\n"), 0600); e != nil {
		t.Fatal(e)
	}
	git("commit", "-am", "theirs")
	git("switch", "main")
	if out, e := exec.Command("git", "-C", repository, "merge", "--no-edit", "incoming").CombinedOutput(); e == nil {
		t.Fatal(fmt.Sprintf("expected conflict: %s", out))
	}
	read("2", name, "ours\n", true)
	read("3", name, "theirs\n", true)
	if e := os.WriteFile(filepath.Join(repository, "binary"), []byte{'a', 0, 'b'}, 0600); e != nil {
		t.Fatal(e)
	}
	git("add", "binary")
	if _, _, e := manager.GitText(context.Background(), "user", "host", repository, "binary", "0"); e == nil {
		t.Fatal("binary accepted")
	}
	if e := os.WriteFile(filepath.Join(repository, "too-large"), []byte(strings.Repeat("x", (2<<20)+1)), 0600); e != nil {
		t.Fatal(e)
	}
	git("add", "too-large")
	if _, _, e := manager.GitText(context.Background(), "user", "host", repository, "too-large", "0"); e == nil {
		t.Fatal("oversized file accepted")
	}
	for _, test := range []struct {
		file   string
		kind   string
		exists bool
	}{
		{"binary", "binary", true}, {"too-large", "text", true}, {"missing", "text", false},
	} {
		info, err := manager.GitFileInfo(ctx, "user", "host", repository, test.file, "0")
		if err != nil || info.Kind != test.kind || info.Exists != test.exists {
			t.Fatalf("metadata %s: %+v %v", test.file, info, err)
		}
		if test.file == "too-large" && info.Size != (2<<20)+1 {
			t.Fatalf("large blob size: %+v", info)
		}
	}
	image := "\x89PNG\r\n\x1a\n" + strings.Repeat("\x00", 100)
	if err := os.WriteFile(filepath.Join(repository, "preview.png"), []byte(image), 0600); err != nil {
		t.Fatal(err)
	}
	git("add", "preview.png")
	info, err := manager.GitFileInfo(ctx, "user", "host", repository, "preview.png", "0")
	if err != nil || info.Kind != "image" {
		t.Fatalf("image metadata: %+v %v", info, err)
	}
	gotImage, mime, err := manager.GitPreviewImage(ctx, "user", "host", repository, "preview.png", "0")
	if err != nil || mime != "image/png" || gotImage != image {
		t.Fatalf("image bytes altered: %s %v", mime, err)
	}
	if _, _, err := manager.GitPreviewImage(ctx, "user", "host", repository, "binary", "0"); err == nil {
		t.Fatal("binary served as image")
	}
}
