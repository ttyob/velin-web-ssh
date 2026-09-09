package api

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ttyob/velin-web-ssh/internal/security"
	"github.com/ttyob/velin-web-ssh/internal/store"
)

const maxRecordingUploadBytes int64 = 1 << 30
const maxBackupUploadBytes int64 = 1 << 30

type commandTaskRequest struct {
	UserID string
	TaskID string
}

func (a *API) commandTaskWorker() {
	for request := range a.taskQueue {
		value, err := a.store.CommandTask(request.UserID, request.TaskID)
		if err != nil {
			continue
		}
		started := time.Now().UTC()
		_ = a.store.UpdateCommandTask(request.UserID, request.TaskID, "running", "", "", &started, nil)
		var output []string
		var failures []string
		command := strings.TrimRight(value.Command, "\r\n") + "\r"
		for _, sessionID := range value.SessionIDs {
			session, getErr := a.terminals.Get(request.UserID, sessionID)
			if getErr != nil {
				failures = append(failures, sessionID+": session unavailable")
				continue
			}
			if writeErr := session.WriteTask([]byte(command)); writeErr != nil {
				failures = append(failures, sessionID+": "+writeErr.Error())
				continue
			}
			output = append(output, sessionID+": sent")
		}
		finished := time.Now().UTC()
		status := "completed"
		message := ""
		if len(failures) > 0 {
			status = "failed"
			message = strings.Join(failures, "\n")
		}
		_ = a.store.UpdateCommandTask(request.UserID, request.TaskID, status, strings.Join(output, "\n"), message, &started, &finished)
	}
}

func (a *API) tasks(w http.ResponseWriter, r *http.Request) {
	items, err := a.store.CommandTasks(currentUser(r).ID)
	if err != nil {
		writeError(w, 500, "database_error", "任务读取失败")
		return
	}
	writeJSON(w, 200, nonNil(items))
}

func (a *API) task(w http.ResponseWriter, r *http.Request) {
	item, err := a.store.CommandTask(currentUser(r).ID, chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, 404, "task_not_found", "任务不存在")
		return
	}
	writeJSON(w, 200, item)
}

func (a *API) createTask(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	var input struct {
		Command    string   `json:"command"`
		SessionIDs []string `json:"sessionIDs"`
	}
	if !decode(w, r, &input) {
		return
	}
	input.Command = strings.TrimSpace(input.Command)
	if input.Command == "" || len([]rune(input.Command)) > 16000 {
		writeError(w, 400, "invalid_task", "命令不能为空且不能超过 16000 个字符")
		return
	}
	if len(input.SessionIDs) == 0 || len(input.SessionIDs) > 50 {
		writeError(w, 400, "invalid_task_targets", "请选择 1 到 50 个终端")
		return
	}
	seen := make(map[string]bool, len(input.SessionIDs))
	for _, id := range input.SessionIDs {
		if id == "" || seen[id] {
			writeError(w, 400, "invalid_task_targets", "任务目标无效")
			return
		}
		seen[id] = true
		if _, err := a.terminals.Get(user.ID, id); err != nil {
			writeError(w, 404, "session_not_attached", "任务目标终端未连接")
			return
		}
	}
	item := store.CommandTask{ID: uuid.NewString(), UserID: user.ID, Command: input.Command, SessionIDs: input.SessionIDs, Status: "queued", CreatedAt: time.Now().UTC()}
	if err := a.store.CreateCommandTask(item); err != nil {
		writeError(w, 500, "database_error", "任务创建失败")
		return
	}
	a.taskQueue <- commandTaskRequest{UserID: user.ID, TaskID: item.ID}
	writeJSON(w, 201, item)
}

func (a *API) recordings(w http.ResponseWriter, r *http.Request) {
	items, err := a.store.Recordings(currentUser(r).ID)
	if err != nil {
		writeError(w, 500, "database_error", "录制记录读取失败")
		return
	}
	for i := range items {
		items[i].Path = ""
	}
	writeJSON(w, 200, nonNil(items))
}

