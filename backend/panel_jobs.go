package main

// Panel background jobs and relay limiters added for the owner:
//   - per-user speed limiting (the speed_limit_mbps field existed but was
//     never enforced) via a token bucket applied in the relay data paths
//   - XHTTP parity for the per-user concurrent-IP limit (was WS-only)
//   - per-user daily usage history (Tehran days) for the sub-page chart
//   - one-time Telegram alerts to the bot admin when a user's traffic runs
//     out or the subscription expires
//   - a daily automatic backup of the full panel state sent to the admin
//     on Telegram
//   - helpers exposing a user's currently tracked IPs

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

var tehranLoc = loadTehranLoc()

func loadTehranLoc() *time.Location {
	if l, err := time.LoadLocation("Asia/Tehran"); err == nil {
		return l
	}
	return time.FixedZone("IRST", 3*3600+30*60)
}

func tehranToday() string { return time.Now().In(tehranLoc).Format("2006-01-02") }

// ---------------------------------------------------------------------------
// Speed limit: one token bucket per user (uuid), limit cached for 30 s.
// ---------------------------------------------------------------------------
type rateBucket struct {
	mu      sync.Mutex
	rate    float64 // bytes per second, 0 = unlimited
	tokens  float64
	last    time.Time
	checked time.Time
}

var rateBuckets sync.Map // uuid -> *rateBucket

func throttleUser(uuid string, n int) {
	if n <= 0 {
		return
	}
	v, _ := rateBuckets.LoadOrStore(uuid, &rateBucket{})
	b := v.(*rateBucket)
	b.mu.Lock()
	if time.Since(b.checked) > 30*time.Second {
		b.checked = time.Now()
		b.rate = 0
		if u := findUserByConfigUUID(uuid); u != nil && u.SpeedLimitMbps > 0 {
			b.rate = u.SpeedLimitMbps * 1_000_000 / 8
		}
	}
	if b.rate <= 0 {
		b.mu.Unlock()
		return
	}
	now := time.Now()
	if b.last.IsZero() {
		b.last = now
	}
	b.tokens += now.Sub(b.last).Seconds() * b.rate
	b.last = now
	if b.tokens > b.rate {
		b.tokens = b.rate // burst: one second worth
	}
	b.tokens -= float64(n)
	var wait time.Duration
	if b.tokens < 0 {
		wait = time.Duration(-b.tokens / b.rate * float64(time.Second))
		b.tokens = 0
	}
	b.mu.Unlock()
	if wait > 3*time.Second {
		wait = 3 * time.Second
	}
	if wait > 0 {
		time.Sleep(wait)
	}
}

// ---------------------------------------------------------------------------
// IP limit parity for XHTTP + current-IP reporting.
// ---------------------------------------------------------------------------
func ipLimitAllowed(r *http.Request, uuid string) bool {
	u := findUserByConfigUUID(uuid)
	if u == nil {
		return true
	}
	if recordUserIP(uuid, extractRemoteIP(r), userIPLimit(u)) {
		return true
	}
	disableUserForIPOverflow(u)
	return false
}

func userCurrentIPs(uuid string) []string {
	userIPTrackerMu.Lock()
	defer userIPTrackerMu.Unlock()
	now := time.Now()
	out := []string{}
	for ip, last := range userIPTracker[uuid] {
		if now.Sub(last) <= 10*time.Minute {
			out = append(out, ip)
		}
	}
	sort.Strings(out)
	return out
}

// ---------------------------------------------------------------------------
// Daily usage history (Tehran calendar days), sampled from the unified
// traffic counter. First sight of a user only sets the baseline, so a
// chart never starts with a fake lifetime-sized bar.
// ---------------------------------------------------------------------------
func sampleUsageHistory() {
	today := tehranToday()
	cutoff := time.Now().In(tehranLoc).AddDate(0, 0, -31).Format("2006-01-02")
	State.mu.Lock()
	changed := false
	for _, u := range State.Users {
		fresh := u.UsageHistory == nil && u.UsageSampledBytes == 0
		if fresh {
			if u.UsageSampledBytes != u.TrafficUsedBytes {
				u.UsageSampledBytes = u.TrafficUsedBytes
				changed = true
			}
			continue
		}
		delta := u.TrafficUsedBytes - u.UsageSampledBytes
		if delta > 0 {
			if u.UsageHistory == nil {
				u.UsageHistory = map[string]int64{}
			}
			u.UsageHistory[today] += delta
			changed = true
		}
		if u.UsageSampledBytes != u.TrafficUsedBytes {
			u.UsageSampledBytes = u.TrafficUsedBytes
			changed = true
		}
		for d := range u.UsageHistory {
			if d < cutoff {
				delete(u.UsageHistory, d)
				changed = true
			}
		}
	}
	State.mu.Unlock()
	if changed {
		_ = saveState()
	}
}

