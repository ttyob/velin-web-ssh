package filepreview

import "testing"

func TestInspect(t *testing.T) {
	for _, test := range []struct {
		name   string
		size   int64
		header string
		kind   string
	}{
		{"code.php", 10, "<?php\n", "text"},
		{"code.txt", 50, "中文\xe4\xb8", "text"},
		{"bad.txt", 5, "a\xffb", "binary"},
		{"bad.bin", 3, "a\x00b", "binary"},
		{"photo.png", 200, "\x89PNG\r\n\x1a\n", "image"},
		{"fake.png", 20, "plain text", "text"},
		{"vector.svg", 100, "<svg xmlns=\"http://www.w3.org/2000/svg\"></svg>", "text"},
	} {
		info := Inspect(test.name, test.size, []byte(test.header))
		if !info.Exists || info.Size != test.size || info.Kind != test.kind {
			t.Fatalf("%s: %+v", test.name, info)
		}
	}
}
