package core

import (
	"context"
	"errors"
	"time"
)

func sendDesktopNotification(ctx context.Context, text string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	script := `on run argv
display notification (item 1 of argv) with title (item 2 of argv)
end run`
	if _, err := runCommand(ctx, "", []string{"osascript", "-e", script, text, Brand}, nil); err != nil {
		return "failed", errors.New("desktop notification failed")
	}
	return "sent", nil
}
