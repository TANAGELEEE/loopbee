package core

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/gofrs/flock"
	"github.com/zalando/go-keyring"
	_ "modernc.org/sqlite"
)

type App struct {
	db          *sql.DB
	Dir         string
	mu          sync.Mutex
	config      Settings
	active      map[string]context.CancelFunc
	gates       map[string]chan gateDecision
	ctx         context.Context
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	lock        *flock.Flock
	agents      []Agent
	github      GitHubStatus
	syncMu      sync.Mutex
	teamMu      sync.Mutex // guards team, teamTimer and teamClosed; never held while taking mu or syncMu
	team        TeamStatus
	teamTimer   *time.Timer
	teamClosed  bool
	awakeCancel context.CancelFunc
	upd         *updater
	picker      Picker // the desktop's folder chooser; nil without a window
}

func Open(dir string) (*App, error) {
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	dir = absolute
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	lock := flock.New(filepath.Join(dir, "instance.lock"))
	ok, err := lock.TryLock()
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.New("loopbee is already using this data directory")
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "loopbee.db"))
	if err != nil {
		lock.Unlock()
		return nil, err
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;
 CREATE TABLE IF NOT EXISTS objects(kind TEXT NOT NULL,id TEXT NOT NULL,data TEXT NOT NULL,PRIMARY KEY(kind,id));
 CREATE TABLE IF NOT EXISTS events(id INTEGER PRIMARY KEY AUTOINCREMENT,run_id TEXT NOT NULL,time TEXT NOT NULL,type TEXT NOT NULL,text TEXT NOT NULL);
 CREATE INDEX IF NOT EXISTS events_run ON events(run_id,id);`)
	if err != nil {
		db.Close()
		lock.Unlock()
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	a := &App{db: db, Dir: dir, lock: lock, ctx: ctx, cancel: cancel, active: map[string]context.CancelFunc{}, gates: map[string]chan gateDecision{}, upd: &updater{}}
	a.config = Settings{DigestCron: "0 18 * * *", DigestTimezone: "UTC", MaxSandbox: "read-only", Language: "zh-TW", ReportLanguage: "zh-TW", DigestLanguage: "zh-TW", Theme: "system", Motion: true, Role: "executor", RetentionDays: 30, Channels: []Channel{}, TeamBranch: "loopbee", Publisher: newID()}
	if err = a.get("settings", "general", &a.config); err != nil && !errors.Is(err, sql.ErrNoRows) {
		a.Close()
		return nil, err
	}
	if errors.Is(err, sql.ErrNoRows) {
		if err = a.put("settings", "general", a.config); err != nil {
			a.Close()
			return nil, err
		}
	}
	// Loops saved before projects existed get their project now; it is derived, so the version stays.
	loops, err := a.Loops()
	if err != nil {
		a.Close()
		return nil, err
	}
	for _, l := range loops {
		if l.Project == "" {
			l.Project = projectName(l.Directory)
			if err = a.put("loop", l.ID, l); err != nil {
				a.Close()
				return nil, err
			}
		}
	}
	runs, err := a.Runs()
	if err != nil {
		a.Close()
		return nil, err
	}
	for _, r := range runs {
		if !terminal(r.Status) {
			r.Status = "interrupted"
			r.Error = "Application stopped; review evidence before starting a new run"
			now := time.Now()
			r.Finished = &now
			for i := range r.Trace {
				if r.Trace[i].Finished == nil {
					r.Trace[i].Status = "interrupted"
					r.Trace[i].Finished = &now
				}
			}
			if err = a.saveRun(r); err != nil {
				a.Close()
				return nil, err
			}
		}
	}
	deliveries, err := list[Delivery](a, "delivery")
	if err != nil {
		a.Close()
		return nil, err
	}
	for _, d := range deliveries {
		if d.Status == "pending" || d.Status == "retryable" {
			d.Status = "unknown"
			d.Error = "application stopped before delivery was confirmed"
			if err = a.put("delivery", d.ID, d); err != nil {
				a.Close()
				return nil, err
			}
		}
	}
	return a, nil
}
func (a *App) Close() {
	a.installOnQuit()
	a.stopTeam()
	a.cancel()
	a.mu.Lock()
	for _, cancel := range a.active {
		cancel()
	}
	a.mu.Unlock()
	a.wg.Wait()
	a.db.Close()
	a.lock.Unlock()
}
func newID() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func (a *App) put(kind, id string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = a.db.Exec("INSERT INTO objects(kind,id,data) VALUES(?,?,?) ON CONFLICT(kind,id) DO UPDATE SET data=excluded.data", kind, id, string(b))
	return err
}
func (a *App) get(kind, id string, v any) error {
	var b string
	if err := a.db.QueryRow("SELECT data FROM objects WHERE kind=? AND id=?", kind, id).Scan(&b); err != nil {
		return err
	}
	return json.Unmarshal([]byte(b), v)
}
func list[T any](a *App, kind string) ([]T, error) {
	rows, err := a.db.Query("SELECT data FROM objects WHERE kind=? ORDER BY rowid DESC", kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []T{}
	for rows.Next() {
		var b string
		var v T
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(b), &v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (a *App) Loops() ([]Loop, error)     { return list[Loop](a, "loop") }
func (a *App) Runs() ([]Run, error)       { return list[Run](a, "run") }
func (a *App) Run(id string) (Run, error) { var r Run; err := a.get("run", id, &r); return r, err }
func (a *App) saveRun(r Run) error {
	if err := a.put("run", r.ID, r); err != nil {
		return err
	}
	if terminal(r.Status) || r.Status == "waiting" {
		a.teamChanged(false)
	}
	return nil
}
func (a *App) Settings() Settings {
	a.mu.Lock()
	defer a.mu.Unlock()
	c := a.config
	c.Channels = append([]Channel{}, c.Channels...)
	return c
}
func (a *App) SaveSettings(c Settings) error {
	a.syncMu.Lock()
	defer a.syncMu.Unlock()
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, err := schedule(Loop{Cron: c.DigestCron, Timezone: c.DigestTimezone}); err != nil {
		return errors.New("invalid digest schedule or timezone")
	}
	if !language(c.Language) || !language(c.ReportLanguage) || !language(c.DigestLanguage) {
		return errors.New("unsupported language")
	}
	if c.Role != "executor" && c.Role != "viewer" {
		return errors.New("invalid role")
	}
	if !validSandbox(c.MaxSandbox) {
		return errors.New("invalid maximum sandbox")
	}
	if _, known := adapterFor(c.DefaultAgent); c.DefaultAgent != "" && !known {
		return errors.New("unknown default agent")
	}
	if len(a.active) > 0 && c.MaxSandbox != a.config.MaxSandbox {
		return errors.New("cancel active runs before changing permission limit")
	}
	if len(a.active) > 0 && c.Role != a.config.Role {
		return errors.New("cancel active runs before switching role")
	}
	if c.RetentionDays < 1 || c.RetentionDays > 3650 {
		return errors.New("retention must be 1–3650 days")
	}
	if c.Theme != "system" && c.Theme != "light" && c.Theme != "dark" {
		return errors.New("invalid theme")
	}
	if c.Publisher != a.config.Publisher {
		return errors.New("publisher identity cannot be changed")
	}
	for i := range c.Channels {
		if c.Channels[i].Endpoint == "********" {
			for _, old := range a.config.Channels {
				if old.Kind == c.Channels[i].Kind {
					c.Channels[i].Endpoint = old.Endpoint
				}
			}
		}
		if c.Channels[i].Secret == "********" {
			for _, old := range a.config.Channels {
				if old.Kind == c.Channels[i].Kind {
					c.Channels[i].Secret = old.Secret
				}
			}
		}
	}
	if err := validateChannels(c.Channels); err != nil {
		return err
	}
	for i := range c.Channels {
		var err error
		c.Channels[i].Secret, err = storeSecret(c.Publisher, c.Channels[i].Kind+"/secret", c.Channels[i].Secret)
		if err != nil {
			return err
		}
		if c.Channels[i].Kind == "feishu" {
			c.Channels[i].Endpoint, err = storeSecret(c.Publisher, "feishu/endpoint", c.Channels[i].Endpoint)
			if err != nil {
				return err
			}
		}
	}
	if err := a.put("settings", "general", c); err != nil {
		return err
	}
	if c.StartAtLogin != a.config.StartAtLogin {
		if err := configureAutostart(c.StartAtLogin, a.Dir); err != nil {
			return errors.Join(err, a.put("settings", "general", a.config))
		}
	}
	if c.KeepAwake != a.config.KeepAwake {
		if err := a.setKeepAwake(c.KeepAwake); err != nil {
			if c.StartAtLogin != a.config.StartAtLogin {
				err = errors.Join(err, configureAutostart(a.config.StartAtLogin, a.Dir))
			}
			return errors.Join(err, a.put("settings", "general", a.config))
		}
	}
	if c.DigestEnabled != a.config.DigestEnabled || c.DigestCron != a.config.DigestCron || c.DigestTimezone != a.config.DigestTimezone {
		schedule, _ := schedule(Loop{Cron: c.DigestCron, Timezone: c.DigestTimezone})
		if err := a.put("cursor", "digest", schedule.Next(time.Now())); err != nil {
			if c.KeepAwake != a.config.KeepAwake {
				err = errors.Join(err, a.setKeepAwake(a.config.KeepAwake))
			}
			if c.StartAtLogin != a.config.StartAtLogin {
				err = errors.Join(err, configureAutostart(a.config.StartAtLogin, a.Dir))
			}
			return errors.Join(err, a.put("settings", "general", a.config))
		}
	}
	// A newly set team repository (or role) is synced at once instead of waiting for the next change.
	teamSet := c.TeamRemote != "" && (c.TeamRemote != a.config.TeamRemote || c.TeamBranch != a.config.TeamBranch || c.Role != a.config.Role)
	a.config = c
	if teamSet {
		a.teamChanged(true)
	}
	return nil
}
func (a *App) SaveLoop(l Loop) (Loop, error) {
	if l.Project = strings.TrimSpace(l.Project); l.Project == "" {
		l.Project = projectName(l.Directory)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.config.Role == "viewer" {
		return l, errors.New("viewer is read-only")
	}
	var err error
	if l.Env, err = a.storeLoopSecrets(l); err != nil {
		return l, err
	}
	if err := Validate(l); err != nil {
		return l, err
	}
	if !sandboxWithin(l.Sandbox, a.config.MaxSandbox) {
		return l, errors.New("permission exceeds this device's limit")
	}
	if l.ID == "" {
		l.ID = newID()
		l.Version = 1
	} else {
		var old Loop
		if err := a.get("loop", l.ID, &old); err != nil {
			return l, err
		}
		if l.Version != old.Version {
			return l, errors.New("definition changed; reload before saving")
		}
		l.Version++
	}
	l.Updated = time.Now()
	l.Next = time.Time{}
	if l.Enabled && l.Cron != "" {
		s, _ := schedule(l)
		l.Next = s.Next(l.Updated)
	}
	if err := a.put("loop", l.ID, l); err != nil {
		return l, err
	}
	a.teamChanged(false)
	return l, nil
}
func (a *App) DeleteLoop(id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.config.Role == "viewer" {
		return errors.New("viewer is read-only")
	}
	runs, err := a.Runs()
	if err != nil {
		return err
	}
	for _, r := range runs {
		if r.LoopID == id && !terminal(r.Status) {
			return errors.New("cancel active run first")
		}
	}
	var l Loop
	found := a.get("loop", id, &l) == nil
	if _, err = a.db.Exec("DELETE FROM objects WHERE kind='loop' AND id=?", id); err != nil {
		return err
	}
	if found {
		a.cleanupLoop(l)
	}
	a.teamChanged(false)
	return nil
}

// cleanupLoop removes what loopbee created for a deleted Loop: its worktree (registered in the developer's
// repository) and the secret values it stored in the system keychain. Branches stay: they may hold unreviewed work.
// Runs keep their definition snapshot and evidence until retention prunes them.
func (a *App) cleanupLoop(l Loop) {
	path := filepath.Join(a.Dir, "worktrees", l.ID)
	if _, err := os.Stat(path); err == nil && validID.MatchString(l.ID) {
		ctx, cancel := context.WithTimeout(a.ctx, time.Minute)
		if l.Directory != "" {
			runCommand(ctx, l.Directory, []string{"git", "worktree", "remove", "--force", path}, nil)
		}
		if err := os.RemoveAll(path); err != nil {
			a.event("", "loop.cleanup_failed", path+": "+err.Error())
		}
		if l.Directory != "" {
			runCommand(ctx, l.Directory, []string{"git", "worktree", "prune"}, nil)
		}
		cancel()
	}
	for _, e := range l.Env {
		if ref, ok := strings.CutPrefix(e.Value, "keyring:"); ok && strings.Contains(ref, "/loop/") {
			if err := deleteSecret(ref); err != nil {
				a.event("", "loop.cleanup_failed", "keychain entry for "+e.Name+": "+err.Error())
			}
		}
	}
}

// deleteSecret removes a stored value from the system keychain; tests replace it.
var deleteSecret = func(ref string) error {
	if err := keyring.Delete(keyringService, ref); err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return err
	}
	return nil
}

func (a *App) event(id, kind, text string) error {
	_, err := a.db.Exec("INSERT INTO events(run_id,time,type,text) VALUES(?,?,?,?)", id, time.Now().UTC().Format(time.RFC3339Nano), kind, text)
	return err
}
func (a *App) Events(id string) ([]Event, error) {
	if id != "" && a.Settings().Role == "viewer" {
		var evidence Evidence
		if err := a.get("shared_evidence", id, &evidence); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return []Event{}, nil
			}
			return nil, err
		}
		return evidence.Events, nil
	}
	rows, err := a.db.Query("SELECT id,time,type,text FROM events WHERE run_id=? ORDER BY id DESC LIMIT 1000", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		e := Event{RunID: id}
		var at string
		if err = rows.Scan(&e.ID, &at, &e.Type, &e.Text); err != nil {
			return nil, err
		}
		e.Time, _ = time.Parse(time.RFC3339Nano, at)
		out = append(out, e)
	}
	return out, rows.Err()
}
func (a *App) State() (State, error) {
	c := a.Settings()
	for i := range c.Channels {
		if c.Channels[i].Kind == "feishu" && c.Channels[i].Endpoint != "" {
			c.Channels[i].Endpoint = "********"
		}
		if c.Channels[i].Secret != "" {
			c.Channels[i].Secret = "********"
		}
	}
	s := State{Brand: Brand, Settings: c, Updated: time.Now()}
	var err error
	if s.Loops, err = a.Loops(); err != nil {
		return s, err
	}
	if s.Runs, err = a.Runs(); err != nil {
		return s, err
	}
	if c.Role == "viewer" {
		if s.Loops, err = list[Loop](a, "shared_loop"); err != nil {
			return s, err
		}
		if s.Runs, err = list[Run](a, "shared_run"); err != nil {
			return s, err
		}
	}
	if s.Deliveries, err = list[Delivery](a, "delivery"); err != nil {
		return s, err
	}
	if s.Activity, err = a.Events(""); err != nil {
		return s, err
	}
	if c.Role == "viewer" {
		s.Activity = []Event{}
		s.Deliveries = []Delivery{}
	}
	a.mu.Lock()
	s.Agents = append([]Agent{}, a.agents...)
	s.GitHub = a.github
	s.Info = Info{Version: Version, OS: runtime.GOOS, Arch: runtime.GOARCH, DataDir: a.Dir, Active: len(a.active), CanPick: a.picker != nil}
	a.mu.Unlock()
	s.Update = a.UpdateState()
	s.Team = a.teamStatus()
	return s, nil
}
