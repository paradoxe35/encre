package platform

import (
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"strings"
	"unicode/utf16"
)

// Text only: the header Windows draws from the registered shortcut already carries the icon.
const toastScript = `$title = '%s'
$content = '%s'
[Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime] > $null
$template = [Windows.UI.Notifications.ToastNotificationManager]::GetTemplateContent([Windows.UI.Notifications.ToastTemplateType]::ToastText02)
$toastXml = [xml] $template.GetXml()
$toastXml.GetElementsByTagName("text")[0].AppendChild($toastXml.CreateTextNode($title)) > $null
$toastXml.GetElementsByTagName("text")[1].AppendChild($toastXml.CreateTextNode($content)) > $null
$xml = New-Object Windows.Data.Xml.Dom.XmlDocument
$xml.LoadXml($toastXml.OuterXml)
$toast = [Windows.UI.Notifications.ToastNotification]::new($xml)
[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier('%s').Show($toast)
`

// A toast shows a few lines; the cap keeps the encoded script within the 32767-character command line.
const (
	maxToastTitle   = 200
	maxToastContent = 2000
)

func toastScriptFor(id, title, content string) string {
	return fmt.Sprintf(toastScript, psQuote(truncateRunes(title, maxToastTitle)), psQuote(truncateRunes(content, maxToastContent)), psQuote(id))
}

// PowerShell ends a single-quoted string at any of these, so each is doubled.
var psQuoter = strings.NewReplacer(
	"'", "''",
	"‘", "‘‘",
	"’", "’’",
	"‚", "‚‚",
	"‛", "‛‛",
)

func psQuote(s string) string {
	return psQuoter.Replace(s)
}

// encodePowerShell is the -EncodedCommand form: base64 of UTF-16LE, read the same whatever the code page.
func encodePowerShell(script string) string {
	units := utf16.Encode([]rune(script))
	raw := make([]byte, 2*len(units))
	for i, unit := range units {
		binary.LittleEndian.PutUint16(raw[2*i:], unit)
	}
	return base64.StdEncoding.EncodeToString(raw)
}

func truncateRunes(s string, limit int) string {
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit-1]) + "…"
}
