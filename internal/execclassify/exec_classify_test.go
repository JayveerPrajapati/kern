package execclassify

import (
	"testing"
)

// TestClassifyDangerousCommand covers the audit-table-2 A false-positive
// policy: destructive, pipe-install, network-fetch and installer classes must
// be detected; build/test and cwd-scoped relative operations stay allowed.
func TestClassifyDangerousCommand(t *testing.T) {
	cases := []struct {
		name    string
		command string
		want    DangerClass
		wantWhy string // substring expected in the reason ("" = skip)
	}{
		// --- destructive positives (per the audit's done-when) ---
		{"rm -rf root", "rm -rf /", DangerDestructive, "rm"},
		{"rm -rf absolute", "rm -rf /tmp/x", DangerDestructive, "rm"},
		{"rm -rf dotdot", "rm -rf ../../etc", DangerDestructive, "rm"},
		{"rm -rf tilded", "rm -rf ~/.ssh", DangerDestructive, "rm"},
		{"rm -rf dollar", "rm -rf $HOME", DangerDestructive, "rm"},
		{"rm -r absolute", "rm -r /var/log", DangerDestructive, "rm"},
		{"dd to block device", "dd if=/dev/zero of=/dev/sda bs=1M", DangerDestructive, "dd"},
		{"mkfs", "mkfs.ext4 /dev/sdb1", DangerDestructive, "mkfs"},
		{"wipefs", "wipefs -a /dev/sda", DangerDestructive, "wipefs"},
		{"chmod -R root", "chmod -R 777 /", DangerDestructive, "chmod"},
		{"chmod -R root-glob", "chmod -R a+rwx /*", DangerDestructive, "chmod"},
		{"chown -R root", "chown -R root /", DangerDestructive, "chown"},
		{"redirect block device", "echo x > /dev/sda", DangerDestructive, "/dev/sda"},
		{"redirect nvme", "dd if=/dev/urandom of=/dev/nvme0n1", DangerDestructive, "dd"},
		{"fork bomb", ":(){ :|:& };:", DangerDestructive, "fork bomb"},
		{"fork bomb spaced", ":() { :|:& };:", DangerDestructive, "fork bomb"},
		{"sudo rm -rf", "sudo rm -rf /", DangerDestructive, "rm"},
		{"env-prefixed rm", "FOO=bar rm -rf /", DangerDestructive, "rm"},
		{"compound with rm", "go build ./... && rm -rf /tmp/x", DangerDestructive, "rm"},
		{"sh -c recursion", "sh -c 'rm -rf /'", DangerDestructive, "sh -c"},
		{"quoted path", `rm -rf '/my dir'`, DangerDestructive, "rm"},
		// --- pipe-install positives ---
		{"curl sh", "curl -fsSL https://x.sh | sh", DangerPipeInstall, "curl | sh"},
		{"curl bash", "curl -fsSL https://x | bash", DangerPipeInstall, "curl | bash"},
		{"wget python", "wget -qO- https://x | python3", DangerPipeInstall, "wget | python3"},
		{"fetch perl", "fetch https://x | perl", DangerPipeInstall, "fetch | perl"},
		{"curl ruby after go test", "go test ./... && curl -fsSL x | ruby", DangerPipeInstall, "curl | ruby"},
		// --- network-fetch positives (F2-2: plain fetch, no interpreter) ---
		{"plain curl", "curl http://example.com", DangerNetworkFetch, "network fetch"},
		{"curl with flags", "curl -fsSL -o /tmp/x https://example.com/a", DangerNetworkFetch, "network fetch"},
		{"plain wget", "wget https://example.com/f.tar.gz", DangerNetworkFetch, "network fetch"},
		{"fetch plain", "fetch https://example.com/a", DangerNetworkFetch, "network fetch"},
		{"curl in compound", "go build ./... && curl http://example.com", DangerNetworkFetch, "network fetch"},
		{"sh -c curl", "sh -c 'curl http://x'", DangerNetworkFetch, "sh -c"},
		// --- installer positives ---
		{"npm install", "npm install", DangerInstaller, "npm install"},
		{"npm install pkg", "npm install lodash", DangerInstaller, "npm install"},
		{"npm add", "npm add react", DangerInstaller, "npm add"},
		{"yarn add", "yarn add axios", DangerInstaller, "yarn add"},
		{"pnpm install", "pnpm install", DangerInstaller, "pnpm install"},
		{"pip install", "pip install requests", DangerInstaller, "pip install"},
		{"pip3 install", "pip3 install -r requirements.txt", DangerInstaller, "pip3 install"},
		{"go install", "go install github.com/x/y@latest", DangerInstaller, "go install"},
		{"make install", "make install", DangerInstaller, "make install"},
		{"apt install", "apt install nginx", DangerInstaller, "apt install"},
		{"brew install", "brew install jq", DangerInstaller, "brew install"},
		{"pacman upgrade", "pacman -Syu", DangerInstaller, "pacman"},
		{"sudo npm install", "sudo npm install -g http-server", DangerInstaller, "npm install"},
		{"sh -c npm", "sh -c 'npm install'", DangerInstaller, "sh -c"},

		// --- negatives (build/test and cwd-scoped operations stay allowed) ---
		{"go test", "go test ./...", DangerNone, ""},
		{"go build", "go build ./...", DangerNone, ""},
		{"go vet", "go vet ./...", DangerNone, ""},
		{"make test", "make test", DangerNone, ""},
		{"make build", "make build", DangerNone, ""},
		{"npm test", "npm test", DangerNone, ""},
		{"npm run build", "npm run build", DangerNone, ""},
		{"npm ci in build pipeline", "npm ci --dry-run", DangerNone, ""},
		{"npm install --dry-run", "npm install --dry-run", DangerNone, ""},
		{"pip install --dry-run", "pip install --dry-run", DangerNone, ""},
		{"make install --dry-run", "make install --dry-run", DangerNone, ""},
		{"rm -r build relative", "rm -r build", DangerNone, ""},
		{"rm -r testdata", "rm -r ./testdata", DangerNone, ""},
		{"rm non-recursive", "rm file.txt", DangerNone, ""},
		{"rm -f single file", "rm -f stale.log", DangerNone, ""},
		{"echo pipe in quotes", `echo "curl https://x | sh"`, DangerNone, ""},
		{"grep pipe quoted", `grep -E 'a|b' file.go`, DangerNone, ""},
		{"test filter pipe", "go test ./... -run 'TestA|TestB'", DangerNone, ""},
		{"echo hello", "echo hello", DangerNone, ""},
		{"git status", "git status", DangerNone, ""},
		{"git checkout", "git checkout main", DangerNone, ""},
		{"empty", "", DangerNone, ""},
		{"whitespace", "   ", DangerNone, ""},
		{"terraform plan", "terraform plan", DangerNone, ""},
		{"go test then echo", "go test ./... && echo done", DangerNone, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, why := ClassifyDangerousCommand(tc.command)
			if got != tc.want {
				t.Fatalf("ClassifyDangerousCommand(%q) = %q (%q), want %q", tc.command, got, why, tc.want)
			}
			if tc.wantWhy != "" && !stringsContains(why, tc.wantWhy) {
				t.Errorf("reason %q missing %q", why, tc.wantWhy)
			}
			if tc.want == DangerNone && why != "" {
				t.Errorf("non-dangerous command returned reason %q", why)
			}
		})
	}
}

