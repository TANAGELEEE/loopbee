package core

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (a *App) Digest(now time.Time) (string, error) {
	c := a.Settings()
	loc, err := time.LoadLocation(c.DigestTimezone)
	if err != nil {
		return "", err
	}
	day := now.In(loc).Format("2006-01-02")
	s, err := a.State()
	if err != nil {
		return "", err
	}
	labels := map[string][2]string{"completed": {"完成", "完成"}, "running": {"運行中", "运行中"}, "waiting": {"等待人工", "等待人工"}, "queued": {"排隊中", "排队中"}, "failed": {"失敗", "失败"}, "interrupted": {"中斷", "中断"}, "cancelled": {"已取消", "已取消"}, "rejected": {"已拒絕", "已拒绝"}, "timed_out": {"逾時", "超时"}, "budget_exhausted": {"輪次耗盡", "轮次耗尽"}, "stalled": {"無進展", "无进展"}, "blocked": {"已阻擋", "已阻止"}, "no_action": {"無需處理", "无需处理"}}
	title := "Daily digest"
	artifact := "artifacts"
	if c.DigestLanguage == "zh-TW" {
		title = "每日摘要"
		artifact = "產物"
	}
	if c.DigestLanguage == "zh-CN" {
		title = "每日摘要"
		artifact = "产物"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# %s · %s\n\n", title, day)
	count := 0
	for _, r := range s.Runs {
		if r.Started.In(loc).Format("2006-01-02") != day {
			continue
		}
		count++
		status := r.Status
		if values, ok := labels[status]; ok && c.DigestLanguage != "en" {
			i := 0
			if c.DigestLanguage == "zh-CN" {
				i = 1
			}
			status = values[i]
		}
		fmt.Fprintf(&b, "- %s — %s (%d %s)\n", r.Definition.Name, status, len(r.Artifacts), artifact)
	}
	fmt.Fprintf(&b, "\n%d Runs\n", count)
	return b.String(), nil
}
func (a *App) TickDigest(now time.Time) {
	a.mu.Lock()
	c := a.config
	if !c.DigestEnabled || c.Role != "executor" {
		a.mu.Unlock()
		return
	}
	schedule, err := schedule(Loop{Cron: c.DigestCron, Timezone: c.DigestTimezone})
	if err != nil {
		a.mu.Unlock()
		return
	}
	var next time.Time
	err = a.get("cursor", "digest", &next)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		a.mu.Unlock()
		return
	}
	if next.IsZero() {
		a.put("cursor", "digest", schedule.Next(now))
		a.mu.Unlock()
		return
	}
	if next.After(now) {
		a.mu.Unlock()
		return
	}
	if err = a.put("cursor", "digest", schedule.Next(now)); err != nil {
		a.mu.Unlock()
		return
	}
	a.mu.Unlock()
	text, err := a.Digest(now)
	if err != nil {
		a.event("", "digest.failed", err.Error())
		return
	}
	for _, channel := range c.Channels {
		if channel.Enabled {
			if err = a.enqueue(channel.Kind, text); err != nil {
				a.event("", "digest.failed", err.Error())
			}
		}
	}
}
