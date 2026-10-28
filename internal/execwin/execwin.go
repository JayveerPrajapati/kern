// Package execwin holds the Windows-native command families the exec firewall
// must recognize: destructive cmd/PowerShell commands, shell-wrapper recursion
// (cmd /c, powershell -Command), and PowerShell downloaders. It is separated
// from internal/execclassify to keep that package under its ARCHITECTURE.md LOC
// cap (extraction, never cap-raising).
package execwin

import "strings"

// Unwrap returns the inner command text for a Windows shell wrapper. encoded
// reports -EncodedCommand (opaque script text, always treated as destructive).
// ok is false when name is not a wrapper.
func Unwrap(name string, tokens []string) (inner string, encoded bool, ok bool) {
	switch strings.ToLower(name) {
	case "cmd", "cmd.exe":
		if len(tokens) >= 3 && (strings.EqualFold(tokens[1], "/c") || strings.EqualFold(tokens[1], "/k")) {
			return strings.Join(tokens[2:], " "), false, true
		}
	case "powershell", "powershell.exe", "pwsh", "pwsh.exe":
		for i := 1; i < len(tokens); i++ {
			switch strings.ToLower(tokens[i]) {
			case "-encodedcommand", "-enc", "-ec":
				return "", true, true
			case "-command", "-c":
				if i+1 < len(tokens) {
					return strings.Join(tokens[i+1:], " "), false, true
				}
			}
		}
	}
	return "", false, false
}

// Destructive reports whether a Windows-native command is destructive, with a
// human-readable reason.
func Destructive(name string, tokens []string) (string, bool) {
	if len(tokens) == 0 {
		return "", false
	}
	rest := tokens[1:]
	switch strings.ToLower(name) {
	case "del", "erase", "rd", "rmdir":
		return name + " deletes files/directories (Windows, no recycle bin)", true
	case "remove-item", "ri", "rmdir-item":
		return name + " deletes files/directories (PowerShell)", true
	case "format", "diskpart":
		return name + " can destroy a volume", true
	case "takeown", "icacls":
		return name + " changes ownership/permissions", true
	case "set-executionpolicy":
		return name + " changes the PowerShell execution policy", true
	case "invoke-expression", "iex":
		return name + " executes arbitrary text", true
	case "reg", "reg.exe":
		if len(rest) > 0 {
			switch strings.ToLower(rest[0]) {
			case "delete", "add", "import", "load", "restore", "copy", "save":
				return "reg " + rest[0] + " modifies the registry", true
			}
		}
	case "sc", "sc.exe":
		if len(rest) > 0 {
			switch strings.ToLower(rest[0]) {
			case "stop", "start", "delete", "create", "config", "failure", "sdset":
				return "sc " + rest[0] + " changes a Windows service", true
			}
		}
	case "net", "net.exe":
		if len(rest) > 0 {
			sub := strings.ToLower(rest[0])
			if sub == "user" || sub == "localgroup" || sub == "group" || sub == "share" {
				for _, t := range rest[1:] {
					lt := strings.ToLower(t)
					if strings.HasPrefix(lt, "/add") || strings.HasPrefix(lt, "/delete") || strings.HasPrefix(lt, "/grant") || strings.HasPrefix(lt, "/revoke") {
						return "net " + sub + " modifies accounts/shares", true
					}
				}
			}
		}
	}
	return "", false
}

// NetworkFetch reports whether name is a PowerShell downloader.
func NetworkFetch(name string) bool {
	switch strings.ToLower(name) {
	case "invoke-webrequest", "iwr", "start-bitstransfer":
		return true
	}
	return false
}
