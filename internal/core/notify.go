package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

func validateChannels(channels []Channel) error {
	seen := map[string]bool{}
	for _, c := range channels {
		if seen[c.Kind] {
			return errors.New("duplicate notification channel")
		}
		seen[c.Kind] = true
		switch c.Kind {
		case "desktop", "telegram", "feishu", "bark":
		default:
			return errors.New("unknown notification channel")
		}
		if c.Enabled && c.Kind != "desktop" && c.Kind != "telegram" {
			endpoint, err := resolveSecret(c.Endpoint)
			if err != nil {
				return err
			}
			u, err := url.Parse(endpoint)
			if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
				return errors.New("notification endpoint must use HTTPS")
			}
		}
		if c.Enabled && (c.Kind == "telegram" || c.Kind == "bark") && c.Secret == "" {
			return errors.New("notification secret is required")
		}
		if c.Enabled && c.Kind == "telegram" && c.Target == "" {
			return errors.New("Telegram chat ID required")
		}
	}
	return nil
}

// runEvent maps a Run status to the notification event a Loop can subscribe to.
func runEvent(status string) string {
	switch status {
	case "completed":
		return "done"
	case "waiting":
		return "attention"
	case "no_action":
		return "no_action"
	case "running", "queued":
		return ""
	}
	return "failed"
}

var statusText = map[string][3]string{
	"completed": {"Done", "完成", "完成"}, "waiting": {"Waiting for your review", "等待你審閱", "等待你审阅"},
	"no_action": {"Nothing to do", "無需處理", "无需处理"}, "failed": {"Failed", "失敗", "失败"},
	"blocked": {"Blocked", "被阻擋", "被阻挡"}, "rejected": {"Sent back", "已退回", "已退回"},
	"cancelled": {"Cancelled", "已取消", "已取消"}, "timed_out": {"Time limit reached", "逾時", "超时"},
	"budget_exhausted": {"Budget used", "預算耗盡", "预算耗尽"}, "stalled": {"No progress", "停滯", "停滞"},
	"interrupted": {"Interrupted", "已中斷", "已中断"},
}

func localized(status, lang string) string {
	v, ok := statusText[status]
	if !ok {
		return status
	}
	switch lang {
	case "zh-TW":
		return v[1]
	case "zh-CN":
		return v[2]
	}
	return v[0]
}

// runNotification composes a self-contained message: the phone has no link back to the local board.
func runNotification(r Run, lang string, includeReport bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s · %s · %s", Brand, r.Definition.Name, localized(r.Status, lang))
	if r.Status == "waiting" && r.Step < len(r.Definition.Steps) {
		fmt.Fprintf(&b, " · %s", r.Definition.Steps[r.Step].Name)
	}
	if r.Issue != nil {
		fmt.Fprintf(&b, "\n#%d %s", r.Issue.Number, r.Issue.Title)
	}
	if r.Result != nil && r.Result.Summary != "" {
		b.WriteString("\n" + r.Result.Summary)
	} else if r.Error != "" && r.Status != "completed" {
		b.WriteString("\n" + r.Error)
	}
	if r.Result != nil {
		for _, l := range r.Result.Links {
			b.WriteString("\n" + l)
		}
	}
	if r.Branch != "" && len(r.Commits) > 0 {
		b.WriteString("\n" + r.Branch + " @ " + strings.Join(r.Commits, ", "))
	}
	if includeReport && r.Report != "" {
		report := r.Report
		if i := strings.LastIndex(report, resultMarker); i >= 0 {
			report = strings.TrimSpace(report[:i])
		}
		if len(report) > 3000 {
			report = report[:3000] + "\n…"
		}
		if report != "" {
			b.WriteString("\n\n" + report)
		}
	}
	b.WriteString("\nrun " + r.ID[:min(10, len(r.ID))])
	return b.String()
}

