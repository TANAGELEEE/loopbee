# GUI shell and platform validation

Wails is pinned to `v3.0.0-beta.24`. The downloaded package source was checked for `application.New`, `Window.NewWithOptions`, `RegisterHook`, `SystemTray.New`, menu, and Quit interfaces. Reference: [Wails Application API](https://v3.wails.io/reference/application/).

Windows and macOS windows load the local authenticated URL by default. Closing hides the window to the tray, whose menu offers Open and Quit. The core implements the single-instance lock, including nogui mode. Windows startup failures display a native error dialog; missing WebView2 provides an installation URL and a headless alternative. Core cleanup is registered with Wails' exit hook and remains idempotent.

macOS notifications pass messages as `osascript` argv. Keep awake uses `caffeinate -i -w PID`. Start at login saves a dedicated LaunchAgent, effective at the next login; disabling it removes only this app's plist. Native macOS acceptance on a physical machine is still outstanding. Linux retains compilation and nogui support without a desktop shell.

## Windows desktop implementation and validation

Completed on 2026-10-11 using Windows 11 Pro 25H2 (build 26200), amd64, and WebView2 154.0.4258.62. The desktop app reuses the Go core, authenticated API, vanilla JS, and SQLite. Release binaries have no debugging port.

- M0: native-message tests against the release EXE verify the window and bee icon, single-instance enforcement, hide-on-close, background API, tray restoration, and Quit. Both `-nogui` and a separate headless binary remain available. The Windows WebView cache lives in `webview/` under the data directory; separate data directories do not share browser profiles.
- M2: start at login writes to the current user's `HKCU\Software\Microsoft\Windows\CurrentVersion\Run\loopbee`, preserving the absolute data directory and nogui mode, with a 260 UTF-16-unit command limit. Tests cover actual registration, argument round trips, removal, and restoration of the previous value. Keep awake calls `SetThreadExecutionState` on a locked OS thread. Enable, disable, and exit cleanup were tested; it prevents idle system sleep only.
- M5: actual Windows Credential Manager storage, retrieval, and deletion passed; the database stores references only. WinRT Toast submission through a fixed PowerShell script passed. `sent` means system acceptance, not that a banner appeared. Timeouts become unknown; explicit errors become failed. See [notifications](notify.md).
- M7: `scripts/build-windows.ps1` produces a GUI `.exe`, console nogui `.exe`, unsigned portable ZIP, and SHA256. It checks the PE subsystem, icon, version, PerMonitorV2 manifest, and ZIP file list. Windows CI build and desktop tests are configured but have not been executed remotely.
- M7: `tests/desktop-e2e.mjs` passed creation, execution, approval, cancellation, controlled Node verification, real downloads, three languages, themes, and forced-termination recovery in actual WebView2. Data paths contained Chinese characters and spaces. The [screenshot](../evidence/windows-desktop.png) comes from that test. Only `desktoptest` builds enable loopback CDP; `build-windows.ps1 -DesktopTest` produces a separate test executable excluded from release ZIPs. This Wails version clears WebView2 debugging environment variables, so test arguments are passed through WindowsOptions under build isolation.

Complete checks: `scripts/check.ps1`. For native API integration, set `LOOPBEE_WINDOWS_INTEGRATION=1` and run core tests. `LOOPBEE_WINDOWS_TOAST_TEST=1` additionally sends a local test notification. Tests delete temporary credentials and restore the previous login registration. Toast testing leaves the local test notification and app notification identity registration in place.

Not yet verified: Windows 10, ARM64, actual logout/login, whole-machine sleep/resume, multi-monitor DPI transitions, clean systems without WebView2, and manual observation of notification banners. API tests and DPI manifests do not establish acceptance of those scenarios. Desktop control tools failed to connect in the original test environment; tray/window behavior was verified using Win32 messages directed only at the test process.

References: [Wails Windows packaging](https://v3.wails.io/guides/build/windows/), [Microsoft WebView2 debugging](https://learn.microsoft.com/en-us/microsoft-edge/webview2/how-to/debug-visual-studio-code).

The app does not promise execution while asleep with the lid closed. Keep awake prevents idle sleep only. If the executable path changes, disable and re-enable start at login. Signing, notarization, and automatic updates are outside v1.

## Window size and placement (2026-10-11, second pass)

The UI spec asks for a 1280×820 default window, a 960×640 minimum and remembered bounds. The first implementation opened at 1280×900 with a 760×600 minimum and no clamping, so on a 1920×1080 display at 150 % scaling (1280×720 DIP work area) or a 1366×768 laptop the window was taller than the screen.

Now the window is created hidden and shown on `ApplicationStarted`, once the screen list is known. `fitWindow` (pure, tested in `windowbounds_test.go`) starts from the saved bounds in `<data>/window.json` or 1280×820, raises them to the minimum, reuses the saved position only when the title-bar area is still on a connected screen, and shrinks the window to the work area minus a 16 px margin. Closing to the tray and Quit save the bounds (and the maximised flag). Values are DIP on Windows (Wails converts physical pixels). The background colour matches the light `--bg` to avoid a white flash.

Not yet verified on hardware: multi-monitor DPI transitions and macOS coordinate behaviour of `Position()`. If a saved position is not on any reported screen the window is centred instead.
