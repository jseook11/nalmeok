package notify

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"strings"
	"unicode/utf16"
)

// Windows toast pieces. They are plain functions so the macOS test run
// checks them too; notify_windows.go runs the result.

// windowsAppID is the AppUserModelID toasts are shown under. It matches the
// macOS bundle identifier.
const windowsAppID = "kr.nalmeok.app"

// powershellAppID is PowerShell's own registered ID, used only when the
// registry entry for windowsAppID cannot be written.
const powershellAppID = `{1AC14E77-02E7-4E5D-B744-2EB1AE5198B7}\WindowsPowerShell\v1.0\powershell.exe`

// toastScript returns the PowerShell that shows message as a toast.
//
// An unpackaged app has no Start-menu shortcut to carry an AppUserModelID,
// so the script registers one under HKCU\Software\Classes\AppUserModelId
// (display name and, when given, icon). Windows 10 and 11 then show toasts
// for that ID with the Nalmeok name and icon. ToastText01 holds one text
// line; the header above it already names the app.
func toastScript(appID, displayName, iconPath, message string) string {
	var b strings.Builder
	b.WriteString("$ErrorActionPreference = 'Stop'\n")
	b.WriteString("$id = " + psQuote(appID) + "\n")
	b.WriteString("try {\n")
	b.WriteString("  $k = 'HKCU:\\Software\\Classes\\AppUserModelId\\' + $id\n")
	b.WriteString("  if (-not (Test-Path $k)) { New-Item -Path $k -Force | Out-Null }\n")
	b.WriteString("  New-ItemProperty -Path $k -Name DisplayName -Value " + psQuote(displayName) + " -PropertyType String -Force | Out-Null\n")
	if iconPath != "" {
		b.WriteString("  New-ItemProperty -Path $k -Name IconUri -Value " + psQuote(iconPath) + " -PropertyType String -Force | Out-Null\n")
	}
	b.WriteString("} catch { $id = " + psQuote(powershellAppID) + " }\n")
	writeToastShow(&b, message)
	return b.String()
}

// packagedToastScript is toastScript for the MSIX build. The package already
// gives the app an AppUserModelID with its name and logo (Start-menu entry),
// so nothing is registered: HKCU writes from a packaged app are virtualized
// and the notification platform would not see them.
func packagedToastScript(appID, message string) string {
	var b strings.Builder
	b.WriteString("$ErrorActionPreference = 'Stop'\n")
	b.WriteString("$id = " + psQuote(appID) + "\n")
	writeToastShow(&b, message)
	return b.String()
}

// writeToastShow appends the lines that show message under the app ID in $id.
func writeToastShow(b *strings.Builder, message string) {
	b.WriteString("[Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime] | Out-Null\n")
	b.WriteString("$x = [Windows.UI.Notifications.ToastNotificationManager]::GetTemplateContent([Windows.UI.Notifications.ToastTemplateType]::ToastText01)\n")
	b.WriteString("$x.GetElementsByTagName('text').Item(0).AppendChild($x.CreateTextNode(" + psQuote(message) + ")) | Out-Null\n")
	b.WriteString("[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier($id).Show([Windows.UI.Notifications.ToastNotification]::new($x))\n")
}

// psQuote makes a PowerShell single-quoted string. PowerShell also treats
// the typographic single quotes as quote marks; each is escaped by doubling.
func psQuote(s string) string {
	var b strings.Builder
	b.WriteByte('\'')
	for _, r := range s {
		switch r {
		case '\'', '‘', '’', '‚', '‛':
			b.WriteRune(r)
		}
		b.WriteRune(r)
	}
	b.WriteByte('\'')
	return b.String()
}

// encodePowerShell turns a script into the -EncodedCommand argument
// (base64 of UTF-16LE). Nothing in it needs quoting on the command line, so
// Korean names and quotes in file names pass through untouched.
func encodePowerShell(script string) string {
	units := utf16.Encode([]rune(script))
	buf := make([]byte, 2*len(units))
	for i, u := range units {
		binary.LittleEndian.PutUint16(buf[2*i:], u)
	}
	return base64.StdEncoding.EncodeToString(buf)
}

// largestPNG returns the biggest PNG-encoded image inside an .ico file, or
// nil. The toast icon is a file path, and the .ico the exe already embeds
// stores its images as PNG.
func largestPNG(ico []byte) []byte {
	if len(ico) < 6 || binary.LittleEndian.Uint16(ico[0:]) != 0 || binary.LittleEndian.Uint16(ico[2:]) != 1 {
		return nil
	}
	count := int(binary.LittleEndian.Uint16(ico[4:]))
	sig := []byte("\x89PNG\r\n\x1a\n")
	var best []byte
	bestSide := -1
	for i := 0; i < count; i++ {
		e := 6 + 16*i
		if e+16 > len(ico) {
			break
		}
		side := int(ico[e])
		if side == 0 {
			side = 256
		}
		size := int(binary.LittleEndian.Uint32(ico[e+8:]))
		off := int(binary.LittleEndian.Uint32(ico[e+12:]))
		if size <= 0 || off < 0 || off > len(ico) || size > len(ico)-off {
			continue
		}
		data := ico[off : off+size]
		if !bytes.HasPrefix(data, sig) || side <= bestSide {
			continue
		}
		best, bestSide = data, side
	}
	return best
}
