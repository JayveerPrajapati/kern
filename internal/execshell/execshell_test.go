package execshell

import (
	"reflect"
	"testing"
)

func TestShellArgs(t *testing.T) {
	cases := []struct {
		goos string
		want string
		args []string
	}{
		{"windows", "cmd", []string{"/c", "npm test"}},
		{"linux", "sh", []string{"-c", "npm test"}},
		{"darwin", "sh", []string{"-c", "npm test"}},
	}
	for _, c := range cases {
		name, args := ShellArgs(c.goos, "npm test")
		if name != c.want || !reflect.DeepEqual(args, c.args) {
			t.Errorf("ShellArgs(%q) = %q %v, want %q %v", c.goos, name, args, c.want, c.args)
		}
	}
}
