//go:build !windows

package api

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pkg/sftp"
	"github.com/ttyob/velin-web-ssh/internal/telegram"
)

func telegramSFTPFixture(t *testing.T) *sftp.Client {
	t.Helper()
	left, right := net.Pipe()
	server, err := sftp.NewServer(right)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = server.Serve(); close(done) }()
	client, err := sftp.NewClientPipe(left, left)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close(); _ = left.Close(); _ = server.Close(); _ = right.Close(); <-done })
	return client
}
func TestTelegramProjectFilesAndSymlinkBoundaries(t *testing.T) {
	client := telegramSFTPFixture(t)
	base := t.TempDir()
	ctx := context.Background()
	allow := func() error { return nil }
	uploaded, err := uploadTelegramFile(ctx, client, base, "../../bad $(touch injected).png", strings.NewReader("image-bytes"), allow)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(uploaded, filepath.Join(base, "clipboard")+"/") || strings.ContainsAny(filepath.Base(uploaded), "$() /\\") {
		t.Fatal("unsafe upload path", uploaded)
	}
	data, err := os.ReadFile(uploaded)
	if err != nil || string(data) != "image-bytes" {
		t.Fatal("wrong upload", err)
	}
	info, err := os.Stat(uploaded)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("uploaded file permissions", info, err)
	}
	relative, _ := filepath.Rel(base, uploaded)
	_, data, err = readTelegramFile(ctx, client, base, relative, allow)
	if err != nil || string(data) != "image-bytes" {
		t.Fatal("download failed", err)
	}
	outside := t.TempDir()
	if err = os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("outside-project"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(outside, filepath.Join(base, "escape")); err != nil {
		t.Fatal(err)
	}
	for _, relative := range []string{"../secret.txt", "/etc/passwd", "a/../../secret.txt", "escape/secret.txt", "clipboard", "bad\x00name"} {
		if _, _, err = readTelegramFile(ctx, client, base, relative, allow); err == nil {
			t.Fatal("out of scope download accepted", relative)
		}
	}
	deny := func() error { return errors.New("revoked") }
	if _, err = uploadTelegramFile(ctx, client, base, "revoked.txt", strings.NewReader("private"), deny); err == nil {
		t.Fatal("upload ignored revocation")
	}
	entries, err := os.ReadDir(filepath.Join(base, "clipboard"))
	if err != nil || len(entries) != 1 {
		t.Fatal("failed upload left partial files", entries, err)
	}
	if _, _, err = readTelegramFile(ctx, client, base, relative, deny); err == nil {
		t.Fatal("download ignored revocation")
	}
	otherProject := t.TempDir()
	if err = os.Symlink(outside, filepath.Join(otherProject, "clipboard")); err != nil {
		t.Fatal(err)
	}
	if _, err = uploadTelegramFile(ctx, client, otherProject, "image.png", strings.NewReader("data"), allow); err == nil {
		t.Fatal("upload followed clipboard symlink")
	}
}

type telegramZeroReader struct{}

func (telegramZeroReader) Read(data []byte) (int, error) { clear(data); return len(data), nil }
func TestTelegramUploadLimitRemovesPartialFile(t *testing.T) {
	client := telegramSFTPFixture(t)
	base := t.TempDir()
	_, err := uploadTelegramFile(context.Background(), client, base, "large.bin", io.LimitReader(telegramZeroReader{}, telegram.MaxFileSize+1), func() error { return nil })
	if err == nil {
		t.Fatal("oversized upload accepted")
	}
	entries, err := os.ReadDir(filepath.Join(base, "clipboard"))
	if err != nil || len(entries) != 0 {
		t.Fatal("oversized upload left data", entries, err)
	}
}
