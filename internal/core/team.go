package core

import (
	"errors"
	"time"
)

// Team sync without a button. An executor publishes once what the team sees has changed (a Run ended or waits at a
// gate, a Loop was saved or deleted), batching the changes of teamPublishDelay into one commit, and at once when the
// team repository is set. A viewer pulls when loopbee starts, every teamPullEvery and when the repository is set.
// "Publish now" and "Pull now" stay for when someone wants it sooner.
var (
	teamPublishDelay = 30 * time.Second
	teamPullEvery    = 5 * time.Minute
)

// beforeTeamPush lets a test push from another publisher between fetching and pushing.
var beforeTeamPush func()

// TeamStatus is what Settings › Team and the banner say about the last sync.
type TeamStatus struct {
	Last    *time.Time `json:"last,omitempty"`    // last successful publish (executor) or pull (viewer)
	Error   string     `json:"error,omitempty"`   // why the last sync failed; cleared by the next success
	Pending bool       `json:"pending,omitempty"` // changes wait for the batch to close
}

// errPushRejected: the team branch moved between fetching and pushing (another publisher pushed); syncing again
// starts from the new branch.
var errPushRejected = errors.New("the team repository changed while publishing")

// teamChanged asks for a sync: after the batch closes, or now. It takes only teamMu, so callers may hold a.mu.
func (a *App) teamChanged(now bool) {
	a.teamMu.Lock()
	defer a.teamMu.Unlock()
	if a.teamClosed {
		return
	}
	a.team.Pending = true
	if a.teamTimer != nil {
		// A pending sync reads the state when it runs, so it already includes this change. Only "now" moves it up;
		// if it is already running, it publishes the latest state anyway.
		if now && a.teamTimer.Stop() {
			a.teamTimer.Reset(0)
		}
		return
	}
	delay := teamPublishDelay
	if now {
		delay = 0
	}
	a.wg.Add(1)
	a.teamTimer = time.AfterFunc(delay, a.autoSync)
}

func (a *App) autoSync() {
	defer a.wg.Done()
	a.teamMu.Lock()
	a.teamTimer = nil
	a.teamMu.Unlock()
	if a.Settings().TeamRemote == "" {
		a.teamMu.Lock()
		a.team.Pending = false
		a.teamMu.Unlock()
		return
	}
	a.SyncTeam()
}

// pullTeam is the viewer's periodic pull; an executor publishes on changes instead.
func (a *App) pullTeam() {
	if c := a.Settings(); c.Role == "viewer" && c.TeamRemote != "" {
		a.SyncTeam()
	}
}

func (a *App) serveTeam() {
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		a.pullTeam()
		tick := time.NewTicker(teamPullEvery)
		defer tick.Stop()
		for {
			select {
			case <-a.ctx.Done():
				return
			case <-tick.C:
				a.pullTeam()
			}
		}
	}()
}

// stopTeam ends automatic syncs before Close waits for background work.
func (a *App) stopTeam() {
	a.teamMu.Lock()
	defer a.teamMu.Unlock()
	a.teamClosed = true
	if a.teamTimer != nil && a.teamTimer.Stop() {
		a.wg.Done()
	}
	a.teamTimer = nil
}

// SyncTeam publishes (executor) or pulls (viewer) once, starts over once when another publisher pushed in between,
// and records the outcome for Settings › Team.
func (a *App) SyncTeam() error {
	err := a.syncTeam()
	if errors.Is(err, errPushRejected) {
		err = a.syncTeam()
	}
	a.teamMu.Lock()
	defer a.teamMu.Unlock()
	a.team.Pending = a.teamTimer != nil
	if err != nil {
		a.team.Error = err.Error()
		return err
	}
	now := time.Now()
	a.team.Last, a.team.Error = &now, ""
	return nil
}

func (a *App) teamStatus() TeamStatus {
	a.teamMu.Lock()
	defer a.teamMu.Unlock()
	return a.team
}
