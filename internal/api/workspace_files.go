package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/pkg/sftp"
)

type workspaceSearchInput struct {
	Root          string `json:"root"`
	Query         string `json:"query"`
	Include       string `json:"include"`
	Exclude       string `json:"exclude"`
	CaseSensitive bool   `json:"caseSensitive"`
	Regex         bool   `json:"regex"`
}
type workspaceMatch struct {
	Path   string `json:"path"`
	Line   int    `json:"line"`
	Column int    `json:"column"`
	Text   string `json:"text"`
	Start  int    `json:"start"`
	End    int    `json:"end"`
}
type workspaceSearchResult struct {
	Matches []workspaceMatch `json:"matches"`
	Files   int              `json:"files"`
	Skipped int              `json:"skipped"`
	Limited bool             `json:"limited"`
}

func workspaceUTF16Length(text string) int {
	n := 0
	for _, r := range text {
		n++
		if r > 0xffff {
			n++
		}
	}
	return n
}
func workspaceGlobs(input string) ([]*regexp.Regexp, error) {
	var result []*regexp.Regexp
	for _, pattern := range strings.Split(input, ",") {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		var b strings.Builder
		b.WriteString("^")
		for i := 0; i < len(pattern); i++ {
			switch pattern[i] {
			case '*':
				if i+1 < len(pattern) && pattern[i+1] == '*' {
					i++
					if i+1 < len(pattern) && pattern[i+1] == '/' {
						i++
						b.WriteString("(?:.*/)?")
					} else {
						b.WriteString(".*")
					}
				} else {
					b.WriteString("[^/]*")
				}
			case '?':
				b.WriteString("[^/]")
			default:
				b.WriteString(regexp.QuoteMeta(pattern[i : i+1]))
			}
		}
		b.WriteString("$")
		re, err := regexp.Compile(b.String())
		if err != nil {
			return nil, err
		}
		result = append(result, re)
	}
	return result, nil
}
func workspaceGlobMatch(patterns []*regexp.Regexp, name string) bool {
	for _, pattern := range patterns {
		if pattern.MatchString(name) || pattern.MatchString(path.Base(name)) {
			return true
		}
	}
	return false
}
func searchWorkspace(ctx context.Context, in workspaceSearchInput, readDir func(string) ([]os.FileInfo, error), readFile func(string) (io.ReadCloser, error)) (workspaceSearchResult, error) {
	result := workspaceSearchResult{Matches: make([]workspaceMatch, 0)}
	if in.Query == "" || len(in.Query) > 1000 {
		return result, errors.New("请输入搜索内容，最多 1000 字符")
	}
	expression := in.Query
	if !in.Regex {
		expression = regexp.QuoteMeta(expression)
	}
	if !in.CaseSensitive {
		expression = "(?i)" + expression
	}
	re, err := regexp.Compile(expression)
	if err != nil {
		return result, fmt.Errorf("正则表达式无效: %w", err)
	}
	includes, err := workspaceGlobs(in.Include)
	if err != nil {
		return result, err
	}
	excludes, err := workspaceGlobs(in.Exclude)
	if err != nil {
		return result, err
	}
	ignored := map[string]bool{".git": true, "node_modules": true, "vendor": true, ".venv": true, "__pycache__": true, "dist": true, "build": true, ".next": true, "target": true}
	queue := []string{in.Root}
	visited := 0
	bytesRead := 0
	for len(queue) > 0 {
		if ctx.Err() != nil {
			result.Limited = true
			return result, nil
		}
		dir := queue[0]
		queue = queue[1:]
		visited++
		if visited > 5000 {
			result.Limited = true
			break
		}
		entries, e := readDir(dir)
		if e != nil {
			result.Skipped++
			continue
		}
		for _, entry := range entries {
			if ctx.Err() != nil {
				result.Limited = true
				return result, nil
			}
			absolute := path.Join(dir, entry.Name())
			relative := strings.TrimPrefix(absolute, strings.TrimSuffix(in.Root, "/")+"/")
			if entry.Mode()&os.ModeSymlink != 0 || workspaceGlobMatch(excludes, relative) {
				continue
			}
			if entry.IsDir() {
				if !ignored[entry.Name()] {
					queue = append(queue, absolute)
				}
				continue
			}
			if !entry.Mode().IsRegular() || entry.Size() > maxEditableTextSize || (len(includes) > 0 && !workspaceGlobMatch(includes, relative)) {
				continue
			}
			if result.Files >= 20000 || bytesRead >= 64<<20 {
				result.Limited = true
				return result, nil
			}
			f, e := readFile(absolute)
			if e != nil {
				result.Skipped++
				continue
			}
			data, e := io.ReadAll(io.LimitReader(f, maxEditableTextSize+1))
			f.Close()
			bytesRead += len(data)
			result.Files++
			if e != nil || len(data) > maxEditableTextSize || !utf8.Valid(data) || strings.IndexByte(string(data), 0) >= 0 {
				result.Skipped++
				continue
			}
			for lineNo, line := range strings.Split(string(data), "\n") {
				locations := re.FindAllStringIndex(line, 1001)
				for _, loc := range locations {
					if loc[0] == loc[1] {
						continue
					}
					before := []rune(line[:loc[0]])
					matched := []rune(line[loc[0]:loc[1]])
					after := []rune(strings.TrimSuffix(line[loc[1]:], "\r"))
					column := workspaceUTF16Length(line[:loc[0]]) + 1
					if len(before) > 120 {
						before = before[len(before)-120:]
					}
					if len(matched) > 400 {
						matched = matched[:400]
					}
					if len(after) > 120 {
						after = after[:120]
					}
					prefix := string(before)
					text := prefix + string(matched) + string(after)
					result.Matches = append(result.Matches, workspaceMatch{absolute, lineNo + 1, column, text, workspaceUTF16Length(prefix), workspaceUTF16Length(prefix + string(matched))})
					if len(result.Matches) >= 1000 {
						result.Limited = true
						return result, nil
					}
				}
			}
		}
	}
	return result, nil
}
func (a *API) workspaceSearch(w http.ResponseWriter, r *http.Request) {
	var in workspaceSearchInput
	if !decode(w, r, &in) {
		return
	}
	in.Root = cleanRemotePath(in.Root)
	if !path.IsAbs(in.Root) {
		writeError(w, 400, "invalid_path", "请选择绝对目录")
		return
	}
	client, err := a.openSFTP(r)
	if err != nil {
		writeError(w, 502, "sftp_connection_failed", err.Error())
		return
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { client.Close() })
	defer stop()
	result, err := searchWorkspace(ctx, in, client.ReadDir, func(name string) (io.ReadCloser, error) { return client.Open(name) })
	if err != nil {
		writeError(w, 400, "search_failed", err.Error())
		return
	}
	writeJSON(w, 200, result)
}
func remoteWithin(root, name string) bool {
	return root == name || strings.HasPrefix(name, strings.TrimSuffix(root, "/")+"/")
}
func copyWorkspaceFile(ctx context.Context, client *sftp.Client, source, destination string, count *int) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	*count++
	if *count > 20000 {
		return errors.New("一次最多复制 20000 个文件或目录")
	}
	info, err := client.Lstat(source)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("复制内容包含符号链接，请在终端处理")
	}
	if info.IsDir() {
		if err = client.Mkdir(destination); err != nil {
			return err
		}
		entries, e := client.ReadDir(source)
		if e != nil {
			return e
		}
		for _, entry := range entries {
			if err = copyWorkspaceFile(ctx, client, path.Join(source, entry.Name()), path.Join(destination, entry.Name()), count); err != nil {
				return err
			}
		}
		return nil
	}
	if !info.Mode().IsRegular() {
		return errors.New("只能复制普通文件和目录")
	}
	input, err := client.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := client.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
	if err != nil {
		return err
	}
	_, err = io.Copy(output, input)
	closeErr := output.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return client.Chmod(destination, info.Mode().Perm())
}
func (a *API) workspaceCopy(w http.ResponseWriter, r *http.Request) {
	var in struct{ Root, Source, Destination string }
	if !decode(w, r, &in) {
		return
	}
	root := cleanRemotePath(in.Root)
	source, e1 := writableRemotePath(in.Source)
	destination, e2 := writableRemotePath(in.Destination)
	if !path.IsAbs(root) || e1 != nil || e2 != nil || !remoteWithin(root, source) || !remoteWithin(root, destination) || remoteWithin(source, destination) {
		writeError(w, 400, "invalid_path", "源和目标必须在工作区内，不能复制到自身目录")
		return
	}
	client, err := a.openSFTP(r)
	if err != nil {
		writeError(w, 502, "sftp_connection_failed", err.Error())
		return
	}
	defer client.Close()
	if _, err = client.Lstat(destination); err == nil {
		writeError(w, 409, "destination_exists", "目标路径已存在")
		return
	} else if !os.IsNotExist(err) {
		writeError(w, 502, "copy_failed", err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { client.Close() })
	defer stop()
	count := 0
	err = copyWorkspaceFile(ctx, client.Client, source, destination, &count)
	if err != nil {
		writeError(w, 502, "copy_failed", fmt.Sprintf("复制未完成，请检查目标目录：%s", err))
		return
	}
	writeJSON(w, 201, map[string]any{"ok": true, "count": count})
}
