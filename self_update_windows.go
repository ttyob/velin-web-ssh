//go:build windows

package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sys/windows"

	"github.com/ttyob/velin-web-ssh/internal/config"
	"github.com/ttyob/velin-web-ssh/internal/version"
)

const (
	windowsUpdateMaxBinarySize = 160 << 20
	windowsUpdateMaxTextSize   = 1 << 20
	windowsUpdateHealthTimeout = 90 * time.Second
)

type windowsUpdateRequest struct {
	RequestID     string `json:"requestID"`
	TargetVersion string `json:"targetVersion"`
	RequestedAt   string `json:"requestedAt"`
}

type windowsUpdateStatus struct {
	Enabled       bool   `json:"enabled"`
	RequestID     string `json:"requestID,omitempty"`
	State         string `json:"state,omitempty"`
	TargetVersion string `json:"targetVersion,omitempty"`
	Message       string `json:"message,omitempty"`
	RequestedAt   string `json:"requestedAt,omitempty"`
	UpdatedAt     string `json:"updatedAt,omitempty"`
}

type windowsRelease struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

func watchWindowsSelfUpdate(cfg config.Config, serviceURL string, launcherPID int, mode string, minimized bool) {
	stateDir := filepath.Join(cfg.DataDir, "self-update")
	requestPath := filepath.Join(stateDir, "request.json")
	processingPath := filepath.Join(stateDir, "request.processing")
	statusPath := filepath.Join(stateDir, "status.json")

	// A request left in processing means the previous worker exited before it
	// handed control to the detached updater helper.
	if content, err := os.ReadFile(processingPath); err == nil {
		var request windowsUpdateRequest
		_ = json.Unmarshal(content, &request)
		_ = writeWindowsUpdateStatus(statusPath, windowsUpdateStatus{
			Enabled: true, RequestID: request.RequestID, State: "failed",
			TargetVersion: request.TargetVersion, Message: "上次更新未能启动，当前版本仍在运行，请重试",
			RequestedAt: request.RequestedAt,
		})
		_ = os.Remove(processingPath)
	}

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for range ticker.C {
		if _, err := os.Stat(requestPath); err != nil {
			continue
		}
		if err := os.Rename(requestPath, processingPath); err != nil {
			continue
		}
		content, err := os.ReadFile(processingPath)
		var request windowsUpdateRequest
		if err == nil {
			err = json.Unmarshal(content, &request)
		}
		if err != nil || !validWindowsRequestID(request.RequestID) || !validWindowsReleaseTag(request.TargetVersion) {
			_ = writeWindowsUpdateStatus(statusPath, windowsUpdateStatus{Enabled: true, State: "failed", Message: "更新请求无效"})
			_ = os.Remove(processingPath)
			continue
		}
		_ = writeWindowsUpdateStatus(statusPath, windowsStatusFromRequest(request, "pulling", "正在下载并校验 Windows 更新"))
		executable, err := os.Executable()
		if err != nil {
			_ = writeWindowsUpdateStatus(statusPath, windowsStatusFromRequest(request, "failed", "无法定位当前程序文件"))
			_ = os.Remove(processingPath)
			continue
		}
		if err = testDirectoryWritable(filepath.Dir(executable)); err != nil {
			_ = writeWindowsUpdateStatus(statusPath, windowsStatusFromRequest(request, "failed", "程序目录不可写，请将程序放到当前用户可写的目录"))
			_ = os.Remove(processingPath)
			continue
		}
		stagedBinary, err := downloadWindowsRelease(request.TargetVersion)
		if err != nil {
			_ = writeWindowsUpdateStatus(statusPath, windowsStatusFromRequest(request, "failed", "下载或校验更新失败："+err.Error()))
			_ = os.Remove(processingPath)
			continue
		}
		helper, err := stageWindowsUpdateHelper(executable)
		if err != nil {
			_ = writeWindowsUpdateStatus(statusPath, windowsStatusFromRequest(request, "failed", "无法准备更新助手"))
			_ = os.Remove(stagedBinary)
			_ = os.Remove(processingPath)
			continue
		}
		restartAddr := cfg.Addr
		if host, port, splitErr := net.SplitHostPort(cfg.Addr); splitErr == nil && port == "0" {
			var parsed *url.URL
			parsed, err = url.Parse(serviceURL)
			if err == nil {
				_, actualPort, portErr := net.SplitHostPort(parsed.Host)
				if portErr != nil {
					err = portErr
				} else {
					restartAddr = net.JoinHostPort(host, actualPort)
				}
			}
		}
		if err != nil || restartAddr == "" {
			_ = writeWindowsUpdateStatus(statusPath, windowsStatusFromRequest(request, "failed", "无法确定服务重启地址"))
			_ = os.Remove(helper)
			_ = os.Remove(stagedBinary)
			_ = os.Remove(processingPath)
			continue
		}

		_ = writeWindowsUpdateStatus(statusPath, windowsStatusFromRequest(request, "restarting", "更新文件已校验，正在重启 Windows 服务"))
		if err = os.Remove(processingPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			_ = writeWindowsUpdateStatus(statusPath, windowsStatusFromRequest(request, "failed", "无法交接更新任务"))
			_ = os.Remove(helper)
			_ = os.Remove(stagedBinary)
			continue
		}
		args := []string{
			"--apply-self-update", executable, stagedBinary, cfg.DataDir,
			request.RequestID, request.TargetVersion, request.RequestedAt, strconv.Itoa(launcherPID),
			strconv.Itoa(os.Getpid()), mode, restartAddr, serviceURL,
			strconv.FormatBool(minimized),
		}
		cmd := exec.Command(helper, args...)
		cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x00000008 | 0x00000200}
		if err = cmd.Start(); err != nil {
			_ = writeWindowsUpdateStatus(statusPath, windowsStatusFromRequest(request, "failed", "无法启动更新助手"))
			_ = os.Remove(helper)
			_ = os.Remove(stagedBinary)
			continue
		}
		_ = cmd.Process.Release()
		return
	}
}

