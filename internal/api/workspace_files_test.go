package api

import (
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pkg/sftp"
)

func TestWorkspaceSearch(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"src/nested", "node_modules/skip", "tests"} {
		os.MkdirAll(filepath.Join(root, dir), 0700)
	}
	for name, text := range map[string]string{"src/main.ts": "one\n汉字😀 Needle 中文\nneedle again\n", "src/nested/x.ts": "NEEDLE\n", "tests/test.ts": "needle", "node_modules/skip/hidden.ts": "needle", "binary.ts": "a\x00needle"} {
		os.WriteFile(filepath.Join(root, name), []byte(text), 0600)
	}
	os.Symlink(filepath.Join(root, "tests"), filepath.Join(root, "linked"))
	readDir := func(name string) ([]os.FileInfo, error) {
		entries, err := os.ReadDir(name)
		if err != nil {
			return nil, err
		}
		out := []os.FileInfo{}
		for _, entry := range entries {
			info, err := entry.Info()
			if err != nil {
				return nil, err
			}
			out = append(out, info)
		}
		return out, nil
	}
	readFile := func(name string) (io.ReadCloser, error) { return os.Open(name) }
	in := workspaceSearchInput{Root: root, Query: "needle", Include: "**/*.ts", Exclude: "tests/**"}
	result, err := searchWorkspace(context.Background(), in, readDir, readFile)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Matches) != 3 {
		t.Fatalf("%+v", result)
	}
	for _, match := range result.Matches {
		if strings.Contains(match.Path, "linked") || strings.Contains(match.Path, "node_modules") {
			t.Fatal("excluded path returned")
		}
		if match.Line == 2 {
			if match.Column != 6 || match.Start != 5 || match.End != 11 {
				t.Fatalf("Unicode offsets: %+v", match)
			}
		}
	}
	in.CaseSensitive = true
	result, err = searchWorkspace(context.Background(), in, readDir, readFile)
	if err != nil || len(result.Matches) != 1 {
		t.Fatalf("case sensitive: %+v %v", result, err)
	}
	in.Regex = true
	in.Query = "["
	if _, err = searchWorkspace(context.Background(), in, readDir, readFile); err == nil {
		t.Fatal("invalid regexp accepted")
	}
	in.Query = "needle"
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err = searchWorkspace(ctx, in, readDir, readFile)
	if err != nil || !result.Limited {
		t.Fatal("cancel ignored")
	}
}
func TestWorkspaceCopy(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	server, err := sftp.NewServer(serverConn)
	if err != nil {
		t.Fatal(err)
	}
	go server.Serve()
	defer server.Close()
	client, err := sftp.NewClientPipe(clientConn, clientConn)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	root := t.TempDir()
	source := filepath.Join(root, "source")
	destination := filepath.Join(root, "copy")
	os.Mkdir(source, 0700)
	content := []byte("\x00binary\xffpayload")
	os.WriteFile(filepath.Join(source, "file.bin"), content, 0750)
	count := 0
	if err = copyWorkspaceFile(context.Background(), client, source, destination, &count); err != nil {
		t.Fatal(err)
	}
	copied, err := os.ReadFile(filepath.Join(destination, "file.bin"))
	if err != nil || string(copied) != string(content) {
		t.Fatal("copied bytes differ")
	}
	info, _ := os.Stat(filepath.Join(destination, "file.bin"))
	if info.Mode().Perm() != 0750 {
		t.Fatal("permissions lost")
	}
	count = 0
	if err = copyWorkspaceFile(context.Background(), client, source, destination, &count); err == nil {
		t.Fatal("existing directory overwritten")
	}
	os.Symlink(filepath.Join(source, "file.bin"), filepath.Join(root, "link"))
	count = 0
	if err = copyWorkspaceFile(context.Background(), client, filepath.Join(root, "link"), filepath.Join(root, "link-copy"), &count); err == nil {
		t.Fatal("symlink copied")
	}
}
