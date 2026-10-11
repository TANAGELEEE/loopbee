//go:build !darwin && !windows

package core

import (
	"context"
	"errors"
)

func sendDesktopNotification(context.Context, string) (string, error) {
	return "failed", errors.New("native notifications are supported on macOS and Windows only")
}
