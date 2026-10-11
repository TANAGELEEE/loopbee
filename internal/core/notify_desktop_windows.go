package core

import (
	"context"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const desktopAppID = "app.loopbee.desktop"

// Notification content travels over stdin; it is never interpolated into PowerShell code.
// Windows creates the notification identity on first Show, before Setting can be queried.
const windowsToastScript = `$ErrorActionPreference = 'Stop'
try {
  [Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime] > $null
  [Windows.Data.Xml.Dom.XmlDocument, Windows.Data.Xml.Dom.XmlDocument, ContentType = WindowsRuntime] > $null
  $document = New-Object Windows.Data.Xml.Dom.XmlDocument
  $document.LoadXml([Text.Encoding]::UTF8.GetString([Convert]::FromBase64String([Console]::In.ReadToEnd())))
  $notifier = [Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier('app.loopbee.desktop')
  $toast = [Windows.UI.Notifications.ToastNotification]::new($document)
  $notifier.Show($toast)
  if ($notifier.get_Setting() -ne 'Enabled') { throw 'Windows notifications are disabled for Loopbee or by system policy' }
} catch { [Console]::Error.WriteLine($_.Exception.Message); exit 1 }
`

func windowsToastXML(text string) ([]byte, error) {
	var document struct {
		XMLName xml.Name `xml:"toast"`
		Visual  struct {
			Binding struct {
				Template string   `xml:"template,attr"`
				Text     []string `xml:"text"`
			} `xml:"binding"`
		} `xml:"visual"`
	}
	document.Visual.Binding.Template = "ToastGeneric"
	document.Visual.Binding.Text = []string{Brand, text}
	return xml.Marshal(document)
}

func sendDesktopNotification(ctx context.Context, text string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "failed", err
	}
	document, err := windowsToastXML(text)
	if err != nil {
		return "failed", err
	}
	key, _, err := registry.CreateKey(registry.CURRENT_USER, `Software\Classes\AppUserModelId\`+desktopAppID, registry.SET_VALUE)
	if err != nil {
		return "failed", fmt.Errorf("register desktop notification: %w", err)
	}
	err = key.SetStringValue("DisplayName", Brand)
	key.Close()
	if err != nil {
		return "failed", err
	}
	systemDirectory, err := windows.GetSystemDirectory()
	if err != nil {
		return "failed", err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	powershell := filepath.Join(systemDirectory, "WindowsPowerShell", "v1.0", "powershell.exe")
	output, err := runCommandInput(ctx, "", []string{powershell, "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-Command", windowsToastScript}, nil, strings.NewReader(base64.StdEncoding.EncodeToString(document)))
	if err != nil {
		if ctx.Err() != nil {
			return "unknown", errors.New("desktop notification result unknown; Windows notification process stopped")
		}
		detail := strings.TrimSpace(string(output))
		if detail == "" {
			detail = err.Error()
		}
		return "failed", fmt.Errorf("desktop notification failed: %s", detail)
	}
	// Windows accepts the toast; Focus Assist may still suppress the banner.
	return "sent", nil
}
