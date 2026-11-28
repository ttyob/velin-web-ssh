//go:build windows

package main

import (
	"crypto/sha256"
	"fmt"
	"testing"
)

func TestValidWindowsReleaseTag(t *testing.T) {
	for _, test := range []struct {
		tag  string
		want bool
	}{
		{tag: "v1.2.3", want: true},
		{tag: "v1.2.3-beta.1", want: true},
		{tag: "v1.2", want: false},
		{tag: "v1.2.3+build", want: false},
		{tag: "v1.2.3/other", want: false},
		{tag: "release", want: false},
	} {
		if got := validWindowsReleaseTag(test.tag); got != test.want {
			t.Errorf("validWindowsReleaseTag(%q) = %v, want %v", test.tag, got, test.want)
		}
	}
}

func TestChecksumForWindowsAsset(t *testing.T) {
	data := []byte("test executable")
	sum := sha256.Sum256(data)
	line := fmt.Sprintf("%x  velin-web-ssh-windows-gui-amd64-v1.2.3.exe\n", sum)
	got, err := checksumForAsset([]byte(line), "velin-web-ssh-windows-gui-amd64-v1.2.3.exe")
	if err != nil || got != fmt.Sprintf("%x", sum) {
		t.Fatalf("checksumForAsset() = %q, %v", got, err)
	}
}

func TestValidWindowsAssetURL(t *testing.T) {
	good := "https://github.com/ttyob/velin-web-ssh/releases/download/v1.2.3/velin-web-ssh-windows-gui-amd64-v1.2.3.exe"
	bad := []string{
		"http://github.com/ttyob/velin-web-ssh/releases/download/v1.2.3/file.exe",
		"https://example.com/ttyob/velin-web-ssh/releases/download/v1.2.3/file.exe",
		"https://github.com/attacker/velin-web-ssh/releases/download/v1.2.3/file.exe",
		"https://github.com/ttyob/velin-web-ssh/releases/download/v9.9.9/file.exe",
	}
	if !validWindowsAssetURL(good, "v1.2.3") {
		t.Fatal("valid GitHub Release asset URL was rejected")
	}
	for _, raw := range bad {
		if validWindowsAssetURL(raw, "v1.2.3") {
			t.Errorf("validWindowsAssetURL(%q) accepted an untrusted URL", raw)
		}
	}
}