func (a *API) startRecording(w http.ResponseWriter, r *http.Request) {
	value, err := a.terminals.StartRecording(currentUser(r).ID, chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, 409, "recording_start_failed", err.Error())
		return
	}
	writeJSON(w, 201, value)
}

func (a *API) stopRecording(w http.ResponseWriter, r *http.Request) {
	userID, sessionID := currentUser(r).ID, chi.URLParam(r, "id")
	value, err := a.terminals.StopRecording(userID, sessionID)
	if err != nil {
		writeError(w, 404, "recording_not_found", "会话不存在")
		return
	}
	if value.ID == "" {
		writeError(w, 409, "recording_not_active", "当前没有进行中的录制")
		return
	}
	writeJSON(w, 200, value)
}

func (a *API) uploadRecording(w http.ResponseWriter, r *http.Request) {
	if r.ContentLength > maxRecordingUploadBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "recording_too_large", "录制文件不能超过 1 GB")
		return
	}
	value, err := a.terminals.UploadRecording(
		r.Context(),
		currentUser(r).ID,
		chi.URLParam(r, "id"),
		http.MaxBytesReader(w, r.Body, maxRecordingUploadBytes+1),
	)
	if err != nil {
		message := err.Error()
		status := http.StatusConflict
		if strings.Contains(message, "MP4 转码失败") || strings.Contains(message, "ffmpeg") {
			status = http.StatusInternalServerError
		}
		writeError(w, status, "recording_upload_failed", message)
		return
	}
	writeJSON(w, http.StatusOK, value)
}

func (a *API) downloadRecording(w http.ResponseWriter, r *http.Request) {
	value, err := a.store.Recording(currentUser(r).ID, chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, 404, "recording_not_found", "录制不存在")
		return
	}
	if value.Status == "recording" {
		writeError(w, 409, "recording_active", "请先停止录制")
		return
	}
	name := value.SessionName + "-" + value.ID + ".mp4"
	contentType := "video/mp4"
	if strings.HasSuffix(strings.ToLower(value.Path), ".cast") {
		name = value.SessionName + "-" + value.ID + ".cast"
		contentType = "application/octet-stream"
	}
	servePrivateFile(w, r, value.Path, name, contentType)
}

func safeBackupFile(dataDir, name string) (string, error) {
	name = filepath.Base(name)
	if !strings.HasPrefix(name, "velin-") || !strings.HasSuffix(name, ".db.enc") {
		return "", errors.New("invalid backup name")
	}
	path := filepath.Join(dataDir, name)
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return "", errors.New("backup not found")
	}
	return path, nil
}

func servePrivateFile(w http.ResponseWriter, r *http.Request, path, name, contentType string) {
	file, err := os.Open(path)
	if err != nil {
		writeError(w, 404, "file_not_found", "文件不存在")
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		writeError(w, 404, "file_not_found", "文件不存在")
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+strings.ReplaceAll(filepath.Base(name), `"`, "")+`"`)
	http.ServeContent(w, r, filepath.Base(name), info.ModTime(), file)
}

func (a *API) downloadBackup(w http.ResponseWriter, r *http.Request) {
	path, err := safeBackupFile(a.cfg.DataDir, chi.URLParam(r, "file"))
	if err != nil {
		writeError(w, 404, "backup_not_found", "备份不存在")
		return
	}
	servePrivateFile(w, r, path, filepath.Base(path), "application/octet-stream")
}

