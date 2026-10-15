package core

import (
	"errors"
	"time"
)

func (a *App) ServeBackground() {
	a.mu.Lock()
	if a.config.KeepAwake {
		if err := a.setKeepAwake(true); err != nil {
			a.event("", "system.error", err.Error())
		}
	}
	a.mu.Unlock()
	a.serveTeam()
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		adoptLoginPath()
		a.DetectAgents()
		tick := time.NewTicker(time.Second * 10)
		defer tick.Stop()
		for {
			select {
			case <-a.ctx.Done():
				return
			case now := <-tick.C:
				a.Tick(now)
				a.TickDigest(now)
			}
		}
	}()
}
func (a *App) Tick(now time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.config.Role != "executor" {
		return
	}
	loops, err := a.Loops()
	if err != nil {
		return
	}
	for _, l := range loops {
		if !l.Enabled || l.Cron == "" || l.Next.IsZero() || l.Next.After(now) {
			continue
		}
		s, err := schedule(l)
		if err != nil {
			continue
		}
		due := l.Next
		l.Next = s.Next(now)
		if err = a.put("loop", l.ID, l); err != nil {
			continue
		}
		if a.config.Paused {
			// Paused scheduling skips future triggers without recording them as missed.
			continue
		}
		late := now.Sub(due) > time.Minute
		if late && !l.CatchUp {
			a.event("", "schedule.missed", l.ID+" "+due.Format(time.RFC3339))
			continue
		}
		source := "schedule"
		if late {
			source = "catchup"
		}
		if _, err = a.startLocked(l.ID, source, ""); err != nil {
			a.event("", "schedule.blocked", l.ID+": "+err.Error())
		}
	}
}

// SetPaused pauses or resumes all future scheduled triggers. Active Runs continue.
func (a *App) SetPaused(paused bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.config.Role != "executor" {
		return errors.New("viewer is read-only")
	}
	c := a.config
	c.Paused = paused
	if err := a.put("settings", "general", c); err != nil {
		return err
	}
	a.config = c
	kind := "schedule.resumed"
	if paused {
		kind = "schedule.paused"
	}
	return a.event("", kind, "")
}

// CompleteOnboarding marks first-run setup as done or skipped.
func (a *App) CompleteOnboarding() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	c := a.config
	c.Onboarded = true
	if err := a.put("settings", "general", c); err != nil {
		return err
	}
	a.config = c
	return nil
}
