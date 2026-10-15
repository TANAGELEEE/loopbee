//go:build windows && !nogui

package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"loopbee/internal/core"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/w32"
	"golang.org/x/sys/windows"
)

// This opt-in integration test targets only the process it launches, including its message-only tray window.
func TestNativeDesktopLifecycle(t *testing.T) {
	binary := os.Getenv("LOOPBEE_NATIVE_TEST_BINARY")
	if binary == "" {
		t.Skip("set LOOPBEE_NATIVE_TEST_BINARY to the packaged Windows GUI executable")
	}
	data := filepath.Join(t.TempDir(), "桌面 lifecycle")
	cmd := exec.Command(binary, "-data", data)
	// A file, not a pipe: Wait must not wait for WebView2 processes that inherit the handle.
	stderr, err := os.Create(filepath.Join(t.TempDir(), "stderr.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer stderr.Close()
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	exited := make(chan struct{})
	var descendants []windows.Handle
	go func() {
		err := cmd.Wait()
		close(exited)
		finished <- err
	}()
	t.Cleanup(func() {
		defer func() {
			deadline := time.Now().Add(10 * time.Second)
			for _, handle := range descendants {
				remaining := max(0, time.Until(deadline).Milliseconds())
				if result, _ := windows.WaitForSingleObject(handle, uint32(remaining)); result == uint32(windows.WAIT_TIMEOUT) {
					windows.TerminateProcess(handle, 1)
					windows.WaitForSingleObject(handle, 5000)
					t.Log("cleaned up an owned descendant still exiting after the desktop stopped")
				}
				windows.CloseHandle(handle)
			}
		}()
		select {
		case <-exited:
			return
		default:
		}
		kill := exec.Command("taskkill", "/PID", strconv.Itoa(cmd.Process.Pid), "/T", "/F")
		kill.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		kill.Run()
		select {
		case <-exited:
		case <-time.After(5 * time.Second):
			t.Error("desktop process did not stop during cleanup")
		}
	})
	wait := func(label string, check func() bool) {
		t.Helper()
		until := time.Now().Add(30 * time.Second)
		for time.Now().Before(until) {
			if check() {
				return
			}
			select {
			case <-exited:
				// A crash, not a slow start: say so with the app's own output.
				output, _ := os.ReadFile(stderr.Name())
				t.Fatalf("desktop exited while waiting for %s: %s", label, output)
			default:
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatalf("timed out: %s", label)
	}
	user32 := windows.NewLazySystemDLL("user32.dll")
	processHandle, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(cmd.Process.Pid))
	if err != nil {
		t.Fatal(err)
	}
	result, _, _ := user32.NewProc("WaitForInputIdle").Call(uintptr(processHandle), 30000)
	defer windows.CloseHandle(processHandle)
	if result != 0 {
		t.Fatalf("desktop process did not reach input idle: %d", result)
	}
	var window w32.HWND
	windowCallback := windows.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
		_, pid := w32.GetWindowThreadProcessId(w32.HWND(hwnd))
		if pid == cmd.Process.Pid && w32.GetWindowText(w32.HWND(hwnd)) == "Loopbee" && w32.IsWindowVisible(w32.HWND(hwnd)) {
			window = w32.HWND(hwnd)
		}
		return 1
	})
	wait("visible native window", func() bool {
		w32.EnumWindows(windowCallback, 0)
		return window != 0
	})
	var webviewVisible bool
	childCallback := windows.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
		if w32.GetClassName(w32.HWND(hwnd)) == "Chrome_WidgetWin_0" && w32.IsWindowVisible(w32.HWND(hwnd)) {
			webviewVisible = true
		}
		return 1
	})
	enumerateChildren := user32.NewProc("EnumChildWindows")
	wait("visible WebView2 controller", func() bool {
		enumerateChildren.Call(uintptr(window), childCallback, 0)
		return webviewVisible
	})
	t.Log("native window and WebView2 visible")
	launch, err := os.ReadFile(filepath.Join(data, "launch-url.txt"))
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(string(launch))
	if err != nil {
		t.Fatal("invalid launch URL")
	}
	token := strings.TrimPrefix(u.Fragment, "token=")
	u.Fragment = ""
	client := &http.Client{Timeout: 5 * time.Second}
	requestAPI := func(method, path string, body any) core.State {
		t.Helper()
		var payload []byte
		if body != nil {
			payload, err = json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
		}
		endpoint := *u
		endpoint.Path = path
		req, err := http.NewRequest(method, endpoint.String(), bytes.NewReader(payload))
		if err != nil {
			t.Fatal("invalid API request")
		}
		req.AddCookie(&http.Cookie{Name: "loopbee", Value: token})
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal("background API unavailable")
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("background API status %d", resp.StatusCode)
		}
		var state core.State
		if err := json.NewDecoder(resp.Body).Decode(&state); err != nil {
			t.Fatal("invalid API response")
		}
		return state
	}
	checkAPI := func() { requestAPI(http.MethodGet, "/api/state", nil) }
	checkAPI()
	t.Log("API ready")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	second := exec.CommandContext(ctx, binary, "-nogui", "-data", data)
	second.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := second.Run(); err == nil || ctx.Err() != nil {
		t.Fatal("second instance must fail promptly while first remains usable")
	}
	checkAPI()
	t.Log("duplicate instance blocked")
	w32.PostMessage(window, w32.WM_CLOSE, 0, 0)
	wait("close hides window", func() bool { return !w32.IsWindowVisible(window) })
	checkAPI()
	t.Log("window hidden; API remains live")
	find := user32.NewProc("FindWindowExW")
	var tray w32.HWND
	wait("registered tray icon", func() bool {
		var after uintptr
		for {
			hwnd, _, _ := find.Call(^uintptr(2), after, 0, 0) // HWND_MESSAGE (-3)
			if hwnd == 0 {
				return false
			}
			after = hwnd
			_, pid := w32.GetWindowThreadProcessId(w32.HWND(hwnd))
			if pid == cmd.Process.Pid {
				// The Wails HRESULT wrapper can return (nil, nil) for an unregistered HWND.
				if bounds, err := w32.GetSystrayBounds(w32.HWND(hwnd), 1); err == nil && bounds != nil {
					tray = w32.HWND(hwnd)
					return true
				}
			}
		}
	})
	t.Log("tray registered")
	// Wails beta.24 delivers native tray activation through WM_USER+1.
	w32.PostMessage(tray, w32.WM_USER+1, 0, w32.WM_LBUTTONUP)
	wait("tray restores window", func() bool { return w32.IsWindowVisible(window) })
	checkAPI()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal("Node is required for the controlled cancellation fixture")
	}
	pidFile := filepath.Join(data, "verify-child.pid")
	state := requestAPI(http.MethodPost, "/api/loops", core.Loop{
		Name: "Quit cancellation", Directory: data, Agent: "codex", Sandbox: "read-only", Private: true,
		MaxRounds: 1, WallMinutes: 1, StallRounds: 1, AllowedTools: []string{node},
		Steps: []core.Step{{Kind: "verify", Name: "Controlled long-running Node process", Argv: []string{
			node, "-e", `require('node:fs').writeFileSync(process.argv[1], String(process.pid)); setInterval(() => {}, 1000)`, pidFile,
		}}},
	})
	if len(state.Loops) != 1 {
		t.Fatal("controlled loop was not created")
	}
	state = requestAPI(http.MethodPost, "/api/loops/"+state.Loops[0].ID+"/run", struct{}{})
	if len(state.Runs) != 1 {
		t.Fatal("controlled run was not started")
	}
	runID := state.Runs[0].ID
	var childPID uint64
	wait("controlled verify process", func() bool {
		payload, err := os.ReadFile(pidFile)
		if err != nil {
			return false
		}
		childPID, err = strconv.ParseUint(string(payload), 10, 32)
		return err == nil && childPID != 0
	})
	childHandle, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_TERMINATE, false, uint32(childPID))
	if err != nil {
		t.Fatal("controlled child exited before Quit")
	}
	t.Cleanup(func() {
		if result, _ := windows.WaitForSingleObject(childHandle, 0); result == uint32(windows.WAIT_TIMEOUT) {
			windows.TerminateProcess(childHandle, 1)
		}
		windows.CloseHandle(childHandle)
	})
	state = requestAPI(http.MethodGet, "/api/state", nil)
	if state.Runs[0].ID != runID || state.Runs[0].Status != "running" {
		t.Fatal("controlled run must be running when Quit is requested")
	}
	t.Log("controlled verify process running")
	descendants = desktopDescendants(t, uint32(cmd.Process.Pid))
	if len(descendants) == 0 {
		t.Fatal("owned child processes were not discovered")
	}
	// Native popup IDs start at MenuItemMsgID; Quit is our third menu entry.
	w32.PostMessage(tray, w32.WM_COMMAND, application.MenuItemMsgID+3, 0)
	select {
	case err := <-finished:
		if err != nil {
			t.Fatalf("tray Quit exited with error: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("tray Quit did not stop the app")
	}
	if result, err := windows.WaitForSingleObject(childHandle, 5000); err != nil || result != windows.WAIT_OBJECT_0 {
		t.Fatal("Quit left the controlled verify process running")
	}
	db, err := sql.Open("sqlite", filepath.Join(data, "loopbee.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var persistedJSON string
	if err := db.QueryRow("SELECT data FROM objects WHERE kind = 'run' AND id = ?", runID).Scan(&persistedJSON); err != nil {
		t.Fatal(err)
	}
	var persisted core.Run
	if err := json.Unmarshal([]byte(persistedJSON), &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted.Status != "cancelled" || persisted.Finished == nil {
		t.Fatalf("Quit persisted run status %q, want cancelled with finish time", persisted.Status)
	}
	t.Log("PASS: packaged GUI visible, duplicate blocked, close hides, API stays live, tray restores, Quit cancels child and persists cancelled run")
}

func desktopDescendants(t *testing.T, parentPID uint32) []windows.Handle {
	t.Helper()
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	var entries []windows.ProcessEntry32
	for err = windows.Process32First(snapshot, &entry); err == nil; err = windows.Process32Next(snapshot, &entry) {
		entries = append(entries, entry)
	}
	if err != windows.ERROR_NO_MORE_FILES {
		t.Fatal(err)
	}
	owned := map[uint32]bool{parentPID: true}
	var handles []windows.Handle
	for {
		found := false
		for _, process := range entries {
			if owned[process.ProcessID] || !owned[process.ParentProcessID] {
				continue
			}
			handle, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_TERMINATE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, process.ProcessID)
			if err != nil {
				continue
			}
			var info windows.PROCESS_BASIC_INFORMATION
			if err := windows.NtQueryInformationProcess(handle, windows.ProcessBasicInformation, unsafe.Pointer(&info), uint32(unsafe.Sizeof(info)), nil); err != nil || uint32(info.InheritedFromUniqueProcessId) != process.ParentProcessID {
				windows.CloseHandle(handle)
				continue
			}
			handles = append(handles, handle)
			owned[process.ProcessID] = true
			found = true
		}
		if !found {
			return handles
		}
	}
}