func (a *App) notifyRun(r Run) {
	event := runEvent(r.Status)
	if event == "" {
		return
	}
	rule := r.Definition.Notify
	events := rule.Events
	if len(events) == 0 {
		events = []string{"done", "attention", "failed"}
	}
	if !slices.Contains(events, event) {
		return
	}
	c := a.Settings()
	text := runNotification(r, c.DigestLanguage, rule.IncludeReport)
	for _, ch := range c.Channels {
		if ch.Enabled && (len(rule.Channels) == 0 || slices.Contains(rule.Channels, ch.Kind)) {
			a.enqueue(ch.Kind, text)
		}
	}
}
func (a *App) enqueue(kind, text string) error {
	d := Delivery{ID: newID(), Channel: kind, Text: text, Status: "pending", Updated: time.Now()}
	if err := a.put("delivery", d.ID, d); err != nil {
		return err
	}
	a.wg.Add(1)
	go func() { defer a.wg.Done(); a.deliver(d) }()
	return nil
}
func (a *App) TestNotify(kind string) error {
	if a.Settings().Role == "viewer" {
		return errors.New("viewer is read-only")
	}
	for _, c := range a.Settings().Channels {
		if c.Kind == kind && c.Enabled {
			return a.enqueue(kind, Brand+" notification test")
		}
	}
	return errors.New("channel is not enabled")
}
func (a *App) RetryDelivery(id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.config.Role == "viewer" {
		return errors.New("viewer is read-only")
	}
	var d Delivery
	if err := a.get("delivery", id, &d); err != nil {
		return err
	}
	if d.Status != "failed" && d.Status != "unknown" {
		return errors.New("only failed or unknown deliveries may be retried")
	}
	retry := Delivery{ID: newID(), Channel: d.Channel, Text: d.Text, Status: "pending", Updated: time.Now()}
	d.Status = "retried"
	d.Updated = time.Now()
	tx, err := a.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, entry := range []Delivery{d, retry} {
		data, _ := json.Marshal(entry)
		if _, err = tx.Exec("INSERT INTO objects(kind,id,data) VALUES('delivery',?,?) ON CONFLICT(kind,id) DO UPDATE SET data=excluded.data", entry.ID, string(data)); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	a.wg.Add(1)
	go func() { defer a.wg.Done(); a.deliver(retry) }()
	return nil
}
func (a *App) deliver(d Delivery) {
	var channel Channel
	for _, c := range a.Settings().Channels {
		if c.Kind == d.Channel {
			channel = c
		}
	}
	if !channel.Enabled {
		d.Status = "failed"
		d.Error = "channel disabled"
		a.put("delivery", d.ID, d)
		return
	}
	for attempt := 1; attempt <= 3; attempt++ {
		d.Attempts = attempt
		status, err := sendNotification(a.ctx, channel, d.Text)
		d.Status = status
		d.Updated = time.Now()
		if err != nil {
			d.Error = err.Error()
		} else {
			d.Error = ""
		}
		if a.put("delivery", d.ID, d) != nil {
			return
		}
		if status != "retryable" {
			return
		}
		select {
		case <-a.ctx.Done():
			d.Status = "unknown"
			d.Error = "application stopped"
			a.put("delivery", d.ID, d)
			return
		case <-time.After(time.Duration(attempt) * time.Second):
		}
	}
	d.Status = "failed"
	a.put("delivery", d.ID, d)
}
func sendNotification(ctx context.Context, c Channel, text string) (string, error) {
	var err error
	c.Secret, err = resolveSecret(c.Secret)
	if err != nil {
		return "failed", err
	}
	c.Endpoint, err = resolveSecret(c.Endpoint)
	if err != nil {
		return "failed", err
	}
	if c.Kind == "desktop" {
		return sendDesktopNotification(ctx, text)
	}
	// Keep each UTF-8 payload well below the providers' limits; never split a rune.
	var chunks []string
	var chunk strings.Builder
	for _, r := range text {
		if chunk.Len()+len(string(r)) > 1800 {
			chunks = append(chunks, chunk.String())
			chunk.Reset()
		}
		chunk.WriteRune(r)
	}
	if chunk.Len() > 0 {
		chunks = append(chunks, chunk.String())
	}
	client := http.Client{Timeout: 12 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}
	sent := 0
	for _, part := range chunks {
		endpoint := c.Endpoint
		var payload any
		switch c.Kind {
		case "feishu":
			payload = map[string]any{"msg_type": "text", "content": map[string]string{"text": part}}
		case "telegram":
			endpoint = "https://api.telegram.org/bot" + c.Secret + "/sendMessage"
			payload = map[string]string{"chat_id": c.Target, "text": part}
		case "bark":
			endpoint = strings.TrimRight(endpoint, "/") + "/push"
			payload = map[string]string{"device_key": c.Secret, "title": Brand, "body": part}
		default:
			return "failed", errors.New("unknown channel")
		}
		b, _ := json.Marshal(payload)
		req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(b))
		if err != nil {
			return "failed", errors.New("invalid endpoint")
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			return "unknown", errors.New("delivery result unknown; check destination before retrying")
		}
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()
		if resp.StatusCode == 429 && sent == 0 {
			return "retryable", errors.New("provider rate limited delivery")
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return "failed", fmt.Errorf("provider returned HTTP %d", resp.StatusCode)
		}
		if readErr != nil {
			return "unknown", errors.New("provider response incomplete")
		}
		var result struct {
			OK         bool `json:"ok"`
			Code       int  `json:"code"`
			StatusCode int  `json:"StatusCode"`
		}
		if json.Unmarshal(data, &result) != nil {
			return "unknown", errors.New("unrecognized provider response")
		}
		if (c.Kind == "telegram" && !result.OK) || (c.Kind == "feishu" && (result.Code != 0 || result.StatusCode != 0)) || (c.Kind == "bark" && result.Code != 200) {
			return "failed", errors.New("provider rejected notification")
		}
		sent++
	}
	return "sent", nil
}