func validWindowsReleaseTag(tag string) bool {
	if len(tag) < 6 || len(tag) > 129 || tag[0] != 'v' {
		return false
	}
	for _, char := range tag {
		if !(char >= '0' && char <= '9' || char >= 'A' && char <= 'Z' || char >= 'a' && char <= 'z' || char == '.' || char == '-') {
			return false
		}
	}
	versionParts := strings.SplitN(tag[1:], "-", 2)
	core := strings.Split(versionParts[0], ".")
	if len(core) != 3 {
		return false
	}
	for _, part := range core {
		if part == "" {
			return false
		}
		if _, err := strconv.Atoi(part); err != nil {
			return false
		}
	}
	return len(versionParts) == 1 || versionParts[1] != ""
}

func validWindowsRequestID(requestID string) bool {
	parsed, err := uuid.Parse(requestID)
	return err == nil && parsed.String() == strings.ToLower(requestID)
}

func windowsStatusFromRequest(request windowsUpdateRequest, state, message string) windowsUpdateStatus {
	return windowsUpdateStatus{
		Enabled: true, RequestID: request.RequestID, State: state,
		TargetVersion: request.TargetVersion, Message: message,
		RequestedAt: request.RequestedAt,
	}
}

func writeWindowsUpdateStatus(path string, status windowsUpdateStatus) error {
	status.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	content, err := json.Marshal(status)
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".status-*.json")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	if _, err = temp.Write(content); err == nil {
		err = temp.Sync()
	}
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = replaceWindowsFile(tempPath, path)
	}
	if err != nil {
		_ = os.Remove(tempPath)
	}
	return err
}

