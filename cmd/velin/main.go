//go:build !windows

package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/google/uuid"
	"velin-webssh/internal/adminreset"
	"velin-webssh/internal/agent"
	"velin-webssh/internal/api"
	"velin-webssh/internal/config"
	"velin-webssh/internal/forward"
	"velin-webssh/internal/security"
	"velin-webssh/internal/store"
	"velin-webssh/internal/tailnet"
	"velin-webssh/internal/terminal"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	s, err := store.Open(cfg.DatabasePath)
	if err != nil {
		log.Fatal(err)
	}
	defer s.Close()
	if argumentPresent("--reset-admin-password") {
		result, err := adminreset.Reset(s, cfg.AdminUser, cfg.AdminPassword)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("管理员密码已重置，旧登录会话已撤销。\nusername=%s\npassword=%s\n", result.Username, result.Password)
		return
	}
	vault, err := security.LoadVault(cfg.MasterKeyPath)
	if err != nil {
		log.Fatal(err)
	}
	count, err := s.UserCount()
	if err != nil {
		log.Fatal(err)
	}
	if count == 0 {
		password := cfg.AdminPassword
		if password == "" {
			password, err = security.RandomToken(12)
			if err != nil {
				log.Fatal(err)
			}
		}
		hash, err := security.HashPassword(password)
		if err != nil {
			log.Fatal(err)
		}
		if err = s.CreateUser(uuid.NewString(), cfg.AdminUser, hash, "admin"); err != nil {
			log.Fatal(err)
		}
		user, _, lookupErr := s.UserByUsername(cfg.AdminUser)
		if lookupErr != nil {
			log.Fatal(lookupErr)
		}
		if err = s.SetForcePasswordChange(user.ID, true); err != nil {
			log.Fatal(err)
		}
		if cfg.AdminPassword == "" {
			credentialPath := filepath.Join(cfg.DataDir, "initial-admin-credentials.txt")
			content := fmt.Sprintf("username=%s\npassword=%s\n", cfg.AdminUser, password)
			if err = os.WriteFile(credentialPath, []byte(content), 0o600); err != nil {
				log.Fatal(err)
			}
			slog.Warn("initial administrator created; change the generated password after login", "username", cfg.AdminUser, "credentials_file", credentialPath)
		} else {
			slog.Warn("initial administrator created; change the configured password after login", "username", cfg.AdminUser)
		}
	}
	tailscaleSettings, err := tailnet.LoadSettings(s, vault)
	if err != nil {
		log.Fatal(err)
	}
	tailscaleManager, err := tailnet.New(cfg)
	if err != nil {
		log.Fatal(err)
	}
	if err = tailscaleManager.Apply(tailscaleSettings); err != nil {
		log.Fatal(err)
	}
	defer tailscaleManager.Close()
	manager := terminal.NewManagerWithFFmpeg(s, vault, cfg.DeploymentID, filepath.Join(cfg.DataDir, "recordings"), cfg.FFmpegBinary, tailscaleManager)
	forwardManager := forward.NewManager(s, manager)
	agentManager := agent.NewManager(manager, agent.AIConfig{BaseURL: cfg.AIBaseURL, APIKey: cfg.AIAPIKey, Model: cfg.AIModel})
	defer agentManager.Close()
	handler := api.NewWithTailnet(cfg, s, vault, manager, forwardManager, agentManager, tailscaleManager).Router()
	server := &http.Server{Addr: cfg.Addr, Handler: handler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1 << 20}
	listener, err := net.Listen("tcp4", cfg.Addr)
	if err != nil {
		log.Fatal(err)
	}
	slog.Info("Velin Web SSH listening", "addr", cfg.Addr, "pid", os.Getpid())
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-stop
		slog.Info("Velin Web SSH shutting down")
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	}()
	if err = server.Serve(listener); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

func argumentPresent(name string) bool {
	for _, argument := range os.Args[1:] {
		if argument == name {
			return true
		}
	}
	return false
}
