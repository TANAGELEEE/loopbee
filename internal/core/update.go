package core

import (
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"loopbee/internal/update"
)

// UpdateStatus is what Settings › General shows about loopbee's own version.
//
// loopbee asks GitHub for the newest release shortly after it starts and every six hours (unless the user turned
// automatic updates off), and downloads it at once when this copy can replace itself, so that only a restart is
// left. A restart waits until no Run is active (a Run cut short is marked interrupted and never replayed), for an
// hour at most; quitting puts a downloaded update in too. Builds from source never update themselves.
type UpdateStatus struct {
	// State: idle (never asked), checking, latest, downloading, ready (downloaded; a restart puts it in),
	// available (newer, but this copy cannot replace itself: Stuck says why), source (a build from source),
	// error (Retry: asking again may work).
	State   string    `json:"state"`
	Current string    `json:"current"`
	Latest  string    `json:"latest,omitempty"`
	Notes   string    `json:"notes,omitempty"`
	URL     string    `json:"url,omitempty"`
	Stuck   string    `json:"stuck,omitempty"`
	Error   string    `json:"error,omitempty"`
	Done    int64     `json:"done,omitempty"`
	Total   int64     `json:"total,omitempty"`
	Checked time.Time `json:"checked"`
	// CanRestart: this is the desktop app, which can restart itself into the update.
	CanRestart bool `json:"canRestart"`
	// Waiting: a restart was asked for and waits for Active Runs to end; GaveUp: it waited an hour.
	Waiting bool `json:"waiting,omitempty"`
	GaveUp  bool `json:"gaveUp,omitempty"`
	Active  int  `json:"active,omitempty"`
}

type updater struct {
	mu      sync.Mutex
	status  UpdateStatus
	latest  *update.Release
	staged  string
	target  update.Target
	restart func() // quits the desktop app; nil without one (headless)
	enabled bool
	waitGen int
	asked   time.Time
}

// Test hooks.
var (
	updateGOOS       = runtime.GOOS
	updateExecutable = func() (string, error) {
		exe, err := os.Executable()
		if err != nil {
			return "", err
		}
		return filepath.EvalSymlinks(exe)
	}
	updateInstall  = update.Install
	updateRelaunch = update.Relaunch
	updateFirst    = 10 * time.Second
	updateEvery    = 6 * time.Hour
	updateWaitTick = 5 * time.Second
	updateWaitMost = time.Hour
	updateClient   = func() *update.Client { return &update.Client{Token: ghToken} }
)

// ghToken is the GitHub CLI's token, for releases of a private repository ("" without gh or a sign-in).
func ghToken() string {
	argv, err := ghCommand()
	if err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := runCommand(ctx, "", append(argv, "auth", "token", "--hostname", "github.com"), nil)
	if err != nil {
		return ""
	}
	token := strings.TrimSpace(string(out))
	if strings.ContainsAny(token, " \n") {
		return ""
	}
	return token
}

// EnableUpdates starts the update checks. restart quits the desktop app (which then starts the new version);
// headless loopbee passes nil and only learns about new versions.
func (a *App) EnableUpdates(restart func()) {
	u := a.upd
	u.mu.Lock()
	if u.enabled {
		u.mu.Unlock()
		return
	}
	u.enabled, u.restart = true, restart
	u.status.CanRestart = restart != nil
	if exe, err := updateExecutable(); err == nil {
		u.target = update.Locate(updateGOOS, exe)
		if updateGOOS == "windows" {
			update.RemoveOld(exe) // what the last update moved aside
		}
	} else {
		u.target = update.Target{OS: updateGOOS, Stuck: "unsupported"}
	}
	if restart == nil && u.target.Stuck == "" {
		u.target.Stuck = "headless"
	}
	u.mu.Unlock()
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		timer := time.NewTimer(updateFirst)
		defer timer.Stop()
		for {
			select {
			case <-a.ctx.Done():
				return
			case <-timer.C:
			}
			a.mu.Lock()
			auto := !a.config.NoAutoUpdate
			a.mu.Unlock()
			u.mu.Lock()
			due := time.Since(u.asked) >= updateEvery
			u.mu.Unlock()
			if auto && due {
				a.CheckUpdate()
			}
			timer.Reset(time.Minute)
		}
	}()
}

// UpdateState is the current update status.
func (a *App) UpdateState() UpdateStatus {
	a.mu.Lock()
	active := len(a.active)
	a.mu.Unlock()
	u := a.upd
	u.mu.Lock()
	defer u.mu.Unlock()
	s := u.status
	s.Current = Version
	if s.State == "" {
		s.State = "idle"
	}
	if s.Waiting || s.State == "ready" {
		s.Active = active
	}
	return s
}

// CheckUpdate asks for the newest release and, when this copy can replace itself, downloads it. It returns at
// once; the state follows.
func (a *App) CheckUpdate() UpdateStatus {
	u := a.upd
	u.mu.Lock()
	if u.status.State == "checking" || u.status.State == "downloading" {
		u.mu.Unlock()
		return a.UpdateState()
	}
	u.status.State, u.status.Error, u.asked = "checking", "", time.Now()
	u.mu.Unlock()
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		a.runUpdate()
	}()
	return a.UpdateState()
}

