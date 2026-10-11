package core

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/zalando/go-keyring"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

func TestWindowsAutostartCommandRoundTrip(t *testing.T) {
	args := []string{`C:\Program Files\Loopbee\loopbee.exe`, "-data", `D:\资料 & $()\trailing\`, "-nogui"}
	command, err := autostartCommand(args)
	if err != nil {
		t.Fatal(err)
	}
	got, err := windows.DecomposeCommandLine(command)
	if err != nil || !reflect.DeepEqual(got, args) {
		t.Fatalf("command round trip: got %q: %v", got, err)
	}
	if _, err = autostartCommand([]string{"loopbee.exe", strings.Repeat("x", 260)}); err == nil {
		t.Fatal("Windows Run command length limit was not enforced")
	}
	if _, err = autostartCommand([]string{"loopbee.exe", strings.Repeat("🐝", 130)}); err == nil {
		t.Fatal("Windows Run command must count UTF-16 code units")
	}
}

func TestWindowsKeepAwakeThreadLifecycle(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan error, 1)
	done := make(chan error, 1)
	var flags []uintptr
	var threadIDs []uint32
	go func() {
		done <- keepAwakeThread(ctx, ready, func(flag uintptr) error {
			flags = append(flags, flag)
			threadIDs = append(threadIDs, windows.GetCurrentThreadId())
			return nil
		})
	}()
	if err := <-ready; err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("sleep request was not released on cancellation")
	}
	if !reflect.DeepEqual(flags, []uintptr{executionContinuous | executionSystemRequired, executionContinuous}) || len(threadIDs) != 2 || threadIDs[0] != threadIDs[1] {
		t.Fatalf("request and cleanup must use the same OS thread: flags %v, threads %v", flags, threadIDs)
	}

	want := errors.New("sleep request denied")
	if err := keepAwakeThread(context.Background(), ready, func(uintptr) error { return want }); err != nil {
		t.Fatal(err)
	}
	if err := <-ready; !errors.Is(err, want) {
		t.Fatalf("startup failure not propagated: %v", err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	go func() {
		done <- keepAwakeThread(ctx, ready, func(flag uintptr) error {
			if flag == executionContinuous {
				return want
			}
			return nil
		})
	}()
	if err := <-ready; err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := <-done; !errors.Is(err, want) {
		t.Fatalf("cleanup failure not propagated: %v", err)
	}
}

func TestWindowsToastContentIsData(t *testing.T) {
	text := "中文 🐝 < & ' \" ` $(throw 'injected')\nsecond line"
	document, err := windowsToastXML(text)
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Text []string `xml:"visual>binding>text"`
	}
	if err = xml.Unmarshal(document, &parsed); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(parsed.Text, []string{Brand, text}) {
		t.Fatalf("notification content changed: %q", parsed.Text)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if status, err := sendDesktopNotification(ctx, text); status != "failed" || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled notification: %s %v", status, err)
	}
}