func downloadWindowsRelease(tag string) (string, error) {
	if !validWindowsReleaseTag(tag) {
		return "", errors.New("发布版本号无效")
	}
	client := windowsUpdateHTTPClient()
	endpoint := "https://api.github.com/repos/" + version.Repository + "/releases/tags/" + url.PathEscape(tag)
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "JeexTerminal/"+version.Current)
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub 返回 HTTP %d", response.StatusCode)
	}
	var release windowsRelease
	if err := json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&release); err != nil {
		return "", fmt.Errorf("读取 Release 信息失败: %w", err)
	}
	if release.TagName != tag {
		return "", errors.New("Release 版本与更新目标不一致")
	}
	executableName := "velin-web-ssh-windows-gui-amd64-" + tag + ".exe"
	assetURLs := make(map[string]string, 2)
	for _, asset := range release.Assets {
		if asset.Name == executableName || asset.Name == "checksums.txt" {
			if !validWindowsAssetURL(asset.BrowserDownloadURL, tag) {
				return "", fmt.Errorf("Release 文件地址无效: %s", asset.Name)
			}
			assetURLs[asset.Name] = asset.BrowserDownloadURL
		}
	}
	checksumURL, hasChecksum := assetURLs["checksums.txt"]
	binaryURL, hasBinary := assetURLs[executableName]
	if !hasChecksum || !hasBinary {
		return "", errors.New("Release 缺少 Windows 安装文件或校验文件")
	}
	checksums, err := readWindowsAsset(client, checksumURL, windowsUpdateMaxTextSize)
	if err != nil {
		return "", fmt.Errorf("下载校验文件失败: %w", err)
	}
	expectedHash, err := checksumForAsset(checksums, executableName)
	if err != nil {
		return "", err
	}
	file, err := os.CreateTemp(os.TempDir(), "velin-web-ssh-update-*.exe")
	if err != nil {
		return "", fmt.Errorf("创建更新临时文件失败: %w", err)
	}
	path := file.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(path)
		}
	}()
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("创建更新临时文件失败: %w", err)
	}
	actualHash, err := downloadWindowsAssetToFile(client, binaryURL, path, windowsUpdateMaxBinarySize)
	if err != nil {
		return "", fmt.Errorf("下载 Windows 程序失败: %w", err)
	}
	if !strings.EqualFold(actualHash, expectedHash) {
		return "", errors.New("Windows 程序校验失败，已取消更新")
	}
	cleanup = false
	return path, nil
}

func windowsUpdateHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 3 * time.Minute,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= 8 || request.URL.Scheme != "https" {
				return errors.New("Release 下载重定向无效")
			}
			host := strings.ToLower(request.URL.Hostname())
			if host != "github.com" && host != "api.github.com" && host != "release-assets.githubusercontent.com" && host != "objects.githubusercontent.com" && !strings.HasSuffix(host, ".githubusercontent.com") {
				return errors.New("Release 下载重定向到不受信任的主机")
			}
			return nil
		},
	}
}

func validWindowsAssetURL(raw, tag string) bool {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || !strings.EqualFold(parsed.Hostname(), "github.com") {
		return false
	}
	prefix := "/" + version.Repository + "/releases/download/" + tag + "/"
	return strings.HasPrefix(parsed.EscapedPath(), prefix)
}

func readWindowsAsset(client *http.Client, assetURL string, maxSize int64) ([]byte, error) {
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, assetURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "JeexTerminal/"+version.Current)
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", response.StatusCode)
	}
	if response.ContentLength > maxSize {
		return nil, errors.New("Release 文件超过大小限制")
	}
	content, err := io.ReadAll(io.LimitReader(response.Body, maxSize+1))
	if err != nil {
		return nil, err
	}
	if int64(len(content)) > maxSize {
		return nil, errors.New("Release 文件超过大小限制")
	}
	return content, nil
}

func downloadWindowsAssetToFile(client *http.Client, assetURL, destination string, maxSize int64) (string, error) {
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, assetURL, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("User-Agent", "JeexTerminal/"+version.Current)
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", response.StatusCode)
	}
	if response.ContentLength > maxSize {
		return "", errors.New("Release 文件超过大小限制")
	}
	file, err := os.OpenFile(destination, os.O_WRONLY|os.O_TRUNC, 0o700)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	count, copyErr := io.Copy(io.MultiWriter(file, hash), io.LimitReader(response.Body, maxSize+1))
	if copyErr == nil && count > maxSize {
		copyErr = errors.New("Release 文件超过大小限制")
	}
	if copyErr == nil {
		copyErr = file.Sync()
	}
	if closeErr := file.Close(); copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		return "", copyErr
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func checksumForAsset(checksums []byte, name string) (string, error) {
	scanner := bufio.NewScanner(strings.NewReader(string(checksums)))
	scanner.Buffer(make([]byte, 1024), windowsUpdateMaxTextSize)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 || strings.TrimPrefix(fields[1], "*") != name {
			continue
		}
		if len(fields[0]) != sha256.Size*2 {
			return "", errors.New("Release 校验值格式无效")
		}
		if _, err := hex.DecodeString(fields[0]); err != nil {
			return "", errors.New("Release 校验值格式无效")
		}
		return fields[0], nil
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("校验文件中没有 %s", name)
}

