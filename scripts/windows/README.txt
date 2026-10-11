Loopbee for Windows x64 (unsigned portable build)

Requires Windows 10/11 and Microsoft Edge WebView2 Evergreen Runtime.
If WebView2 is missing, install it from Microsoft's official page:
https://developer.microsoft.com/microsoft-edge/webview2/

Extract this ZIP to a permanent folder, then open loopbee.exe.
The desktop app runs without a console window. Closing its window keeps it
running in the notification area. Use the tray menu to reopen or quit it.

loopbee-nogui.exe -nogui runs the scheduler and local API in a console.
The GUI and nogui programs share the same data directory; use only one at a
time, or pass -data with separate directories. The default data location is
%APPDATA%\loopbee. Back up this folder with Loopbee stopped.

Keep the executable in the same location after enabling launch at login.
Disable launch at login before moving or removing the portable folder.

This build is not digitally signed. Windows SmartScreen may warn about an
unrecognized publisher. Verify the archive checksum and its source before use.
No agent credentials, user data, or WebView2 runtime are included in this ZIP.

beectl.exe lets Codex or Claude Code create draft Loops from their own
conversations. Add this folder to PATH, then run:
  beectl skill install
Drafts created this way stay disabled until you review them in Loopbee.
