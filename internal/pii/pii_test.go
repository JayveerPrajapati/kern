package pii

import (
	"strings"
	"testing"
)

func TestMaskIPAndEmail(t *testing.T) {
	in := "server at 192.168.1.100 failed; contact ops@corp.example.com"
	res := Mask(in)
	if !strings.Contains(res.Text, "[MASKED_IP_1]") {
		t.Errorf("IP not masked: %q", res.Text)
	}
	if !strings.Contains(res.Text, "[MASKED_EMAIL_1]") {
		t.Errorf("email not masked: %q", res.Text)
	}
	if res.ByLabel["IP"] != 1 || res.ByLabel["EMAIL"] != 1 {
		t.Errorf("expected 1 IP + 1 email, got %+v", res.ByLabel)
	}
}

func TestMaskSequentialPlaceholders(t *testing.T) {
	in := "a: 10.0.0.1\nb: 10.0.0.2\nc: 10.0.0.3"
	res := Mask(in)
	for _, ph := range []string{"[MASKED_IP_1]", "[MASKED_IP_2]", "[MASKED_IP_3]"} {
		if !strings.Contains(res.Text, ph) {
			t.Errorf("missing %s in %q", ph, res.Text)
		}
	}
	if res.Mapping["[MASKED_IP_1]"] != "10.0.0.1" {
		t.Errorf("bad mapping: %+v", res.Mapping)
	}
}

func TestMaskKeys(t *testing.T) {
	cases := []struct{ in, label string }{
		{`aws_secret_access_key = "c9as98c27as6c987scx87as69c0as97c6x9as123"`, "AWS_SECRET"},
		{"AKIAIOSFODNN7EXAMPLE", "AWS"},
		{"ghp_abcdefghijklmnopqrstuvwxyz1234567890", "GITHUB"},
		{"xoxb-1234567890123-1234567890123-abc", "SLACK"},
		{"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxIn0.abc1234567", "JWT"},
		{`api_key = "abcd1234efgh5678ijkl9012"`, "KEY"},
		{`"password": "hunter2secret123"`, "PASSWORD"},
		{`token = "0123456789abcdef0123456789abcdef"`, "TOKEN"},
		{"Authorization: Bearer sk-live-abcdefghijklmnopqrstuvwxyz123456", "BEARER"},
		{"-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEAx\n-----END RSA PRIVATE KEY-----", "PRIVATE_KEY"},
	}
	for _, c := range cases {
		res := Mask(c.in)
		if res.ByLabel[c.label] == 0 {
			t.Errorf("input %q: label %s not detected (got %+v)", c.in, c.label, res.ByLabel)
		}
		if strings.Contains(res.Text, c.in) && c.in != res.Text {
			t.Errorf("input %q not fully replaced: %q", c.in, res.Text)
		}
	}
}

func TestMaskURLCreds(t *testing.T) {
	in := "mysql://root:supersecretpw@db.internal:3306/app"
	res := Mask(in)
	if strings.Contains(res.Text, "supersecretpw") {
		t.Errorf("url password leaked: %q", res.Text)
	}
	if !strings.Contains(res.Text, "[MASKED_URL_CRED_1]") {
		t.Errorf("url creds not masked: %q", res.Text)
	}
}

func TestMaskNames(t *testing.T) {
	res := MaskNames("deploy for Acme Corp at 10.1.1.1", []string{"Acme Corp"})
	if !strings.Contains(res.Text, "[MASKED_NAME_1]") {
		t.Errorf("client name not masked: %q", res.Text)
	}
	if strings.Contains(res.Text, "Acme") {
		t.Errorf("client name leaked: %q", res.Text)
	}
}

func TestOverlapLongestWins(t *testing.T) {
	in := "mysql://user:hunter2secret123@host"
	res := Mask(in)
	// URL_CRED covers the whole userinfo; the PASSWORD inside must not also be
	// replaced (no nested placeholders, no double masking).
	if strings.Contains(res.Text, "[MASKED_PASSWORD_1]") {
		t.Errorf("nested password masked inside URL creds: %q", res.Text)
	}
	if !strings.Contains(res.Text, "[MASKED_URL_CRED_1]") {
		t.Errorf("URL creds not masked: %q", res.Text)
	}
}

func TestUnmaskRoundTrip(t *testing.T) {
	in := "ip 10.0.0.9 email x@y.example"
	res := Mask(in)
	back := res.Unmask(res.Text)
	if back != in {
		t.Errorf("unmask round-trip failed: got %q want %q", back, in)
	}
}

func TestMaskNoSecretsUnchanged(t *testing.T) {
	in := "hello world, nothing to see here"
	res := Mask(in)
	if res.Text != in || res.Replaced != 0 {
		t.Errorf("clean text was modified: %q", res.Text)
	}
}

func TestMaskBareIdentifiersUntouched(t *testing.T) {
	// Quoted-only generic rules must NOT mask code identifiers.
	in := "password = hashed; token = cached"
	res := Mask(in)
	if strings.Contains(res.Text, "[MASKED_PASSWORD_1]") || strings.Contains(res.Text, "[MASKED_TOKEN_1]") {
		t.Errorf("bare identifiers masked: %q", res.Text)
	}
}

func TestMaskUnquotedSecretsWithDigits(t *testing.T) {
	in := "PASSWORD=hunter2 token=abc12345secretkeylong api_key =  myapi123456789key"
	res := Mask(in)
	for _, want := range []string{"[MASKED_PASSWORD_", "[MASKED_TOKEN_", "[MASKED_KEY_"} {
		if !strings.Contains(res.Text, want) {
			t.Errorf("expected %s in %q", want, res.Text)
		}
	}
}

func TestMaskGithubPATFormat(t *testing.T) {
	in := "token github_pat_ABCDEFGHIJKLMNOPQRSTUVWXYZ1234567890_abcdefghijklmnopqrstuvwxyz1234567890"
	res := Mask(in)
	if !strings.Contains(res.Text, "[MASKED_GITHUB_PAT_1]") {
		t.Errorf("github_pat token not masked: %q", res.Text)
	}
}