func stageWindowsUpdateHelper(executable string) (string, error) {
	helper, err := os.CreateTemp(os.TempDir(), "velin-web-ssh-update-helper-*.exe")
	if err != nil {
		return "", err
	}
	path := helper.Name()
	if err = helper.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	if err = copyWindowsFile(executable, path); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}

func testDirectoryWritable(directory string) error {
	file, err := os.CreateTemp(directory, ".velin-update-write-test-*")
	if err != nil {
		return err
	}
	name := file.Name()
	if err := file.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	return os.Remove(name)
}

func runWindowsUpdateHelper(args []string) error {
	if len(args) != 12 {
		return errors.New("更新助手参数无效")
	}
	executable, stagedBinary, dataDir := args[0], args[1], args[2]
	request := windowsUpdateRequest{RequestID: args[3], TargetVersion: args[4], RequestedAt: args[5]}
	launcherPID, _ := strconv.Atoi(args[6])
	workerPID, _ := strconv.Atoi(args[7])
	mode, restartAddr, healthURL := args[8], args[9], args[10]
	minimized, _ := strconv.ParseBool(args[11])
	statusPath := filepath.Join(dataDir, "self-update", "status.json")
	fail := func(message string, cause error) error {
		_ = writeWindowsUpdateStatus(statusPath, windowsStatusFromRequest(request, "failed", message))
		if cause != nil {
			return fmt.Errorf("%s: %w", message, cause)
		}
		return errors.New(message)
	}
	if !validWindowsRequestID(request.RequestID) || !validWindowsReleaseTag(request.TargetVersion) || launcherPID < 1 || workerPID < 1 || mode != "service" && mode != "window" {
		return fail("更新助手参数无效", nil)
	}
	if !filepath.IsAbs(executable) || !filepath.IsAbs(stagedBinary) || !filepath.IsAbs(dataDir) {
		return fail("更新路径无效", nil)
	}
	backup := executable + ".velin-update-" + request.RequestID + ".bak"
	candidate := executable + ".velin-update-" + request.RequestID + ".new"
	_ = os.Remove(backup)
	_ = os.Remove(candidate)
	if err := copyWindowsFile(executable, backup); err != nil {
		return fail("无法备份当前程序，更新已取消", err)
	}
	if err := copyWindowsFile(stagedBinary, candidate); err != nil {
		_ = os.Remove(backup)
		return fail("无法准备新程序，更新已取消", err)
	}
	_ = writeWindowsUpdateStatus(statusPath, windowsStatusFromRequest(request, "restarting", "正在关闭 Jeex Terminal 并替换程序"))
	if err := stopWindowsProcess(launcherPID); err != nil {
		_ = os.Remove(candidate)
		_ = os.Remove(backup)
		return fail("无法关闭 Windows 管理窗口，更新已取消", err)
	}
	if err := stopWindowsProcess(workerPID); err != nil {
		_ = os.Remove(candidate)
		_ = os.Remove(backup)
		if _, restartErr := startWindowsLauncher(executable, mode, minimized, restartAddr); restartErr != nil {
			return fail("旧服务未能停止，重新打开管理窗口失败", restartErr)
		}
		return fail("无法停止旧服务，更新已取消", err)
	}
	if err := replaceWindowsFile(candidate, executable); err != nil {
		if restoreErr := restoreWindowsExecutable(backup, executable); restoreErr != nil {
			return fail("替换程序失败，旧版本备份保留在 "+backup, restoreErr)
		}
		oldPID, restartErr := startWindowsLauncher(executable, mode, minimized, restartAddr)
		if restartErr == nil && waitWindowsService(healthURL, windowsUpdateHealthTimeout) {
			_ = os.Remove(backup)
			_ = os.Remove(stagedBinary)
			return fail("替换程序失败，已恢复并启动旧版本", err)
		}
		if oldPID > 0 {
			_ = stopWindowsProcessTree(oldPID)
		}
		return fail("替换程序失败，旧版本文件已恢复，但服务未能启动", restartErr)
	}
	newPID, err := startWindowsLauncher(executable, mode, minimized, restartAddr)
	if err != nil || !waitWindowsService(healthURL, windowsUpdateHealthTimeout) {
		if newPID > 0 {
			_ = stopWindowsProcessTree(newPID)
		}
		if restoreErr := restoreWindowsExecutable(backup, executable); restoreErr != nil {
			return fail("新版本启动失败，旧程序备份保留在 "+backup, restoreErr)
		}
		oldPID, startErr := startWindowsLauncher(executable, mode, minimized, restartAddr)
		if startErr == nil && waitWindowsService(healthURL, windowsUpdateHealthTimeout) {
			_ = os.Remove(backup)
			_ = os.Remove(stagedBinary)
			return fail("新版本启动失败，已恢复并启动旧版本", err)
		}
		if startErr == nil && oldPID > 0 {
			_ = stopWindowsProcess(oldPID)
		}
		return fail("新版本启动失败，旧版本文件已恢复，但服务未通过健康检查", startErr)
	}
	_ = writeWindowsUpdateStatus(statusPath, windowsStatusFromRequest(request, "succeeded", "已更新到 "+request.TargetVersion))
	_ = os.Remove(backup)
	_ = os.Remove(stagedBinary)
	return nil
}