func (a *API) uploadBackup(w http.ResponseWriter, r *http.Request) {
	a.backupMu.Lock()
	defer a.backupMu.Unlock()
	if r.ContentLength > maxBackupUploadBytes+2<<20 {
		writeError(w, http.StatusRequestEntityTooLarge, "backup_too_large", "备份文件不能超过 1 GB")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBackupUploadBytes+2<<20)
	if err := r.ParseMultipartForm(2 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_backup_upload", "上传的备份文件无效")
		return
	}
	key := r.FormValue("key")
	if err := security.ValidateBackupKey(key); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_backup_key", "备份密钥至少 12 个字符且不能超过 256 个字符")
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "backup_file_required", "请选择加密备份文件")
		return
	}
	defer file.Close()
	upload, err := os.CreateTemp(a.cfg.DataDir, ".velin-upload-*.db.enc")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "backup_upload_failed", "无法创建上传临时文件")
		return
	}
	uploadPath := upload.Name()
	defer os.Remove(uploadPath)
	written, copyErr := io.Copy(upload, io.LimitReader(file, maxBackupUploadBytes+1))
	closeErr := upload.Close()
	if copyErr != nil || closeErr != nil {
		writeError(w, http.StatusBadRequest, "backup_upload_failed", "读取备份文件失败")
		return
	}
	if written > maxBackupUploadBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "backup_too_large", "备份文件不能超过 1 GB")
		return
	}
	verifyDB, err := os.CreateTemp(a.cfg.DataDir, ".velin-upload-verify-*.db")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "backup_upload_failed", "无法验证备份文件")
		return
	}
	verifyDBPath := verifyDB.Name()
	_ = verifyDB.Close()
	defer os.Remove(verifyDBPath)
	verifyMaster, err := os.CreateTemp(a.cfg.DataDir, ".velin-upload-verify-master-*")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "backup_upload_failed", "无法验证备份文件")
		return
	}
	verifyMasterPath := verifyMaster.Name()
	_ = verifyMaster.Close()
	defer os.Remove(verifyMasterPath)
	if err = security.DecryptBackupBundle(uploadPath, verifyDBPath, verifyMasterPath, key); err != nil {
		writeError(w, http.StatusBadRequest, "backup_decrypt_failed", "备份密钥错误或备份文件已损坏")
		return
	}
	if err = store.VerifyBackup(verifyDBPath); err != nil {
		writeError(w, http.StatusBadRequest, "backup_invalid", "备份完整性校验失败")
		return
	}
	name := "velin-upload-" + time.Now().UTC().Format("20060102-150405") + "-" + uuid.NewString()[:8] + ".db.enc"
	path := filepath.Join(a.cfg.DataDir, name)
	if err = os.Rename(uploadPath, path); err != nil {
		writeError(w, http.StatusInternalServerError, "backup_upload_failed", "无法保存上传备份")
		return
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		_ = os.Remove(path)
		writeError(w, http.StatusInternalServerError, "backup_upload_failed", "无法读取上传备份")
		return
	}
	sum := sha256.Sum256(raw)
	checksum := hex.EncodeToString(sum[:])
	_ = os.WriteFile(path+".sha256", []byte(checksum+"  "+name+"\n"), 0o600)
	writeJSON(w, http.StatusCreated, map[string]any{"file": name, "sha256": checksum, "verified": true, "encrypted": true})
}

