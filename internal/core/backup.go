package core

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

type Archive struct {
	Version int                `json:"version"`
	Loops   []Loop             `json:"loops"`
	Runs    []Run              `json:"runs"`
	Events  map[string][]Event `json:"events"`
}

var validID = regexp.MustCompile(`^[a-f0-9]{24}$`)
var validArtifactName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,199}$`)

const archiveLimit = 128 << 20

func (a *App) Export(w io.Writer) error {
	s, err := a.State()
	if err != nil {
		return err
	}
	bundle := Archive{Version: 1, Loops: []Loop{}, Runs: []Run{}, Events: map[string][]Event{}}
	publicLoops := map[string]bool{}
	for _, l := range s.Loops {
		publicLoops[l.ID] = !l.Private
		if !l.Private {
			bundle.Loops = append(bundle.Loops, l)
		}
	}
	for _, r := range s.Runs {
		if !r.Definition.Private && publicLoops[r.LoopID] && terminal(r.Status) {
			bundle.Runs = append(bundle.Runs, r)
			events, err := a.Events(r.ID)
			if err != nil {
				return err
			}
			bundle.Events[r.ID] = events
		}
	}
	data, err := json.Marshal(bundle)
	if err != nil {
		return err
	}
	if sensitive(data) {
		return errors.New("export blocked: possible secret in definitions or evidence")
	}
	z := zip.NewWriter(w)
	entry, err := z.Create("loopbee/manifest.json")
	if err != nil {
		return err
	}
	if _, err = entry.Write(data); err != nil {
		return err
	}
	total := len(data)
	for _, r := range bundle.Runs {
		for _, name := range r.Artifacts {
			b, err := a.ReadArtifact(r.ID, name)
			if err != nil {
				return err
			}
			total += len(b)
			if len(b) > 8<<20 || total > archiveLimit {
				return errors.New("archive exceeds size limit")
			}
			if sensitive(b) {
				return errors.New("export blocked: possible secret in artifact")
			}
			entry, err = z.Create("loopbee/runs/" + r.ID + "/" + name)
			if err != nil {
				return err
			}
			if _, err = entry.Write(b); err != nil {
				return err
			}
		}
	}
	return z.Close()
}
func (a *App) ImportArchive(data []byte) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.config.Role == "viewer" {
		return errors.New("viewer is read-only")
	}
	if len(data) > 32<<20 {
		return errors.New("compressed archive exceeds 32 MiB")
	}
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	entries := map[string][]byte{}
	total := uint64(0)
	for _, file := range z.File {
		total += file.UncompressedSize64
		if total > archiveLimit || len(entries) > 10000 {
			return errors.New("archive exceeds expansion limit")
		}
		if _, ok := entries[file.Name]; ok {
			return errors.New("duplicate archive entry")
		}
		if !file.Mode().IsRegular() {
			return errors.New("archive must contain regular files")
		}
		f, err := file.Open()
		if err != nil {
			return err
		}
		b, err := io.ReadAll(io.LimitReader(f, archiveLimit+1))
		f.Close()
		if err != nil {
			return err
		}
		if len(b) > archiveLimit {
			return errors.New("archive entry too large")
		}
		entries[file.Name] = b
	}
	manifest, ok := entries["loopbee/manifest.json"]
	if !ok {
		return errors.New("manifest missing")
	}
	delete(entries, "loopbee/manifest.json")
	var bundle Archive
	if err = json.Unmarshal(manifest, &bundle); err != nil {
		return err
	}
	if bundle.Version != 1 {
		return errors.New("unsupported archive version")
	}
	ids := map[string]string{}
	for _, l := range bundle.Loops {
		if !validID.MatchString(l.ID) || ids[l.ID] != "" {
			return errors.New("invalid or duplicate loop ID")
		}
		ids[l.ID] = newID()
	}
	stage, err := os.MkdirTemp(a.Dir, "import-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	runIDs := map[string]string{}
	for _, r := range bundle.Runs {
		if !validID.MatchString(r.ID) || runIDs[r.ID] != "" {
			return errors.New("invalid or duplicate run ID")
		}
		runIDs[r.ID] = newID()
		if ids[r.LoopID] == "" {
			ids[r.LoopID] = newID()
		}
		for _, name := range r.Artifacts {
			if !validArtifactName.MatchString(name) {
				return errors.New("invalid artifact name")
			}
			key := "loopbee/runs/" + r.ID + "/" + name
			b, ok := entries[key]
			if !ok {
				return errors.New("artifact missing")
			}
			delete(entries, key)
			dir := filepath.Join(stage, runIDs[r.ID])
			if err = os.MkdirAll(dir, 0700); err != nil {
				return err
			}
			if err = os.WriteFile(filepath.Join(dir, name), b, 0600); err != nil {
				return err
			}
		}
	}
	if len(entries) != 0 {
		return errors.New("unexpected archive paths")
	}
	tx, err := a.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, l := range bundle.Loops {
		l.ID = ids[l.ID]
		l.Enabled = false
		l.Private = true
		l.Next = time.Time{}
		l.Version = 1
		l.Updated = time.Now()
		b, _ := json.Marshal(l)
		if _, err = tx.Exec("INSERT INTO objects(kind,id,data) VALUES('loop',?,?)", l.ID, string(b)); err != nil {
			return err
		}
	}
	moved := []string{}
	committed := false
	defer func() {
		if !committed {
			for _, path := range moved {
				os.RemoveAll(path)
			}
		}
	}()
	if err = os.MkdirAll(filepath.Join(a.Dir, "runs"), 0700); err != nil {
		return err
	}
	for _, r := range bundle.Runs {
		oldID := r.ID
		r.ID = runIDs[oldID]
		r.LoopID = ids[r.LoopID]
		r.Definition.ID = r.LoopID
		r.Definition.Private = true
		if !terminal(r.Status) {
			r.Status = "interrupted"
			now := time.Now()
			r.Finished = &now
		}
		b, _ := json.Marshal(r)
		if _, err = tx.Exec("INSERT INTO objects(kind,id,data) VALUES('run',?,?)", r.ID, string(b)); err != nil {
			return err
		}
		for _, e := range bundle.Events[oldID] {
			if _, err = tx.Exec("INSERT INTO events(run_id,time,type,text) VALUES(?,?,?,?)", r.ID, e.Time.UTC().Format(time.RFC3339Nano), e.Type, e.Text); err != nil {
				return err
			}
		}
		if len(r.Artifacts) > 0 {
			dest := filepath.Join(a.Dir, "runs", r.ID)
			if err = os.Rename(filepath.Join(stage, r.ID), dest); err != nil {
				return err
			}
			moved = append(moved, dest)
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}
