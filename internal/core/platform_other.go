//go:build !darwin && !windows

package core

import "errors"

func (a *App) setKeepAwake(enabled bool) error {
	if enabled {
		return errors.New("keep awake is supported on macOS and Windows only")
	}
	return nil
}

func configureAutostart(enabled bool, data string) error {
	if enabled {
		return errors.New("start at login is supported on macOS and Windows only")
	}
	return nil
}
