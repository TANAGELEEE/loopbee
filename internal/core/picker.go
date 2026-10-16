package core

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// Picker opens the system's chooser and returns the chosen path, or "" when the person cancelled. files allows files
// as well as folders. The desktop shell provides it; without a window (-nogui in a browser) there is none and the
// page keeps its text fields.
type Picker func(title, start string, files bool) (string, error)

func (a *App) SetPicker(p Picker) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.picker = p
}

// PickPath asks the person for a folder (or a file) instead of a typed path. The chooser opens at start when that
// folder exists (~/ is the home folder), at the folder that contains it, or else at the home folder.
func (a *App) PickPath(title, start string, files bool) (string, error) {
	a.mu.Lock()
	p := a.picker
	a.mu.Unlock()
	if p == nil {
		return "", errors.New("choosing a folder needs the loopbee desktop app; type the path instead")
	}
	return p(title, pickStart(start), files)
}

func pickStart(start string) string {
	home, _ := os.UserHomeDir()
	start = strings.TrimSpace(start)
	if rest, ok := strings.CutPrefix(start, "~"); ok && (rest == "" || rest[0] == '/' || rest[0] == '\\') {
		start = filepath.Join(home, rest)
	}
	for dir := start; filepath.IsAbs(dir); dir = filepath.Dir(dir) {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir
		}
		if filepath.Dir(dir) == dir {
			break
		}
	}
	return home
}
