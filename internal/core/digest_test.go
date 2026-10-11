package core

import (
	"strings"
	"testing"
	"time"
)

func TestDigestLanguageTimezoneAndDedup(t *testing.T) {
	a := testApp(t)
	c := a.Settings()
	c.Language = "en"
	c.DigestLanguage = "zh-CN"
	c.DigestTimezone = "Asia/Shanghai"
	c.DigestEnabled = true
	if err := a.SaveSettings(c); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 11, 0, 30, 0, 0, time.UTC)
	r := Run{ID: newID(), Status: "completed", Definition: Loop{Name: "Daily test"}, Started: now.Add(-time.Hour)}
	if err := a.saveRun(r); err != nil {
		t.Fatal(err)
	}
	text, err := a.Digest(now)
	if err != nil || !strings.Contains(text, "每日摘要") || !strings.Contains(text, "Daily test — 完成") {
		t.Fatalf("digest: %s %v", text, err)
	}
	if err = a.put("cursor", "digest", now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	a.TickDigest(now)
	var first time.Time
	if err = a.get("cursor", "digest", &first); err != nil {
		t.Fatal(err)
	}
	a.TickDigest(now)
	var second time.Time
	if err = a.get("cursor", "digest", &second); err != nil {
		t.Fatal(err)
	}
	if !first.Equal(second) || !first.After(now) {
		t.Fatal("digest slot was not advanced exactly once")
	}
}
