package core

import (
	"os"
	"path/filepath"
	"testing"
)

// Paths are chosen, not typed: the chooser opens where the field already points (or the nearest folder that exists,
// or the home folder), and without a desktop the API says to type the path instead of failing silently.
func TestPickPathOpensWhereTheFieldPoints(t *testing.T) {
	a := testApp(t)
	if _, err := a.PickPath("Project folder", "", false); err == nil {
		t.Fatal("without a desktop there is no chooser")
	}
	if s, _ := a.State(); s.Info.CanPick {
		t.Fatal("state offers a chooser that does not exist")
	}
	var opened string
	a.SetPicker(func(title, start string, files bool) (string, error) { opened = start; return "/chosen", nil })
	if s, _ := a.State(); !s.Info.CanPick {
		t.Fatal("state does not offer the desktop's chooser")
	}
	home, _ := os.UserHomeDir()
	dir := t.TempDir()
	for start, want := range map[string]string{
		dir:                                  dir,
		filepath.Join(dir, "missing", "sub"): dir,
		"":                                   home,
		"relative/path":                      home,
		"~":                                  home,
	} {
		if got, err := a.PickPath("Project folder", start, false); err != nil || got != "/chosen" || opened != want {
			t.Errorf("start %q: opened at %q (want %q), got %q %v", start, opened, want, got, err)
		}
	}
}
