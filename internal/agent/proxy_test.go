package agent

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/armon/go-socks5"
)

func TestAIProxyValidation(t *testing.T) {
	for _, value := range []string{"", "http://localhost:7890", "https://user:password@proxy.example:443/", "socks5://127.0.0.1:1080", "socks5h://user:password@[::1]:1080"} {
		if _, err := ParseAIProxyURL(value); err != nil {
			t.Fatalf("valid proxy rejected: %s: %v", value, err)
		}
	}
	for _, value := range []string{"localhost:7890", "ftp://localhost:21", "http://", "http://proxy:0", "http://proxy:65536", "http://proxy:abc", "http://proxy/path", "http://proxy?token=secret", "http://proxy#fragment", "socks5:///tmp/socket", strings.Repeat("x", 4097)} {
		if _, err := ParseAIProxyURL(value); err == nil {
			t.Fatalf("invalid proxy accepted: %s", value)
		}
	}
}

func modelProxyResponse(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Path == "/v1/models" {
		fmt.Fprint(w, `{"data":[{"id":"proxy-model"}]}`)
		return
	}
	fmt.Fprint(w, `{"choices":[{"message":{"content":"proxy reply"}}]}`)
}

func exerciseModelRequests(t *testing.T, manager *Manager) {
	t.Helper()
	models, err := manager.Models(context.Background())
	if err != nil || len(models) != 1 || models[0].ID != "proxy-model" {
		t.Fatalf("models=%+v err=%v", models, err)
	}
	history := []ChatMessage{{Role: "user", Content: "hello"}}
	result, err := manager.Chat(context.Background(), history, "test")
	if err != nil || result.Message != "proxy reply" {
		t.Fatalf("chat=%+v err=%v", result, err)
	}
	var delta string
	result, err = manager.ChatStream(context.Background(), history, "test", func(value string) error { delta += value; return nil })
	if err != nil || result.Message != "proxy reply" || delta != "proxy reply" {
		t.Fatalf("stream=%+v delta=%q err=%v", result, delta, err)
	}
}

func TestAIHTTPProxyIsScopedAndCanBeCleared(t *testing.T) {
	defaultTransport := http.DefaultTransport
	var proxied atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxied.Add(1)
		if !r.URL.IsAbs() || r.URL.Host != "model.invalid" {
			t.Errorf("unexpected proxy request: %s", r.URL)
		}
		if r.Header.Get("Proxy-Authorization") != "Basic "+base64.StdEncoding.EncodeToString([]byte("user:password")) || r.Header.Get("Authorization") != "Bearer test-key" {
			t.Error("proxy credentials or model API key missing")
		}
		modelProxyResponse(w, r)
	}))
	defer proxy.Close()
	config := AIConfig{BaseURL: "http://model.invalid/v1", Model: "proxy-model", APIKey: "test-key", ProxyURL: strings.Replace(proxy.URL, "://", "://user:password@", 1)}
	manager := NewManager(nil, config)
	exerciseModelRequests(t, manager)
	if proxied.Load() != 3 {
		t.Fatalf("proxy requests=%d", proxied.Load())
	}
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Proxy-Authorization") != "" {
			t.Error("proxy credentials leaked to direct request")
		}
		modelProxyResponse(w, r)
	}))
	defer other.Close()
	response, err := http.Get(other.URL)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if http.DefaultTransport != defaultTransport || proxied.Load() != 3 {
		t.Fatal("Agent proxy affected unrelated HTTP traffic")
	}
	config.BaseURL, config.ProxyURL = other.URL+"/v1", ""
	manager.UpdateAI(config)
	exerciseModelRequests(t, manager)
	if proxied.Load() != 3 {
		t.Fatal("cleared proxy was still used")
	}
	config.ProxyURL = "ftp://invalid"
	manager.UpdateAI(config)
	if _, err := manager.Models(context.Background()); err == nil {
		t.Fatal("invalid proxy must fail rather than fall back to direct access")
	}
}

func TestAIProxyHTTPSConnect(t *testing.T) {
	model := httptest.NewTLSServer(http.HandlerFunc(modelProxyResponse))
	defer model.Close()
	for _, secureProxy := range []bool{false, true} {
		t.Run(fmt.Sprintf("TLSProxy=%t", secureProxy), func(t *testing.T) {
			var tunnels atomic.Int32
			proxy := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodConnect || r.Host != strings.TrimPrefix(model.URL, "https://") {
					http.Error(w, "unexpected tunnel", http.StatusBadRequest)
					return
				}
				target, err := net.Dial("tcp", r.Host)
				if err != nil {
					t.Error(err)
					return
				}
				defer target.Close()
				client, buffered, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				defer client.Close()
				tunnels.Add(1)
				fmt.Fprint(client, "HTTP/1.1 200 Connection Established\r\n\r\n")
				go io.Copy(target, buffered)
				io.Copy(client, target)
			}))
			if secureProxy {
				proxy.StartTLS()
			} else {
				proxy.Start()
			}
			defer proxy.Close()
			manager := NewManager(nil, AIConfig{BaseURL: model.URL + "/v1", Model: "proxy-model", ProxyURL: proxy.URL})
			roots := x509.NewCertPool()
			roots.AddCert(model.Certificate())
			if secureProxy {
				roots.AddCert(proxy.Certificate())
			}
			manager.aiTransport.TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
			defer manager.aiTransport.CloseIdleConnections()
			exerciseModelRequests(t, manager)
			if tunnels.Load() == 0 {
				t.Fatal("HTTPS requests bypassed proxy tunnel")
			}
		})
	}
}

func TestAISOCKSProxy(t *testing.T) {
	server, err := socks5.New(&socks5.Config{Credentials: socks5.StaticCredentials{"user": "password"}, Logger: log.New(io.Discard, "", 0)})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go server.Serve(listener)
	model := httptest.NewServer(http.HandlerFunc(modelProxyResponse))
	defer model.Close()
	for _, scheme := range []string{"socks5", "socks5h"} {
		t.Run(scheme, func(t *testing.T) {
			manager := NewManager(nil, AIConfig{BaseURL: model.URL + "/v1", Model: "proxy-model", ProxyURL: scheme + "://user:password@" + listener.Addr().String()})
			defer manager.aiTransport.CloseIdleConnections()
			exerciseModelRequests(t, manager)
			config := manager.AIConfig()
			config.ProxyURL = strings.Replace(config.ProxyURL, "user:password@", "user:wrong@", 1)
			manager.UpdateAI(config)
			if _, err := manager.Models(context.Background()); err == nil {
				t.Fatal("SOCKS authentication failure must not fall back to direct access")
			}
		})
	}
}
