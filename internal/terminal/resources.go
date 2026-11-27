package terminal

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"
)

const resourceCountersCommand = `LC_ALL=C
if [ -r /proc/stat ]; then
  awk '/^cpu / {total=0; for (i=2; i<=9 && i<=NF; i++) total+=$i; printf "cpu\t%.0f\t%.0f\n", total, $5+$6; exit}' /proc/stat
fi
if [ -r /proc/meminfo ]; then
  awk '/^MemTotal:/ {t=$2} /^MemAvailable:/ {a=$2; available=1} /^MemFree:/ {f=$2} /^Buffers:/ {b=$2} /^Cached:/ {c=$2} END {if (!available) a=f+b+c; if (t>0) printf "memory\t%.0f\t%.0f\n", t, t-a}' /proc/meminfo
fi
df -Pk / 2>/dev/null | awk 'NR==2 && $2>0 {printf "disk\t%s\t%s\n", $2, $3}'
`

type ResourceUsage struct {
	TotalBytes  uint64  `json:"totalBytes"`
	UsedBytes   uint64  `json:"usedBytes"`
	UsedPercent float64 `json:"usedPercent"`
}
type ResourceCPU struct {
	Total uint64 `json:"total"`
	Idle  uint64 `json:"idle"`
}
type ResourceSnapshot struct {
	CPU         *ResourceCPU   `json:"cpu"`
	Memory      *ResourceUsage `json:"memory"`
	Disk        *ResourceUsage `json:"disk"`
	CollectedAt time.Time      `json:"collectedAt"`
}

func parseResources(output string) (ResourceSnapshot, error) {
	result := ResourceSnapshot{CollectedAt: time.Now().UTC()}
	for _, line := range strings.Split(output, "\n") {
		parts := strings.Fields(line)
		if len(parts) != 3 {
			continue
		}
		first, err1 := strconv.ParseUint(parts[1], 10, 64)
		second, err2 := strconv.ParseUint(parts[2], 10, 64)
		if err1 != nil || err2 != nil || first == 0 || second > first {
			continue
		}
		if parts[0] == "cpu" {
			result.CPU = &ResourceCPU{Total: first, Idle: second}
			continue
		}
		if first > ^uint64(0)/1024 {
			continue
		}
		usage := &ResourceUsage{TotalBytes: first * 1024, UsedBytes: second * 1024, UsedPercent: float64(second) / float64(first) * 100}
		switch parts[0] {
		case "memory":
			result.Memory = usage
		case "disk":
			result.Disk = usage
		}
	}
	if result.CPU == nil && result.Memory == nil && result.Disk == nil {
		return result, errors.New("resource counters unavailable")
	}
	return result, nil
}

// Resources reads a private command channel on the existing SSH transport. It
// never writes to the shell, changes its working directory or acquires control.
func (s *Session) Resources(ctx context.Context) (ResourceSnapshot, error) {
	client := s.SSHClient()
	if client == nil {
		return ResourceSnapshot{}, errors.New("terminal connection is not available")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	output, truncated, err := bridgeCommand(ctx, client, resourceCountersCommand)
	if err != nil {
		return ResourceSnapshot{}, err
	}
	if truncated {
		return ResourceSnapshot{}, errors.New("incomplete resource counters")
	}
	return parseResources(string(output))
}
