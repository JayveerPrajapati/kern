package execinterp

import "testing"

func TestClassifyInterpreter(t *testing.T) {
	cases := []struct {
		name   string
		tokens []string
		want   bool
	}{
		{"python3", []string{"python3", "-c", "import subprocess; subprocess.run(['rm','-rf','d'])"}, true},
		{"python3", []string{"python3", "-c", "open('/tmp/x','w').write('x')"}, true},
		{"python3", []string{"python3", "-c", "print('hi')"}, false},
		{"perl", []string{"perl", "-e", `unlink "x"`}, true},
		{"ruby", []string{"ruby", "-e", `File.delete("x")`}, true},
		{"node", []string{"node", "-e", "require('fs').rmSync('x')"}, true},
		{"node", []string{"node", "-e", "console.log(1)"}, false},
	}
	for _, c := range cases {
		hit, why := ClassifyInterpreter(c.name, "", c.tokens)
		if hit != c.want {
			t.Errorf("ClassifyInterpreter(%s %v) = %v (%s), want %v", c.name, c.tokens, hit, why, c.want)
		}
	}
}

func TestClassifyEncodedPipe(t *testing.T) {
	cases := []struct {
		stages []string
		want   bool
	}{
		{[]string{"printf ZWNobyBwd25lZAo=", "base64 -d", "sh"}, true},
		{[]string{"echo x", "base64 -d"}, false},
		{[]string{"base64 -d file.txt"}, false},
	}
	for _, c := range cases {
		hit, why := ClassifyEncodedPipe(c.stages)
		if hit != c.want {
			t.Errorf("ClassifyEncodedPipe(%v) = %v (%s), want %v", c.stages, hit, why, c.want)
		}
	}
}