func usageHistorySeries(hist map[string]int64, days int) []map[string]any {
	out := make([]map[string]any, 0, days)
	now := time.Now().In(tehranLoc)
	for i := days - 1; i >= 0; i-- {
		d := now.AddDate(0, 0, -i).Format("2006-01-02")
		out = append(out, map[string]any{"date": d, "bytes": hist[d]})
	}
	return out
}

// ---------------------------------------------------------------------------
// Admin Telegram alerts: traffic exhausted / subscription expired, once per
// condition key (raising the limit or extending the date re-arms them).
// ---------------------------------------------------------------------------
func primaryAdminChatID() (int64, bool) {
	ids := getBotAdminIDs()
	if len(ids) == 0 {
		return 0, false
	}
	return ids[0], true
}

func sendAdminTelegram(text string) {
	chatID, ok := primaryAdminChatID()
	if !ok {
		return
	}
	State.mu.RLock()
	token := getBotTokenLocked()
	State.mu.RUnlock()
	if token == "" {
		return
	}
	_, _ = BotManager.callAPI(token, "sendMessage", map[string]any{
		"chat_id": chatID, "text": text,
	})
}

func checkAdminAlerts() {
	var texts []string
	State.mu.Lock()
	changed := false
	now := time.Now()
	for _, u := range State.Users {
		if u.Status == "disabled" {
			continue
		}
		if u.TrafficLimitBytes > 0 && u.TrafficUsedBytes >= u.TrafficLimitBytes {
			key := fmt.Sprintf("exh:%d", u.TrafficLimitBytes)
			if u.AlertedExhaustedKey != key {
				u.AlertedExhaustedKey = key
				changed = true
				texts = append(texts, fmt.Sprintf("⚠️ حجم کاربر %s تموم شد.\nمصرف: %s از %s",
					u.Username, formatBytes(u.TrafficUsedBytes), formatBytes(u.TrafficLimitBytes)))
			}
		} else if u.AlertedExhaustedKey != "" {
			u.AlertedExhaustedKey = ""
			changed = true
		}
		expired := false
		if u.ExpireAt != "" {
			if t, err := time.Parse(time.RFC3339, u.ExpireAt); err == nil && now.After(t) {
				expired = true
			}
		}
		if expired {
			key := "exp:" + u.ExpireAt
			if u.AlertedExpiredKey != key {
				u.AlertedExpiredKey = key
				changed = true
				texts = append(texts, fmt.Sprintf("⏰ اشتراک کاربر %s منقضی شد.", u.Username))
			}
		} else if u.AlertedExpiredKey != "" {
			u.AlertedExpiredKey = ""
			changed = true
		}
	}
	State.mu.Unlock()
	if changed {
		_ = saveState()
	}
	for _, t := range texts {
		sendAdminTelegram(t)
	}
}

// ---------------------------------------------------------------------------
// Daily automatic backup: the complete panel state file, sent to the admin
// on Telegram once a day (after 03:00 Tehran).
// ---------------------------------------------------------------------------
func sendTelegramDocument(token string, chatID int64, filename string, data []byte, caption string) error {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if err := mw.WriteField("chat_id", fmt.Sprint(chatID)); err != nil {
		return err
	}
	if err := mw.WriteField("caption", caption); err != nil {
		return err
	}
	fw, err := mw.CreateFormFile("document", filename)
	if err != nil {
		return err
	}
	if _, err := fw.Write(data); err != nil {
		return err
	}
	if err := mw.Close(); err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost,
		"https://api.telegram.org/bot"+token+"/sendDocument", &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("telegram sendDocument: %s", string(body))
	}
	return nil
}

func maybeDailyBackup() {
	today := tehranToday()
	State.mu.RLock()
	last, _ := State.Settings["last_auto_backup_date"].(string)
	State.mu.RUnlock()
	if last == today || time.Now().In(tehranLoc).Hour() < 3 {
		return
	}
	chatID, ok := primaryAdminChatID()
	if !ok {
		return
	}
	State.mu.RLock()
	token := getBotTokenLocked()
	dataDir := State.DataDir
	State.mu.RUnlock()
	if token == "" {
		return
	}
	_ = saveState()
	data, err := os.ReadFile(filepath.Join(dataDir, "parham_state.json"))
	if err != nil || len(data) == 0 {
		return
	}
	caption := "🗄 بکاپ خودکار روزانه Parham Panel — " + today + "\nکل وضعیت پنل (کاربرا، تنظیمات، اینباندها) توی این فایله."
	if err := sendTelegramDocument(token, chatID, "parham-backup-"+today+".json", data, caption); err != nil {
		return
	}
	State.mu.Lock()
	State.Settings["last_auto_backup_date"] = today
	State.mu.Unlock()
	_ = saveState()
}

// ---------------------------------------------------------------------------
// Jobs loop: history sampling + admin alerts + daily backup.
// ---------------------------------------------------------------------------
func panelJobsLoop() {
	time.Sleep(30 * time.Second)
	for {
		sampleUsageHistory()
		checkAdminAlerts()
		maybeDailyBackup()
		time.Sleep(5 * time.Minute)
	}
}
