package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Window size rules from the UI spec: 1280×820 by default, never smaller than 960×640,
// and never larger than the work area of the screen it opens on.
const (
	defaultWidth  = 1280
	defaultHeight = 820
	minWidth      = 960
	minHeight     = 640
)

type rect struct{ X, Y, W, H int }

// windowState is persisted in <data>/window.json so the next launch reopens where the user left it.
type windowState struct {
	X, Y, Width, Height int
	Maximised           bool
}

func loadWindowState(data string) (windowState, bool) {
	var s windowState
	b, err := os.ReadFile(filepath.Join(data, "window.json"))
	if err != nil || json.Unmarshal(b, &s) != nil || s.Width <= 0 || s.Height <= 0 {
		return windowState{}, false
	}
	return s, true
}

func saveWindowState(data string, s windowState) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(data, "window.json"), b, 0600)
}

// fitWindow decides the initial bounds. work is the primary work area followed by any other
// screens; all values are in device-independent pixels. place reports whether X/Y should be used
// (false means "center on the primary screen").
func fitWindow(saved windowState, hasSaved bool, work []rect) (w, h, x, y int, place bool) {
	w, h = defaultWidth, defaultHeight
	if hasSaved {
		w, h = saved.Width, saved.Height
	}
	w, h = max(w, minWidth), max(h, minHeight)
	if len(work) == 0 {
		return w, h, 0, 0, false
	}
	target := work[0]
	if hasSaved {
		for _, s := range work {
			// Reuse the saved position only when the title bar area is still on a visible screen.
			if saved.X+120 > s.X && saved.X+120 < s.X+s.W && saved.Y >= s.Y && saved.Y+40 < s.Y+s.H {
				target, place, x, y = s, true, saved.X, saved.Y
				break
			}
		}
	}
	// A 1280×820 window does not fit a 1366×768 laptop or a 1920×1080 screen at 150 % scaling;
	// shrink to the work area (keeping a small margin) instead of opening partly off-screen.
	if maxW := target.W - 16; w > maxW {
		w = max(maxW, 640)
	}
	if maxH := target.H - 16; h > maxH {
		h = max(maxH, 480)
	}
	if place {
		x = min(max(x, target.X), target.X+target.W-w)
		y = min(max(y, target.Y), target.Y+target.H-h)
	}
	return w, h, x, y, place
}
