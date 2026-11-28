package agent

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/ttyob/velin-web-ssh/internal/filepreview"
	"golang.org/x/crypto/ssh"
)

// GitText reads a complete Git blob. Unlike terminal command output it must
// never be truncated, since it is used as the source of an editable merge.
func (m *Manager) GitText(ctx context.Context, userID, hostID, repository, file, revision string) (string, bool, error) {
	conn, oid, size, err := m.gitBlob(ctx, userID, hostID, repository, file, revision)
	if err != nil || oid == "" {
		return "", false, err
	}
	if size > 2<<20 {
		return "", false, errors.New("文件超过 2 MiB 完整对比上限")
	}
	content, err := conn.run(ctx, "git -C "+shellQuote(repository)+" cat-file blob "+shellQuote(oid))
	if err != nil {
		return "", false, err
	}
	if int64(len(content)) != size {
		return "", false, fmt.Errorf("Git 文件读取不完整，期望 %d 字节，实际 %d 字节", size, len(content))
	}
	if !utf8.ValidString(content) || strings.ContainsRune(content, '\x00') {
		return "", false, errors.New("二进制文件不支持文本合并，请在终端处理")
	}
	return content, true, nil
}

func (m *Manager) GitFileInfo(ctx context.Context, userID, hostID, repository, file, revision string) (filepreview.Info, error) {
	conn, oid, size, err := m.gitBlob(ctx, userID, hostID, repository, file, revision)
	if err != nil || oid == "" {
		return filepreview.Info{Kind: "text"}, err
	}
	header, err := conn.run(ctx, "git -C "+shellQuote(repository)+" cat-file blob "+shellQuote(oid)+" | head -c 4096")
	if err != nil {
		return filepreview.Info{}, err
	}
	return filepreview.Inspect(file, size, []byte(header)), nil
}

func (m *Manager) GitPreviewImage(ctx context.Context, userID, hostID, repository, file, revision string) (string, string, error) {
	conn, oid, size, err := m.gitBlob(ctx, userID, hostID, repository, file, revision)
	if err != nil {
		return "", "", err
	}
	if oid == "" || size <= 0 || size > 32<<20 {
		return "", "", errors.New("图片不存在或超过 32 MiB 预览上限")
	}
	content, err := conn.run(ctx, "git -C "+shellQuote(repository)+" cat-file blob "+shellQuote(oid))
	if err != nil {
		return "", "", err
	}
	if int64(len(content)) != size {
		return "", "", errors.New("Git 图片读取不完整")
	}
	header := []byte(content)
	if len(header) > 512 {
		header = header[:512]
	}
	contentType := filepreview.ImageType(file, header)
	if contentType == "" {
		return "", "", errors.New("文件不是支持的图片格式")
	}
	return content, contentType, nil
}

func (m *Manager) gitBlob(ctx context.Context, userID, hostID, repository, file, revision string) (*connection, string, int64, error) {
	if repository == "" || file == "" || len(repository)+len(file) > 5000 || strings.ContainsAny(repository+file, "\x00") {
		return nil, "", 0, errors.New("invalid Git file path")
	}
	if revision != "HEAD" && revision != "0" && revision != "2" && revision != "3" {
		return nil, "", 0, errors.New("invalid Git file revision")
	}
	conn, err := m.connection(userID, hostID)
	if err != nil {
		return nil, "", 0, err
	}
	prefix := "git -C " + shellQuote(repository) + " "
	var listing string
	if revision == "HEAD" {
		// An unborn branch has no original version.
		output, e := conn.run(ctx, prefix+"rev-parse --verify -q HEAD")
		if e != nil {
			var exit *ssh.ExitError
			if errors.As(e, &exit) && exit.ExitStatus() == 1 && strings.TrimSpace(output) == "" {
				return conn, "", 0, nil
			}
			return nil, "", 0, e
		}
		listing, err = conn.run(ctx, prefix+"ls-tree -z HEAD -- "+shellQuote(":(literal)"+file))
	} else {
		listing, err = conn.run(ctx, prefix+"ls-files --stage -z -- "+shellQuote(":(literal)"+file))
	}
	if err != nil {
		return nil, "", 0, err
	}
	oid := ""
	for _, record := range strings.Split(listing, "\x00") {
		header, name, found := strings.Cut(record, "\t")
		fields := strings.Fields(header)
		if !found || name != file || len(fields) != 3 {
			continue
		}
		if revision != "HEAD" && fields[2] != revision {
			continue
		}
		if fields[0] != "100644" && fields[0] != "100755" {
			return nil, "", 0, errors.New("只能对比普通文件，符号链接和子模块请在终端处理")
		}
		if revision == "HEAD" {
			oid = fields[2]
		} else {
			oid = fields[1]
		}
		break
	}
	if oid == "" {
		return conn, "", 0, nil
	}
	sizeText, err := conn.run(ctx, prefix+"cat-file -s "+shellQuote(oid))
	if err != nil {
		return nil, "", 0, err
	}
	size, err := strconv.ParseInt(strings.TrimSpace(sizeText), 10, 64)
	if err != nil || size < 0 {
		return nil, "", 0, errors.New("Git 文件大小无效")
	}
	return conn, oid, size, nil
}
