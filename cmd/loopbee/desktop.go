//go:build (darwin || windows) && !nogui

package main

import (
	"errors"
	"path/filepath"
	"runtime"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"loopbee/internal/core"
	"loopbee/internal/ui"
)

func desktop(url, data string, shutdown func()) error {
	icon, err := ui.Files.ReadFile("web/bee.png")
	if err != nil {
		return err
	}
	app := application.New(application.Options{
		Name: core.Brand, Description: "Local agent loops", Icon: icon,
		Windows:    application.WindowsOptions{WebviewUserDataPath: filepath.Join(data, "webview"), AdditionalBrowserArgs: desktopBrowserArgs()},
		OnShutdown: shutdown,
	})
	if runtime.GOOS == "windows" && app.Env.Info().PlatformInfo["WebView2"] == "" {
		return errors.New("Microsoft Edge WebView2 Runtime is required. Install the Evergreen Runtime from https://developer.microsoft.com/microsoft-edge/webview2/ and reopen Loopbee. You can also use loopbee-nogui.exe -nogui")
	}
	saved, hasSaved := loadWindowState(data)
	window := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title: core.Brand, Width: defaultWidth, Height: defaultHeight, MinWidth: minWidth, MinHeight: minHeight, URL: url,
		// Shown once the screens are known so the first frame already has the right size and place.
		Hidden: true, InitialPosition: application.WindowCentered,
		BackgroundColour: application.NewRGB(0xf4, 0xf4, 0xf6),
		Permissions: map[application.PermissionType]application.Permission{
			application.PermissionCamera:        application.PermissionDeny,
			application.PermissionMicrophone:    application.PermissionDeny,
			application.PermissionGeolocation:   application.PermissionDeny,
			application.PermissionNotifications: application.PermissionDeny,
			application.PermissionClipboardRead: application.PermissionDeny,
		},
	})
	remember := func() {
		if window.IsMinimised() {
			return
		}
		state := windowState{Maximised: window.IsMaximised()}
		if !state.Maximised {
			state.X, state.Y = window.Position()
			state.Width, state.Height = window.Size()
		} else if hasSaved {
			state.X, state.Y, state.Width, state.Height = saved.X, saved.Y, saved.Width, saved.Height
		} else {
			state.Width, state.Height = defaultWidth, defaultHeight
		}
		saveWindowState(data, state)
	}
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		var work []rect
		if primary := app.Screen.GetPrimary(); primary != nil {
			work = append(work, rect{primary.WorkArea.X, primary.WorkArea.Y, primary.WorkArea.Width, primary.WorkArea.Height})
		}
		for _, s := range app.Screen.GetAll() {
			if s != nil && !s.IsPrimary {
				work = append(work, rect{s.WorkArea.X, s.WorkArea.Y, s.WorkArea.Width, s.WorkArea.Height})
			}
		}
		w, h, x, y, place := fitWindow(saved, hasSaved, work)
		window.SetSize(w, h)
		if place {
			window.SetPosition(x, y)
		} else {
			window.Center()
		}
		window.Show()
		if hasSaved && saved.Maximised {
			window.Maximise()
		}
	})
	window.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) { remember(); e.Cancel(); window.Hide() })
	tray := app.SystemTray.New()
	tray.SetIcon(icon)
	tray.SetLabel(core.Brand)
	tray.SetTooltip(core.Brand)
	open := func() { window.UnMinimise(); window.Show(); window.Focus() }
	tray.OnClick(open)
	tray.OnDoubleClick(open)
	menu := application.NewMenu()
	menu.Add("Open " + core.Brand).OnClick(func(*application.Context) { open() })
	menu.AddSeparator()
	menu.Add("Quit").OnClick(func(*application.Context) {
		if window.IsVisible() {
			remember()
		}
		app.Quit()
	})
	tray.SetMenu(menu)
	return app.Run()
}
