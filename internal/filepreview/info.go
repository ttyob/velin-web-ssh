package filepreview

import (
	"bytes"
	"net/http"
	"path"
	"strings"
	"unicode/utf8"
)

// Info describes a file without transferring its complete content.
type Info struct {
	Exists bool   `json:"exists"`
	Size   int64  `json:"size"`
	Kind   string `json:"kind"`
}

func Inspect(name string, size int64, header []byte) Info {
	info := Info{Exists: true, Size: size, Kind: "text"}
	if ImageType(name, header) != "" {
		info.Kind = "image"
		return info
	}
	// A sample may end in the middle of a UTF-8 character.
	if int64(len(header)) < size && len(header) > 0 {
		start := len(header) - 1
		for start > 0 && !utf8.RuneStart(header[start]) {
			start--
		}
		if !utf8.FullRune(header[start:]) {
			header = header[:start]
		}
	}
	if bytes.IndexByte(header, 0) >= 0 || !utf8.Valid(header) {
		info.Kind = "binary"
	}
	return info
}

func ImageType(name string, header []byte) string {
	detected := http.DetectContentType(header)
	switch detected {
	case "image/png", "image/jpeg", "image/gif", "image/webp", "image/bmp", "image/x-icon":
		return detected
	}
	if detected == "application/octet-stream" && strings.EqualFold(path.Ext(name), ".avif") && len(header) >= 12 && string(header[4:8]) == "ftyp" && (string(header[8:12]) == "avif" || string(header[8:12]) == "avis") {
		return "image/avif"
	}
	return ""
}
