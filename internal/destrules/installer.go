package destrules

import "strings"

// ClassifyInstaller reports whether a command installs/upgrades packages or
// dependencies (npm/pnpm/yarn, pip, go, make, apt, dnf, yum, apk, brew,
// pacman). It lives here so execclassify stays under its ARCHITECTURE.md LOC
// cap; the classifier maps a hit to DangerInstaller.
func ClassifyInstaller(name string, tokens []string) (bool, string) {
	rest := tokens[1:]
	switch name {
	case "npm", "pnpm", "yarn":
		if len(rest) > 0 && (rest[0] == "install" || rest[0] == "add" || rest[0] == "ci") {
			return true, name + " " + rest[0]
		}
	case "pip", "pip3":
		if len(rest) > 0 && rest[0] == "install" {
			return true, name + " install"
		}
	case "go":
		if len(rest) > 0 && rest[0] == "install" {
			return true, "go install"
		}
	case "make":
		if len(rest) > 0 && rest[0] == "install" {
			return true, "make install"
		}
	case "apt", "apt-get", "dnf", "yum", "apk", "brew":
		if len(rest) > 0 && (rest[0] == "install" || rest[0] == "upgrade") {
			return true, name + " " + rest[0]
		}
	case "pacman":
		// pacman installs/upgrades with -S / -Syu / --sync / --upgrade flags.
		for _, t := range rest {
			if strings.HasPrefix(t, "--") && (t == "--sync" || t == "--upgrade") {
				return true, "pacman " + t
			}
			if strings.HasPrefix(t, "-S") && len(t) >= 2 {
				return true, "pacman " + t
			}
		}
	}
	return false, ""
}
