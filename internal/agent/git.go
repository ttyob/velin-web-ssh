package agent

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/ssh"
)

// GitText reads a complete Git blob. Unlike terminal command output it must
// never be truncated, since it is used as the source of an editable merge.
func (m *Manager) GitText(ctx context.Context, userID, hostID, repository, file, revision string) (string, bool, error) {
	if repository == "" || file == "" || len(repository)+len(file) > 5000 || strings.ContainsAny(repository+file, "\x00") {
		return "", false, errors.New("invalid Git file path")
	}
	if revision != "HEAD" && revision != "0" && revision != "2" && revision != "3" {
		return "", false, errors.New("invalid Git file revision")
	}
	conn, err := m.connection(userID, hostID)
	if err != nil {
		return "", false, err
	}
	prefix := "git -C " + shellQuote(repository) + " "
	var listing string
	if revision == "HEAD" {
		// An unborn branch has no original version.
		output, e := conn.run(ctx, prefix+"rev-parse --verify -q HEAD")
		if e != nil {
			var exit *ssh.ExitError
			if errors.As(e, &exit) && exit.ExitStatus() == 1 && strings.TrimSpace(output) == "" {
				return "", false, nil
			}
			return "", false, e
		}
		listing, err = conn.run(ctx, prefix+"ls-tree -z HEAD -- "+shellQuote(":(literal)"+file))
	} else {
		listing, err = conn.run(ctx, prefix+"ls-files --stage -z -- "+shellQuote(":(literal)"+file))
	}
	if err != nil {
		return "", false, err
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
			return "", false, errors.New("只能对比普通文本文件，符号链接和子模块请在终端处理")
		}
		if revision == "HEAD" {
			oid = fields[2]
		} else {
			oid = fields[1]
		}
		break
	}
	if oid == "" {
		return "", false, nil
	}
	sizeText, err := conn.run(ctx, prefix+"cat-file -s "+shellQuote(oid))
	if err != nil {
		return "", false, err
	}
	size, err := strconv.Atoi(strings.TrimSpace(sizeText))
	if err != nil || size < 0 || size > 2<<20 {
		return "", false, errors.New("文件超过 2 MiB 完整对比上限")
	}
	content, err := conn.run(ctx, prefix+"cat-file blob "+shellQuote(oid))
	if err != nil {
		return "", false, err
	}
	if len(content) != size {
		return "", false, fmt.Errorf("Git 文件读取不完整，期望 %d 字节，实际 %d 字节", size, len(content))
	}
	if !utf8.ValidString(content) || strings.ContainsRune(content, '\x00') {
		return "", false, errors.New("二进制文件不支持文本合并，请在终端处理")
	}
	return content, true, nil
}
