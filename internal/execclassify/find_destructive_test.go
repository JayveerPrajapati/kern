package execclassify

import "testing"

// TestClassifyFindXargsTeeDestructive pins the 2026-10-26 audit bypasses:
// `find . -delete` ran and deleted files while the guard classified the
// command "serve" because find is a READ command by default. Destructive
// predicates (-delete, -exec rm, -fprint) and destructive forwarding through
// xargs/tee/shred must classify destructive so enforcement gates them.
func TestClassifyFindXargsTeeDestructive(t *testing.T) {
	cases := []struct {
		name    string
		command string
		want    DangerClass
	}{
		{"find -delete", "find . -delete", DangerDestructive},
		{"find name -delete", "find . -name '*.go' -delete", DangerDestructive},
		{"find exec rm", "find . -exec rm {} ;", DangerDestructive},
		{"find execdir rm absolute", "find . -execdir /bin/rm {} ;", DangerDestructive},
		{"find ok rm", "find . -ok rm {} ;", DangerDestructive},
		{"find fprint", "find . -fprint out.txt", DangerDestructive},
		{"xargs rm", "ls | xargs rm", DangerDestructive},
		{"xargs -n1 rm", "ls | xargs -n1 rm", DangerDestructive},
		{"tee file", "echo x | tee out.txt", DangerDestructive},
		{"shred", "shred -u secret.txt", DangerDestructive},
		// Non-destructive controls: read-only predicates and forwarding to a
		// read tool stay DangerNone.
		{"find type", "find . -type f", DangerNone},
		{"find exec grep", "find . -exec grep -n x {} ;", DangerNone},
		{"find print", "find . -print", DangerNone},
		{"xargs grep", "ls | xargs -n1 grep x", DangerNone},
		{"tee devnull", "echo x | tee /dev/null", DangerNone},
	}
	for _, c := range cases {
		got, why := ClassifyDangerousCommand(c.command)
		if got != c.want {
			t.Errorf("%s: ClassifyDangerousCommand(%q) = %q (%s), want %q", c.name, c.command, got, why, c.want)
		}
	}
}