func (a *API) restoreBackup(w http.ResponseWriter, r *http.Request) {
	a.backupMu.Lock()
	defer a.backupMu.Unlock()
	var input struct {
		Key string `json:"key"`
	}
	if !decode(w, r, &input) {
		return
	}
	if err := security.ValidateBackupKey(input.Key); err != nil {
		writeError(w, 400, "invalid_backup_key", "备份密钥至少 12 个字符且不能超过 256 个字符")
		return
	}
	path, err := safeBackupFile(a.cfg.DataDir, chi.URLParam(r, "file"))
	if err != nil {
		writeError(w, 404, "backup_not_found", "备份不存在")
		return
	}
	temp, err := os.CreateTemp(a.cfg.DataDir, ".velin-restore-*.db")
	if err != nil {
		writeError(w, 500, "restore_failed", "无法创建恢复临时文件")
		return
	}
	tempPath := temp.Name()
	if err = temp.Close(); err != nil {
		_ = os.Remove(tempPath)
		writeError(w, 500, "restore_failed", "无法创建恢复临时文件")
		return
	}
	defer os.Remove(tempPath)
	masterTemp, err := os.CreateTemp(a.cfg.DataDir, ".velin-restore-master-*")
	if err != nil {
		writeError(w, 500, "restore_failed", "无法创建主密钥临时文件")
		return
	}
	masterTempPath := masterTemp.Name()
	if err = masterTemp.Close(); err != nil {
		_ = os.Remove(masterTempPath)
		writeError(w, 500, "restore_failed", "无法创建主密钥临时文件")
		return
	}
	defer os.Remove(masterTempPath)
	if err = security.DecryptBackupBundle(path, tempPath, masterTempPath, input.Key); err != nil {
		writeError(w, 400, "backup_decrypt_failed", "备份密钥错误或备份文件已损坏")
		return
	}
	if err = store.VerifyBackup(tempPath); err != nil {
		writeError(w, 400, "backup_invalid", "备份完整性校验失败")
		return
	}
	oldMasterKey, err := os.ReadFile(a.cfg.MasterKeyPath)
	if err != nil || len(oldMasterKey) != 32 {
		writeError(w, 500, "restore_failed", "当前主密钥不可用，无法安全恢复")
		return
	}
	preRaw, err := os.CreateTemp(a.cfg.DataDir, ".velin-pre-restore-*.db")
	if err != nil {
		writeError(w, 500, "backup_failed", "无法创建恢复前备份")
		return
	}
	preRawPath := preRaw.Name()
	if err = preRaw.Close(); err != nil {
		_ = os.Remove(preRawPath)
		writeError(w, 500, "backup_failed", "无法创建恢复前备份")
		return
	}
	defer os.Remove(preRawPath)
	if err = a.store.Backup(r.Context(), preRawPath); err != nil {
		writeError(w, 500, "backup_failed", "无法创建恢复前备份")
		return
	}
	preName := "velin-pre-restore-" + time.Now().UTC().Format("20060102-150405") + "-" + uuid.NewString()[:8] + ".db.enc"
	pre := filepath.Join(a.cfg.DataDir, preName)
	if err = security.EncryptBackupBundle(preRawPath, a.cfg.MasterKeyPath, pre, input.Key); err != nil {
		_ = os.Remove(pre)
		writeError(w, 500, "backup_encrypt_failed", "无法加密恢复前备份")
		return
	}
	if raw, readErr := os.ReadFile(pre); readErr == nil {
		sum := sha256.Sum256(raw)
		checksum := hex.EncodeToString(sum[:])
		_ = os.WriteFile(pre+".sha256", []byte(checksum+"  "+preName+"\n"), 0o600)
	}
	a.terminals.CloseAll()
	if err = a.store.Restore(r.Context(), tempPath); err != nil {
		_ = os.Remove(pre)
		_ = os.Remove(pre + ".sha256")
		writeError(w, 500, "restore_failed", err.Error())
		return
	}
	if err = os.Rename(masterTempPath, a.cfg.MasterKeyPath); err != nil {
		_ = a.store.Restore(r.Context(), preRawPath)
		writeError(w, 500, "restore_failed", "数据库已恢复，但主密钥替换失败")
		return
	}
	if err = a.vault.Reload(a.cfg.MasterKeyPath); err != nil {
		_ = a.store.Restore(r.Context(), preRawPath)
		_ = os.WriteFile(a.cfg.MasterKeyPath, oldMasterKey, 0o600)
		_ = a.vault.Reload(a.cfg.MasterKeyPath)
		writeError(w, 500, "restore_failed", "恢复后的主密钥无法加载")
		return
	}
	a.restoreAIModelConfig()
	writeJSON(w, 200, map[string]any{"ok": true, "preRestore": preName, "requiresLogin": true})
}
