//go:build darwin

package core

import (
	"context"
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

func (a *App) setKeepAwake(enabled bool) error {
	if a.awakeCancel != nil {
		a.awakeCancel()
		a.awakeCancel = nil
	}
	if !enabled {
		return nil
	}
	ctx, cancel := context.WithCancel(a.ctx)
	cmd := exec.CommandContext(ctx, "/usr/bin/caffeinate", "-i", "-w", strconv.Itoa(os.Getpid()))
	configureProcess(cmd)
	if err := cmd.Start(); err != nil {
		cancel()
		return err
	}
	a.awakeCancel = cancel
	a.wg.Add(1)
	go func() { defer a.wg.Done(); cmd.Wait() }()
	return nil
}
func configureAutostart(enabled bool, data string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	dir := filepath.Join(home, "Library", "LaunchAgents")
	file := filepath.Join(dir, "app.loopbee.agent.plist")
	if !enabled {
		if err = os.Remove(file); os.IsNotExist(err) {
			return nil
		}
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	escape := func(s string) string { var b strings.Builder; xml.EscapeText(&b, []byte(s)); return b.String() }
	var arguments strings.Builder
	for _, arg := range startupArguments(executable, data) {
		fmt.Fprintf(&arguments, "<string>%s</string>", escape(arg))
	}
	body := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?><!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd"><plist version="1.0"><dict><key>Label</key><string>app.loopbee.agent</string><key>ProgramArguments</key><array>%s</array><key>RunAtLoad</key><true/></dict></plist>`, arguments.String())
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	return os.WriteFile(file, []byte(body), 0600)
}
