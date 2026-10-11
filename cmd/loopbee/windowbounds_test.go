package main

import "testing"

func TestFitWindow(t *testing.T) {
	big := rect{0, 0, 2560, 1400}
	cases := []struct {
		name       string
		saved      windowState
		hasSaved   bool
		work       []rect
		w, h, x, y int
		place      bool
	}{
		{"default on large screen", windowState{}, false, []rect{big}, 1280, 820, 0, 0, false},
		{"1920x1080 at 150% shrinks to work area", windowState{}, false, []rect{{0, 0, 1280, 680}}, 1264, 664, 0, 0, false},
		{"saved bounds restored", windowState{X: 100, Y: 80, Width: 1100, Height: 760}, true, []rect{big}, 1100, 760, 100, 80, true},
		{"saved size below minimum is raised", windowState{X: 10, Y: 10, Width: 500, Height: 300}, true, []rect{big}, 960, 640, 10, 10, true},
		{"saved position on a disconnected monitor is centered", windowState{X: 4000, Y: 80, Width: 1280, Height: 820}, true, []rect{big}, 1280, 820, 0, 0, false},
		{"saved position on second monitor", windowState{X: 2700, Y: 50, Width: 1280, Height: 820}, true, []rect{big, {2560, 0, 1920, 1040}}, 1280, 820, 2700, 50, true},
		{"window pulled back inside the screen", windowState{X: 2000, Y: 900, Width: 1280, Height: 820}, true, []rect{big}, 1280, 820, 1280, 580, true},
		{"no screen information", windowState{}, false, nil, 1280, 820, 0, 0, false},
	}
	for _, c := range cases {
		w, h, x, y, place := fitWindow(c.saved, c.hasSaved, c.work)
		if w != c.w || h != c.h || place != c.place || (place && (x != c.x || y != c.y)) {
			t.Errorf("%s: got %dx%d at %d,%d place=%v", c.name, w, h, x, y, place)
		}
	}
}
