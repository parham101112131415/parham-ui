package main

// ---------------------------------------------------------------------------
// GitHub backup: the full panel backup (same payload as
// GET /api/settings/backup) kept as ONE file — parham-backup.json — in a
// dedicated GitHub repo, overwritten in place.
//
//   - Automatic: once an hour (jobs loop) when enabled and configured.
//   - Manual: "backup now" button + download in Settings → Backup & Restore.
//   - Import: paste the GitHub link of that file (raw / blob / raw-path
//     forms) and the panel fetches and restores it itself; plain file
//     upload keeps working through /api/settings/restore.
//
// Config lives in Settings (gh_backup_repo / gh_backup_token /
// gh_backup_enabled) and can be overridden by the PARHAM_GH_BACKUP_REPO /
// PARHAM_GH_BACKUP_TOKEN env vars. Push uses the GitHub Contents API:
// GET the current blob sha, skip the commit when the new blob sha matches
// (no noise commits), otherwise PUT the replacement.
// ---------------------------------------------------------------------------

import (
	"bytes"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

const ghBackupFileName = "parham-backup.json"
const ghBackupDefaultRepo = "parham101112131415/parham-panel-backup"

var ghBackupMu sync.Mutex
var ghBackupRunning bool

func ghBackupConfig() (repo, token string, enabled bool) {
	State.mu.RLock()
	repo, _ = State.Settings["gh_backup_repo"].(string)
	token, _ = State.Settings["gh_backup_token"].(string)
	enabled, _ = State.Settings["gh_backup_enabled"].(bool)
	State.mu.RUnlock()
	if v := strings.TrimSpace(os.Getenv("PARHAM_GH_BACKUP_REPO")); v != "" {
		repo = v
	}
	if v := strings.TrimSpace(os.Getenv("PARHAM_GH_BACKUP_TOKEN")); v != "" {
		token = v
	}
	return strings.TrimSpace(repo), strings.TrimSpace(token), enabled
}

// gitBlobSHA is the SHA-1 git would assign to data as a blob object —
// identical to the sha the Contents API reports for the stored file.
func gitBlobSHA(data []byte) string {
	h := sha1.New()
	fmt.Fprintf(h, "blob %d\x00", len(data))
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}

func ghAPIRequest(method, endpoint, token string, body []byte, accept string) (int, []byte, error) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, endpoint, rd)
	if err != nil {
		return 0, nil, err
	}
	if accept == "" {
		accept = "application/vnd.github+json"
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "ParhamPanel")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := &http.Client{Timeout: 45 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024*1024))
	return resp.StatusCode, data, err
}

// ghPushBackup writes data as the single backup file in the repo,
// replacing the previous one. Returns true when a new commit was made,
// false when the stored file already had identical content.
func ghPushBackup(repo, token string, data []byte) (bool, error) {
	endpoint := "https://api.github.com/repos/" + repo + "/contents/" + ghBackupFileName
	status, body, err := ghAPIRequest(http.MethodGet, endpoint, token, nil, "")
	if err != nil {
		return false, fmt.Errorf("github get: %w", err)
	}
	sha := ""
	if status == http.StatusOK {
		var cur struct {
			SHA string `json:"sha"`
		}
		if err := json.Unmarshal(body, &cur); err == nil {
			sha = cur.SHA
		}
	} else if status != http.StatusNotFound {
		return false, fmt.Errorf("github get: HTTP %d: %s", status, ghErrSnippet(body))
	}
	if sha != "" && sha == gitBlobSHA(data) {
		return false, nil
	}
	payload := map[string]any{
		"message": "Parham Panel backup — " + time.Now().In(tehranLoc).Format("2006-01-02 15:04"),
		"content": base64.StdEncoding.EncodeToString(data),
	}
	if sha != "" {
		payload["sha"] = sha
	}
	raw, _ := json.Marshal(payload)
	status, body, err = ghAPIRequest(http.MethodPut, endpoint, token, raw, "")
	if err != nil {
		return false, fmt.Errorf("github put: %w", err)
	}
	if status != http.StatusOK && status != http.StatusCreated {
		return false, fmt.Errorf("github put: HTTP %d: %s", status, ghErrSnippet(body))
	}
	return true, nil
}