func TestWindowsPlatformIntegration(t *testing.T) {
	if os.Getenv("LOOPBEE_WINDOWS_INTEGRATION") != "1" {
		t.Skip("set LOOPBEE_WINDOWS_INTEGRATION=1 for native registry, power and Credential Manager checks")
	}
	t.Run("Autostart", func(t *testing.T) {
		key, _, err := registry.CreateKey(registry.CURRENT_USER, autostartRegistryPath, registry.QUERY_VALUE|registry.SET_VALUE)
		if err != nil {
			t.Fatal(err)
		}
		defer key.Close()
		old, kind, oldErr := key.GetStringValue(Brand)
		if oldErr != nil && !errors.Is(oldErr, registry.ErrNotExist) {
			t.Fatal(oldErr)
		}
		defer func() {
			var err error
			switch {
			case errors.Is(oldErr, registry.ErrNotExist):
				err = key.DeleteValue(Brand)
			case kind == registry.EXPAND_SZ:
				err = key.SetExpandStringValue(Brand, old)
			default:
				err = key.SetStringValue(Brand, old)
			}
			if err != nil && !errors.Is(err, registry.ErrNotExist) {
				t.Errorf("restore startup registration: %v", err)
			}
		}()
		data := t.TempDir()
		if err = configureAutostart(true, data); err != nil {
			t.Fatal(err)
		}
		command, _, err := key.GetStringValue(Brand)
		if err != nil {
			t.Fatal(err)
		}
		args, err := windows.DecomposeCommandLine(command)
		executable, _ := os.Executable()
		if err != nil || !reflect.DeepEqual(args, startupArguments(executable, data)) {
			t.Fatalf("registered arguments %q: %v", args, err)
		}
		if err = configureAutostart(false, data); err != nil {
			t.Fatal(err)
		}
		if _, _, err = key.GetStringValue(Brand); !errors.Is(err, registry.ErrNotExist) {
			t.Fatalf("startup registration still exists: %v", err)
		}
		a := testApp(t)
		a.cancel()
		settings := a.Settings()
		settings.StartAtLogin, settings.KeepAwake = true, true
		if err = a.SaveSettings(settings); !errors.Is(err, context.Canceled) {
			t.Fatalf("closed app must reject the sleep request: %v", err)
		}
		if _, _, err = key.GetStringValue(Brand); !errors.Is(err, registry.ErrNotExist) {
			t.Fatalf("failed settings update did not undo startup registration: %v", err)
		}
		var persisted Settings
		if err = a.get("settings", "general", &persisted); err != nil || persisted.StartAtLogin || persisted.KeepAwake {
			t.Fatalf("failed settings were persisted: %v", err)
		}
	})
	t.Run("KeepAwake", func(t *testing.T) {
		a := testApp(t)
		settings := a.Settings()
		settings.KeepAwake = true
		if err := a.SaveSettings(settings); err != nil {
			t.Fatal(err)
		}
		settings.KeepAwake = false
		if err := a.SaveSettings(settings); err != nil {
			t.Fatal(err)
		}
		if a.awakeCancel != nil {
			t.Fatal("sleep request not cleared")
		}
		settings.KeepAwake = true
		if err := a.SaveSettings(settings); err != nil {
			t.Fatal(err)
		}
		// testApp cleanup closes the app and joins the native sleep-request thread.
	})
	t.Run("CredentialManager", func(t *testing.T) {
		a := testApp(t)
		settings := a.Settings()
		secret := "loopbee-integration-secret-" + newID()
		settings.Channels = []Channel{{Kind: "telegram", Secret: secret}}
		defer func() {
			if err := keyring.Delete(Brand, settings.Publisher+"/telegram/secret"); err != nil && !errors.Is(err, keyring.ErrNotFound) {
				t.Errorf("remove integration-test credential: %v", err)
			}
		}()
		if err := a.SaveSettings(settings); err != nil {
			t.Fatal(err)
		}
		stored := a.Settings()
		if !strings.HasPrefix(stored.Channels[0].Secret, "keyring:") {
			t.Fatal("credential was stored inline")
		}
		got, err := resolveSecret(stored.Channels[0].Secret)
		if err != nil || got != secret {
			t.Fatalf("Credential Manager round trip failed: %v", err)
		}
		var persisted Settings
		if err = a.get("settings", "general", &persisted); err != nil {
			t.Fatal(err)
		}
		data, _ := json.Marshal(persisted)
		if strings.Contains(string(data), secret) {
			t.Fatal("plaintext credential leaked into settings")
		}
	})
}

func TestWindowsToastIntegration(t *testing.T) {
	if os.Getenv("LOOPBEE_WINDOWS_TOAST_TEST") != "1" {
		t.Skip("set LOOPBEE_WINDOWS_TOAST_TEST=1 to submit one native local test notification")
	}
	status, err := sendDesktopNotification(context.Background(), "Loopbee Windows desktop test · 中文 < & >")
	if err != nil || status != "sent" {
		t.Fatalf("native toast: %s %v", status, err)
	}
}
