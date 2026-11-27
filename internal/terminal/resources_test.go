package terminal

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestResourceSnapshotUsesExistingSSH(t *testing.T) {
	if _, err := os.Stat("/proc/stat"); err != nil {
		t.Skip("Linux counters unavailable")
	}
	client := bridgeSSHPeer(t)
	session := &Session{sshClient: client, controller: "browser", gridRows: 24, gridCols: 80}
	value, err := session.Resources(context.Background())
	if err != nil || value.CPU == nil || value.Memory == nil || value.Disk == nil {
		t.Fatalf("snapshot: %+v %v", value, err)
	}
	if value.CPU.Idle > value.CPU.Total || value.Memory.UsedPercent < 0 || value.Memory.UsedPercent > 100 || value.Disk.TotalBytes == 0 {
		t.Fatalf("invalid resources: %+v", value)
	}
	if session.controller != "browser" || session.gridCols != 80 || session.gridRows != 24 {
		t.Fatal("observation changed terminal state")
	}
	out, _, err := bridgeCommand(context.Background(), client, "printf still-connected")
	if err != nil || strings.TrimSpace(string(out)) != "still-connected" {
		t.Fatal("monitoring closed shared SSH transport", err)
	}
}

func TestResourceCountersRejectInvalidValues(t *testing.T) {
	value, err := parseResources("cpu\t1000\t700\nmemory\t8192\t2048\ndisk\t10000\t2500\n")
	if err != nil || value.Memory.UsedPercent != 25 || value.Disk.UsedPercent != 25 || value.CPU.Idle != 700 || value.Memory.TotalBytes != 8192*1024 {
		t.Fatalf("snapshot: %+v %v", value, err)
	}
	value, err = parseResources("cpu\t100\t200\nmemory\t0\t0\ndisk\t10\t5\n")
	if err != nil || value.CPU != nil || value.Memory != nil || value.Disk.UsedPercent != 50 {
		t.Fatal("invalid counters displayed as valid", value, err)
	}
	if _, err = parseResources("memory\tNaN\t10\ndisk\t1\t-1\n"); err == nil {
		t.Fatal("invalid sample accepted")
	}
}