func copyWindowsFile(source, destination string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o700)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	if copyErr == nil {
		copyErr = out.Sync()
	}
	if closeErr := out.Close(); copyErr == nil {
		copyErr = closeErr
	}
	return copyErr
}

func replaceWindowsFile(source, destination string) error {
	if err := os.Rename(source, destination); err == nil {
		return nil
	}
	if err := os.Remove(destination); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Rename(source, destination)
}

func restoreWindowsExecutable(backup, executable string) error {
	if _, err := os.Stat(backup); err != nil {
		return err
	}
	return replaceWindowsFile(backup, executable)
}

func stopWindowsProcess(pid int) error {
	if pid < 1 || windowsProcessStopped(pid) {
		return nil
	}
	command := exec.Command("taskkill.exe", "/PID", strconv.Itoa(pid), "/F")
	_ = command.Run()
	return waitWindowsProcessStopped(pid)
}

func stopWindowsProcessTree(pid int) error {
	if pid < 1 {
		return nil
	}
	command := exec.Command("taskkill.exe", "/PID", strconv.Itoa(pid), "/T", "/F")
	_ = command.Run()
	return waitWindowsProcessStopped(pid)
}

func waitWindowsProcessStopped(pid int) error {
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if windowsProcessStopped(pid) {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("进程 %d 未能退出", pid)
}

func windowsProcessStopped(pid int) bool {
	process, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return errors.Is(err, windows.ERROR_INVALID_PARAMETER)
	}
	defer windows.CloseHandle(process)
	result, err := windows.WaitForSingleObject(process, 0)
	return err == nil && result == windows.WAIT_OBJECT_0
}

func startWindowsLauncher(executable, mode string, minimized bool, addr string) (int, error) {
	args := []string{"--" + mode}
	if minimized {
		args = append(args, "--minimized")
	}
	command := exec.Command(executable, args...)
	command.Dir = filepath.Dir(executable)
	command.Env = withEnvironment(os.Environ(), "VELIN_ADDR", addr)
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x00000008 | 0x00000200}
	if err := command.Start(); err != nil {
		return 0, err
	}
	pid := command.Process.Pid
	return pid, command.Process.Release()
}

func waitWindowsService(serviceURL string, timeout time.Duration) bool {
	client := &http.Client{Timeout: 2 * time.Second}
	endpoint := strings.TrimRight(serviceURL, "/") + "/api/health/ready"
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		response, err := client.Get(endpoint)
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return true
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return false
}

func cleanupWindowsUpdateHelpers() {
	go func() {
		pattern := filepath.Join(os.TempDir(), "velin-web-ssh-update-helper-*.exe")
		for attempt := 0; attempt < 12; attempt++ {
			time.Sleep(30 * time.Second)
			files, _ := filepath.Glob(pattern)
			for _, file := range files {
				_ = os.Remove(file)
			}
		}
	}()
}
