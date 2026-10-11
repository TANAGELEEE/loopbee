package core

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

func ParseDefinition(source string) (Loop, error) {
	var l Loop
	if len(source) > 1<<20 {
		return l, errors.New("definition exceeds 1 MiB")
	}
	var value any
	if err := yaml.Unmarshal([]byte(source), &value); err != nil {
		return l, err
	}
	b, err := json.Marshal(value)
	if err != nil {
		return l, err
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err = d.Decode(&l); err != nil {
		return l, err
	}
	if len(l.Steps) == 0 {
		return l, errors.New("definition must contain steps")
	}
	l.ID = ""
	l.Version = 0
	l.Enabled = false
	l.Private = true
	l.Next = time.Time{}
	l.Updated = time.Time{}
	return l, nil
}

type ImportCandidate struct {
	Name   string `json:"name"`
	Source string `json:"source"`
	Raw    string `json:"raw"`
}

func ClaudeCandidates() ([]ImportCandidate, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(home, ".claude", "scheduled-tasks")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return []ImportCandidate{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []ImportCandidate{}
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		if len(out) >= 100 {
			break
		}
		path := filepath.Join(dir, entry.Name())
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		b, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
		f.Close()
		if err != nil {
			return nil, err
		}
		if len(b) > 1<<20 {
			continue
		}
		out = append(out, ImportCandidate{Name: entry.Name(), Source: path, Raw: string(b)})
	}
	return out, nil
}