func ghErrSnippet(body []byte) string {
	s := strings.TrimSpace(string(body))
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

func recordGHBackupResult(result string) {
	State.mu.Lock()
	State.Settings["gh_backup_last_run_unix"] = time.Now().Unix()
	State.Settings["gh_backup_last_at"] = time.Now().In(tehranLoc).Format("2006-01-02 15:04:05")
	State.Settings["gh_backup_last_result"] = result
	State.mu.Unlock()
	_ = saveState()
}

// runGitHubBackup performs one backup round and records its outcome.
// Returns a short machine status: "updated", "unchanged", or "error".
func runGitHubBackup() (string, error) {
	ghBackupMu.Lock()
	if ghBackupRunning {
		ghBackupMu.Unlock()
		return "error", fmt.Errorf("بکاپ گیت‌هاب در حال انجام است")
	}
	ghBackupRunning = true
	ghBackupMu.Unlock()
	defer func() {
		ghBackupMu.Lock()
		ghBackupRunning = false
		ghBackupMu.Unlock()
	}()

	repo, token, _ := ghBackupConfig()
	if repo == "" || token == "" {
		err := fmt.Errorf("ریپو یا توکن گیت‌هاب تنظیم نشده است")
		recordGHBackupResult("error: " + err.Error())
		return "error", err
	}
	data, err := buildBackupBytes()
	if err != nil {
		recordGHBackupResult("error: " + err.Error())
		return "error", err
	}
	changed, err := ghPushBackup(repo, token, data)
	if err != nil {
		recordGHBackupResult("error: " + err.Error())
		return "error", err
	}
	if changed {
		recordGHBackupResult("ok")
		return "updated", nil
	}
	recordGHBackupResult("ok (بدون تغییر)")
	return "unchanged", nil
}

// maybeGitHubBackup is the jobs-loop hook: one backup per hour while the
// feature is enabled and configured.
func maybeGitHubBackup() {
	_, _, enabled := ghBackupConfig()
	if !enabled {
		return
	}
	repo, token, _ := ghBackupConfig()
	if repo == "" || token == "" {
		return
	}
	State.mu.RLock()
	var last int64
	switch v := State.Settings["gh_backup_last_run_unix"].(type) {
	case int64:
		last = v
	case float64:
		last = int64(v)
	}
	State.mu.RUnlock()
	if time.Now().Unix()-last < 55*60 {
		return
	}
	_, _ = runGitHubBackup()
}

// parseGitHubFileURL understands the link forms GitHub shows for a file:
//   https://raw.githubusercontent.com/{owner}/{repo}/{branch}/{path}
//   https://github.com/{owner}/{repo}/raw/{branch}/{path}
//   https://github.com/{owner}/{repo}/blob/{branch}/{path}
// and returns "owner/repo", branch, and the in-repo file path.
func parseGitHubFileURL(raw string) (repo, branch, path string, ok bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return "", "", "", false
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	switch u.Host {
	case "raw.githubusercontent.com":
		if len(parts) < 4 {
			return "", "", "", false
		}
		return parts[0] + "/" + parts[1], parts[2], strings.Join(parts[3:], "/"), true
	case "github.com":
		if len(parts) < 5 || (parts[2] != "blob" && parts[2] != "raw") {
			return "", "", "", false
		}
		return parts[0] + "/" + parts[1], parts[3], strings.Join(parts[4:], "/"), true
	}
	return "", "", "", false
}

// fetchBackupFromURL downloads a backup file from a pasted link. GitHub
// links go through the Contents API with the raw media type, so private
// repos work with the configured token; any other http(s) link is a plain
// download.
func fetchBackupFromURL(raw string) ([]byte, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("لینک خالی است")
	}
	if repo, branch, path, ok := parseGitHubFileURL(raw); ok {
		_, token, _ := ghBackupConfig()
		endpoint := "https://api.github.com/repos/" + repo + "/contents/" + path
		if branch != "" {
			endpoint += "?ref=" + url.QueryEscape(branch)
		}
		status, body, err := ghAPIRequest(http.MethodGet, endpoint, token, nil, "application/vnd.github.raw+json")
		if err != nil {
			return nil, fmt.Errorf("خطا در گرفتن فایل از گیت‌هاب: %w", err)
		}
		if status != http.StatusOK {
			return nil, fmt.Errorf("گیت‌هاب فایل را نداد (HTTP %d) — اگر ریپو خصوصی است، توکن باید به همان ریپو دسترسی داشته باشد", status)
		}
		return body, nil
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, fmt.Errorf("لینک معتبر نیست")
	}
	req, err := http.NewRequest(http.MethodGet, raw, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "ParhamPanel")
	client := &http.Client{Timeout: 45 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("خطا در دانلود فایل: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("دانلود فایل ناموفق بود (HTTP %d)", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 55*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("خطا در خواندن فایل: %w", err)
	}
	return data, nil
}

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

func ghBackupStatusPayload() map[string]any {
	repo, token, enabled := ghBackupConfig()
	if repo == "" {
		repo = ghBackupDefaultRepo
	}
	masked := ""
	if token != "" {
		if len(token) > 8 {
			masked = token[:4] + "********"
		} else {
			masked = "********"
		}
	}
	State.mu.RLock()
	lastAt, _ := State.Settings["gh_backup_last_at"].(string)
	lastResult, _ := State.Settings["gh_backup_last_result"].(string)
	State.mu.RUnlock()
	return map[string]any{
		"ok":          true,
		"repo":        repo,
		"token_set":   token != "",
		"token_mask":  masked,
		"enabled":     enabled,
		"file":        ghBackupFileName,
		"last_at":     lastAt,
		"last_result": lastResult,
	}
}

func handleGHBackupConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		body := readBody(r)
		repo := strings.TrimSpace(gs(body, "gh_repo"))
		token := strings.TrimSpace(gs(body, "gh_token"))
		enabled := false
		if v, ok := body["gh_enabled"].(bool); ok {
			enabled = v
		}
		if repo != "" {
			parts := strings.Split(repo, "/")
			if len(parts) != 2 || parts[0] == "" || parts[1] == "" || strings.ContainsAny(repo, " \t") {
				writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "detail": "آدرس ریپو باید به شکل owner/repo باشد"})
				return
			}
		}
		State.mu.Lock()
		State.Settings["gh_backup_repo"] = repo
		// An empty or masked token field means "keep the saved token".
		if token != "" && !strings.Contains(token, "********") {
			State.Settings["gh_backup_token"] = token
		}
		State.Settings["gh_backup_enabled"] = enabled
		State.mu.Unlock()
		_ = saveState()
	}
	writeJSON(w, http.StatusOK, ghBackupStatusPayload())
}

func handleGHBackupRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false})
		return
	}
	status, err := runGitHubBackup()
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "detail": err.Error(), "status": ghBackupStatusPayload()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "result": status, "status": ghBackupStatusPayload()})
}

func handleSettingsRestoreURL(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false})
		return
	}
	body := readBody(r)
	link := strings.TrimSpace(gs(body, "url"))
	data, err := fetchBackupFromURL(link)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "detail": err.Error()})
		return
	}
	status, payload := restoreBackupBytes(data)
	writeJSON(w, status, payload)
}
