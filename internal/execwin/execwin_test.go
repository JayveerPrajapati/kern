package execwin

import "testing"

func TestUnwrap(t *testing.T) {
	tests := []struct {
		name    string
		tokens  []string
		inner   string
		encoded bool
		ok      bool
	}{
		{"cmd /c", []string{"cmd", "/c", "dir", "/s"}, "dir /s", false, true},
		{"cmd /k", []string{"CMD.EXE", "/K", "echo hi"}, "echo hi", false, true},
		{"cmd no switch", []string{"cmd", "dir"}, "", false, false},
		{"cmd /c missing body", []string{"cmd", "/c"}, "", false, false},
		{"powershell -Command", []string{"powershell", "-Command", "Write-Host hi"}, "Write-Host hi", false, true},
		{"pwsh -c", []string{"pwsh.exe", "-c", "Get-Process"}, "Get-Process", false, true},
		{"powershell -EncodedCommand", []string{"powershell", "-EncodedCommand", "ZQBjAGgAbwA="}, "", true, true},
		{"powershell -enc", []string{"pwsh", "-enc", "AAAA"}, "", true, true},
		{"powershell no command", []string{"powershell", "-NoProfile"}, "", false, false},
		{"not a wrapper", []string{"bash", "-c", "ls"}, "", false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			inner, encoded, ok := Unwrap(tc.tokens[0], tc.tokens)
			if ok != tc.ok || encoded != tc.encoded || inner != tc.inner {
				t.Fatalf("Unwrap(%v) = (%q, %v, %v), want (%q, %v, %v)",
					tc.tokens, inner, encoded, ok, tc.inner, tc.encoded, tc.ok)
			}
		})
	}
}

func TestDestructive(t *testing.T) {
	tests := []struct {
		name   string
		tokens []string
		want   bool
	}{
		{"del", []string{"del", "a.txt"}, true},
		{"erase", []string{"erase", "a.txt"}, true},
		{"rd", []string{"rd", "/s", "dir"}, true},
		{"rmdir", []string{"rmdir", "dir"}, true},
		{"Remove-Item", []string{"Remove-Item", "x"}, true},
		{"ri alias", []string{"ri", "x"}, true},
		{"format", []string{"format", "D:"}, true},
		{"diskpart", []string{"diskpart"}, true},
		{"takeown", []string{"takeown", "/f", "x"}, true},
		{"icacls", []string{"icacls", "x", "/grant", "u:f"}, true},
		{"Set-ExecutionPolicy", []string{"Set-ExecutionPolicy", "Bypass"}, true},
		{"iex", []string{"iex", "(New-Object Net.WebClient).DownloadString('x')"}, true},
		{"reg delete", []string{"reg", "delete", `HKCU\X`, "/f"}, true},
		{"REG ADD", []string{"REG", "ADD", `HKCU\X`, "/v", "y"}, true},
		{"reg query is read-only", []string{"reg", "query", `HKCU\X`}, false},
		{"reg with no args", []string{"reg"}, false},
		{"sc stop", []string{"sc", "stop", "svc"}, true},
		{"sc query is read-only", []string{"sc", "query", "svc"}, false},
		{"net user /add", []string{"net", "user", "bob", "/add"}, true},
		{"net user read", []string{"net", "user", "bob"}, false},
		{"net localgroup /delete", []string{"net", "localgroup", "admins", "bob", "/delete"}, true},
		{"unlisted command", []string{"echo", "hello"}, false},
		{"empty", nil, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			name := "echo"
			if len(tc.tokens) > 0 {
				name = tc.tokens[0]
			}
			reason, got := Destructive(name, tc.tokens)
			if got != tc.want {
				t.Fatalf("Destructive(%v) = (%q, %v), want %v", tc.tokens, reason, got, tc.want)
			}
			if got && reason == "" {
				t.Fatalf("Destructive(%v) returned true with an empty reason", tc.tokens)
			}
		})
	}
}

func TestNetworkFetch(t *testing.T) {
	for _, name := range []string{"Invoke-WebRequest", "iwr", "Start-BitsTransfer", "IWR"} {
		if !NetworkFetch(name) {
			t.Fatalf("NetworkFetch(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"curl", "wget", "echo"} {
		if NetworkFetch(name) {
			t.Fatalf("NetworkFetch(%q) = true, want false", name)
		}
	}
}