func (a *App) runUpdate() {
	u := a.upd
	ctx, cancel := context.WithTimeout(a.ctx, 15*time.Minute)
	defer cancel()
	client := updateClient()
	rel, err := client.Latest(ctx)
	u.mu.Lock()
	u.status.Checked = time.Now()
	if err != nil {
		if u.staged != "" {
			u.status.State = "ready" // what was downloaded can still be put in
		} else {
			u.status.State, u.status.Error = "error", err.Error()
		}
		u.mu.Unlock()
		return
	}
	u.status.Latest, u.status.Notes, u.status.URL = rel.Version, rel.Notes, rel.URL
	switch {
	case !update.Released(Version):
		u.status.State = "source"
	case !update.Newer(rel.Version, Version):
		u.status.State = "latest"
	case u.staged != "" && u.latest != nil && u.latest.Version == rel.Version:
		u.status.State = "ready"
	case u.target.Stuck != "":
		u.status.State, u.status.Stuck = "available", u.target.Stuck
	default:
		u.status.State, u.status.Done, u.status.Total = "downloading", 0, 0
		target := u.target
		u.mu.Unlock()
		client.Progress = func(done, total int64) {
			u.mu.Lock()
			u.status.Done, u.status.Total = done, total
			u.mu.Unlock()
		}
		staged, err := client.Stage(ctx, rel, target, runtime.GOARCH)
		u.mu.Lock()
		if err != nil {
			log.Println("update:", err)
			u.status.State, u.status.Error = "error", err.Error()
			break
		}
		u.staged, u.latest, u.status.State = staged, rel, "ready"
	}
	u.mu.Unlock()
}

// RestartToUpdate puts the downloaded update in and restarts into it. when: "" waits until no Run is active
// (an hour at most), "now" restarts at once (active Runs are cut short and marked interrupted), "cancel" stops a
// restart that waits.
func (a *App) RestartToUpdate(when string) (UpdateStatus, error) {
	a.mu.Lock()
	viewer := a.config.Role == "viewer"
	active := len(a.active)
	a.mu.Unlock()
	if viewer {
		return a.UpdateState(), errors.New("viewer is read-only")
	}
	u := a.upd
	u.mu.Lock()
	if when == "cancel" {
		u.status.Waiting = false
		u.waitGen++
		u.mu.Unlock()
		return a.UpdateState(), nil
	}
	if u.status.State != "ready" || u.staged == "" || u.restart == nil {
		state := u.status.State
		u.mu.Unlock()
		if state == "error" {
			return a.CheckUpdate(), nil // download again
		}
		return a.UpdateState(), errors.New("no downloaded update to restart into")
	}
	if when != "now" && active > 0 {
		if !u.status.Waiting {
			u.status.Waiting, u.status.GaveUp = true, false
			u.waitGen++
			gen := u.waitGen
			a.wg.Add(1)
			go func() {
				defer a.wg.Done()
				a.waitIdleThenRestart(gen)
			}()
		}
		u.mu.Unlock()
		return a.UpdateState(), nil
	}
	u.mu.Unlock()
	return a.UpdateState(), a.restartNow()
}

func (a *App) waitIdleThenRestart(gen int) {
	u := a.upd
	start := time.Now()
	for {
		select {
		case <-a.ctx.Done():
			return
		case <-time.After(updateWaitTick):
		}
		u.mu.Lock()
		if !u.status.Waiting || u.waitGen != gen {
			u.mu.Unlock()
			return
		}
		if time.Since(start) >= updateWaitMost {
			u.status.Waiting, u.status.GaveUp = false, true
			u.mu.Unlock()
			return
		}
		u.mu.Unlock()
		a.mu.Lock()
		active := len(a.active)
		a.mu.Unlock()
		if active > 0 {
			continue
		}
		u.mu.Lock()
		u.status.Waiting = false
		u.mu.Unlock()
		if err := a.restartNow(); err != nil {
			log.Println("update:", err)
		}
		return
	}
}

// restartNow installs the staged version, arranges for it to start once this process is gone, and quits. The
// lock is not held while installing: macOS may be showing the administrator's password prompt.
func (a *App) restartNow() error {
	u := a.upd
	u.mu.Lock()
	staged, target, restart := u.staged, u.target, u.restart
	if staged == "" || restart == nil || u.status.State == "installing" {
		u.mu.Unlock()
		return errors.New("no downloaded update to restart into")
	}
	u.status.State, u.status.Error = "installing", ""
	u.mu.Unlock()
	_, err := updateInstall(staged, target, true)
	u.mu.Lock()
	defer u.mu.Unlock()
	if err != nil {
		u.status.State = "ready"
		if !errors.Is(err, update.ErrCanceled) {
			u.status.Error = err.Error()
		}
		return err
	}
	u.staged = ""
	if err := updateRelaunch(target); err != nil {
		u.status.State, u.status.Error = "latest", "installed; reopen loopbee to start the new version ("+err.Error()+")"
		return err
	}
	go restart()
	return nil
}

// installOnQuit puts a downloaded update in when loopbee quits, so the next start is the new version. It never
// asks for a password.
func (a *App) installOnQuit() {
	a.mu.Lock()
	auto := !a.config.NoAutoUpdate
	a.mu.Unlock()
	u := a.upd
	u.mu.Lock()
	defer u.mu.Unlock()
	if !auto || u.staged == "" || u.restart == nil {
		return
	}
	if _, err := updateInstall(u.staged, u.target, false); err != nil {
		log.Println("update on quit:", err)
		return
	}
	u.staged = ""
}
