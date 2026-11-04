//go:build !windows

package main

import "github.com/ttyob/velin-web-ssh/internal/config"

func loadServerConfig() (config.Config, error) {
	return config.Load()
}
