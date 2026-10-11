package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"unicode/utf16"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	executionContinuous     = 0x80000000
	executionSystemRequired = 0x00000001
	autostartRegistryPath   = `Software\Microsoft\Windows\CurrentVersion\Run`
)

var setThreadExecutionState = windows.NewLazySystemDLL("kernel32.dll").NewProc("SetThreadExecutionState")

func windowsExecutionState(flags uintptr) error {
	previous, _, err := setThreadExecutionState.Call(flags)
	if previous == 0 {
		return fmt.Errorf("SetThreadExecutionState failed: %v", err)
	}
	return nil
}

func keepAwakeThread(ctx context.Context, ready chan<- error, setState func(uintptr) error) error {
	// Windows associates the sleep request with the calling OS thread.
	runtime.LockOSThread()
	releaseFailed := false
	defer func() {
		// A failed reset must terminate this OS thread instead of returning it to Go's pool.
		if !releaseFailed {
			runtime.UnlockOSThread()
		}
	}()
	if err := ctx.Err(); err != nil {
		ready <- err
		return nil
	}
	err := setState(executionContinuous | executionSystemRequired)
	ready <- err
	if err != nil {
		return nil
	}
	<-ctx.Done()
	err = setState(executionContinuous)
	releaseFailed = err != nil
	return err
}

func (a *App) setKeepAwake(enabled bool) error {
	if !enabled {
		if a.awakeCancel != nil {
			a.awakeCancel()
			a.awakeCancel = nil
		}
		return nil
	}
	if a.awakeCancel != nil {
		return nil
	}
	ctx, cancel := context.WithCancel(a.ctx)
	ready := make(chan error, 1)
	done := make(chan struct{})
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		defer close(done)
		if err := keepAwakeThread(ctx, ready, windowsExecutionState); err != nil {
			a.event("", "system.error", err.Error())
		}
	}()
	if err := <-ready; err != nil {
		cancel()
		<-done
		return err
	}
	a.awakeCancel = func() { cancel(); <-done }
	return nil
}

func autostartCommand(args []string) (string, error) {
	command := windows.ComposeCommandLine(args)
	// The Windows Run registry contract limits command lines to 260 characters.
	if len(utf16.Encode([]rune(command))) > 260 {
		return "", errors.New("login command exceeds Windows' 260-character limit; use shorter application and data paths")
	}
	return command, nil
}

func configureAutostart(enabled bool, data string) error {
	if !enabled {
		key, err := registry.OpenKey(registry.CURRENT_USER, autostartRegistryPath, registry.SET_VALUE)
		if errors.Is(err, registry.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		defer key.Close()
		if err = key.DeleteValue(Brand); errors.Is(err, registry.ErrNotExist) {
			return nil
		}
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	command, err := autostartCommand(startupArguments(executable, data))
	if err != nil {
		return err
	}
	key, _, err := registry.CreateKey(registry.CURRENT_USER, autostartRegistryPath, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	return key.SetStringValue(Brand, command)
}
