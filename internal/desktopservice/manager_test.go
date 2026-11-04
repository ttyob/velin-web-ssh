package desktopservice

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestWorkerProcess(t *testing.T) {
	mode := os.Getenv("VELIN_MANAGER_TEST_WORKER")
	if mode == "" {
		return
	}
	if mode == "fail" {
		_ = json.NewEncoder(os.Stdout).Encode(Ready{Error: "configured port is already in use"})
		os.Exit(1)
	}
	if mode == "hang" {
		time.Sleep(30 * time.Second)
		os.Exit(0)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	go http.Serve(listener, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "ready") }))
	_ = json.NewEncoder(os.Stdout).Encode(Ready{URL: "http://" + listener.Addr().String() + "/", Credentials: &Credentials{Username: "admin", Password: "temporary-test-password"}})
	if mode == "crash" {
		time.Sleep(200 * time.Millisecond)
		os.Exit(2)
	}
	if mode == "ignore-stop" {
		time.Sleep(30 * time.Second)
		os.Exit(0)
	}
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		if scanner.Text() == "stop" {
			break
		}
	}
	_ = listener.Close()
	os.Exit(0)
}

func testManager(t *testing.T, workerMode string) *Manager {
	t.Helper()
	m := New(Options{Command: func(string) *exec.Cmd {
		cmd := exec.Command(os.Args[0], "-test.run=^TestWorkerProcess$")
		cmd.Env = append(os.Environ(), "VELIN_MANAGER_TEST_WORKER="+workerMode)
		return cmd
	}, StartTimeout: 3 * time.Second, StopTimeout: 100 * time.Millisecond})
	t.Cleanup(m.Close)
	return m
}

func TestManagerLifecycle(t *testing.T) {
	m := testManager(t, "normal")
	if _, err := m.Start("invalid"); err == nil {
		t.Fatal("invalid mode was accepted")
	}
	for attempt := 0; attempt < 2; attempt++ {
		status, err := m.Start("service")
		if err != nil || status.State != "running" {
			t.Fatalf("start: %v, %+v", err, status)
		}
		response, err := http.Get(status.URL)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 200 {
			t.Fatal("worker is not accepting requests")
		}
		if same, err := m.Start("service"); err != nil || same.URL != status.URL {
			t.Fatal("duplicate start created another worker")
		}
		if _, err := m.Start("window"); err == nil {
			t.Fatal("live mode changed without stopping")
		}
		if credentials := m.Credentials(); credentials == nil || credentials.Username != "admin" {
			t.Fatal("initial credentials were not received")
		}
		m.ClearCredentials()
		if m.Credentials() != nil {
			t.Fatal("cleared credentials remain accessible")
		}
		if stopped, err := m.Stop(); err != nil || stopped.State != "stopped" || stopped.URL != "" {
			t.Fatalf("stop: %v, %+v", err, stopped)
		}
		client := &http.Client{Timeout: time.Second}
		if response, err := client.Get(status.URL); err == nil {
			response.Body.Close()
			t.Fatal("stopped worker still accepts requests")
		}
	}
	if _, err := m.Start("window"); err != nil {
		t.Fatal(err)
	}
	m.Close()
	if _, err := m.Start("service"); err == nil {
		t.Fatal("closed manager restarted")
	}
}

func TestManagerFailures(t *testing.T) {
	for _, mode := range []string{"fail", "hang", "crash", "ignore-stop"} {
		t.Run(mode, func(t *testing.T) {
			m := testManager(t, mode)
			if mode == "hang" {
				m.options.StartTimeout = 100 * time.Millisecond
			}
			status, err := m.Start("service")
			if mode == "fail" || mode == "hang" {
				if err == nil || status.State != "error" {
					t.Fatalf("failure not reported: %v, %+v", err, status)
				}
				if mode == "fail" && !strings.Contains(err.Error(), "port") {
					t.Fatalf("worker's startup error was lost: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if mode == "crash" {
				deadline := time.Now().Add(3 * time.Second)
				for m.Status().State == "running" && time.Now().Before(deadline) {
					time.Sleep(10 * time.Millisecond)
				}
				if m.Status().State != "error" || m.Status().LastError == "" {
					t.Fatal("unexpected worker exit was not reported")
				}
			}
			if status, err = m.Stop(); err != nil || status.State != "stopped" {
				t.Fatalf("worker did not stop: %v, %+v", err, status)
			}
		})
	}
}