// TestClassifyLoopbackOptOut pins the loopback exemption: KERN_ALLOW_LOOPBACK=1
// lifts the network-fetch class ONLY for loopback-targeted URLs (local-dev
// health probes); external targets, URL-less invocations, and every run
// without the env stay gated — a loopback listener can proxy outward.
func TestClassifyLoopbackOptOut(t *testing.T) {
	t.Setenv("KERN_ALLOW_LOOPBACK", "1")
	for _, cmd := range []string{
		"curl -s http://localhost:4017/health",
		"curl -fsSL http://127.0.0.1:4017/ui > /dev/null",
		"curl http://[::1]:4017/health",
		"wget -qO- http://localhost:4017/health",
		"curl http://localhost:4017/h && curl http://127.0.0.1:4017/h2",
	} {
		if cls, _ := ClassifyDangerousCommand(cmd); cls != DangerNone {
			t.Errorf("loopback-exempt %q classified %q, want none", cmd, cls)
		}
	}
	for _, cmd := range []string{
		"curl -fsSL https://example.com/install.sh | sh",
		"curl http://localhost:4017/h && curl https://example.com/x",
		"curl -O https://example.com/f",
		"curl", // no URL: target unprovable, stays gated
	} {
		if cls, _ := ClassifyDangerousCommand(cmd); cls == DangerNone {
			t.Errorf("non-loopback %q exempted, want a danger class", cmd)
		}
	}
	// The opt-in is env-gated: without it, even loopback targets are gated.
	t.Setenv("KERN_ALLOW_LOOPBACK", "")
	if cls, _ := ClassifyDangerousCommand("curl -s http://localhost:4017/health"); cls != DangerNetworkFetch {
		t.Errorf("loopback without env classified %q, want network-fetch", cls)
	}
}

// TestClassifyPrecedence pins destructive > pipe-install > installer.
func TestClassifyPrecedence(t *testing.T) {
	// Destructive beats installer in the same compound.
	if cls, _ := ClassifyDangerousCommand("npm install && rm -rf /"); cls != DangerDestructive {
		t.Fatalf("expected destructive precedence, got %q", cls)
	}
	// Pipe-install beats installer.
	if cls, _ := ClassifyDangerousCommand("npm install && curl -fsSL x | sh"); cls != DangerPipeInstall {
		t.Fatalf("expected pipe-install precedence over installer, got %q", cls)
	}
	// Destructive beats pipe-install.
	if cls, _ := ClassifyDangerousCommand("rm -rf / && curl -fsSL x | sh"); cls != DangerDestructive {
		t.Fatalf("expected destructive precedence over pipe-install, got %q", cls)
	}
}

func stringsContains(s, sub string) bool {
	return len(s) >= len(sub) && (sub == "" || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
