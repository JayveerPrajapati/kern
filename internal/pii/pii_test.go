package pii

import (
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"
)

func TestMaskIPAndEmail(t *testing.T) {
	in := "server at 8.8.8.8 failed; contact ops@corp.example.com"
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

func TestMaskCDNVersionURLNotEmail(t *testing.T) {
	in := "load https://unpkg.com/boxicons@2.1.4/css/boxicons.min.css and 1.0.0"
	res := Mask(in)
	if strings.Contains(res.Text, "[MASKED_EMAIL_") {
		t.Errorf("pkg@version URL must not be masked as email: %q", res.Text)
	}
	if res.ByLabel["EMAIL"] != 0 {
		t.Errorf("expected no EMAIL findings, got %+v", res.ByLabel)
	}
	// A real email must still be masked.
	res2 := Mask("contact ops@corp.example.com")
	if res2.ByLabel["EMAIL"] != 1 {
		t.Errorf("real email must still be masked, got %+v", res2.ByLabel)
	}
}

func TestMaskSequentialPlaceholders(t *testing.T) {
	in := "a: 8.8.8.8\nb: 1.1.1.1\nc: 9.9.9.9"
	res := Mask(in)
	for _, ph := range []string{"[MASKED_IP_1]", "[MASKED_IP_2]", "[MASKED_IP_3]"} {
		if !strings.Contains(res.Text, ph) {
			t.Errorf("missing %s in %q", ph, res.Text)
		}
	}
	if res.Mapping["[MASKED_IP_1]"] != "8.8.8.8" {
		t.Errorf("bad mapping: %+v", res.Mapping)
	}
}

func TestMaskSkipsNonSecretIPs(t *testing.T) {
	in := "loopback 127.0.0.1, private 10.0.0.5 and 192.168.1.10, link-local 169.254.1.1, ipv6 ::1 and fe80::1"
	res := Mask(in)
	if strings.Contains(res.Text, "[MASKED_IP_") {
		t.Errorf("non-secret IPs must not be masked: %q", res.Text)
	}
	if res.Text != in {
		t.Errorf("expected input unchanged, got %q", res.Text)
	}
	if res.Replaced != 0 {
		t.Errorf("expected zero replacements, got %d", res.Replaced)
	}
}

func TestMaskPublicIPv6(t *testing.T) {
	res := Mask("router at 2001:db8:85a3::8a2e:370:7334")
	if !strings.Contains(res.Text, "[MASKED_IPV6_1]") {
		t.Errorf("public IPv6 not masked: %q", res.Text)
	}
	if res.Mapping["[MASKED_IPV6_1]"] != "2001:db8:85a3::8a2e:370:7334" {
		t.Errorf("bad ipv6 mapping: %+v", res.Mapping)
	}
}

func TestMaskKeys(t *testing.T) {
	cases := []struct{ in, label string }{
		{`aws_secret_access_key = "c9as98c27as6c987scx87as69c0as97c6x9as123"`, "AWS_SECRET"},
		{"AKIAIOSFODNN7EXAMPLE", "AWS"},
		{"ghp_abcdefghijklmnopqrstuvwxyz1234567890", "GITHUB"},
		{"sk-proj-4f8a2b9c1d0e3f5a7b8c9d0e1f2a3b4c5d6e7f8a", "OPENAI"},
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

func TestMaskPhoneFormats(t *testing.T) {
	in := "us: (555) 123-4567, dash: 555-123-4567, dot: 555.123.4567, e164: +1 202-555-0134, in: +91 98765 43210, uk: 020 7946 0958, bare: 9876543210"
	res := Mask(in)
	for _, want := range []string{"[MASKED_PHONE_1]", "[MASKED_PHONE_2]", "[MASKED_PHONE_3]", "[MASKED_PHONE_4]", "[MASKED_PHONE_5]", "[MASKED_PHONE_6]", "[MASKED_PHONE_7]"} {
		if !strings.Contains(res.Text, want) {
			t.Errorf("missing %s in %q", want, res.Text)
		}
	}
	if res.ByLabel["PHONE"] != 7 {
		t.Errorf("expected 7 phones, got %+v", res.ByLabel)
	}
}

func TestMaskPhoneDoesNotEatDatesIds(t *testing.T) {
	in := "date 12.03.2026, ts 20250813151234, ord ORD-12345, ver 1.2.3456"
	res := Mask(in)
	if strings.Contains(res.Text, "[MASKED_PHONE_") {
		t.Errorf("dates/ids must not be masked as phones: %q", res.Text)
	}
}

func TestMaskPhoneIPAndSSNNotConfused(t *testing.T) {
	in := "ip 192.168.1.1, ssn 123-45-6789, phone 07911 123456, uk +44 20 7946 0958"
	res := Mask(in)
	if !strings.Contains(res.Text, "[MASKED_PHONE_1]") {
		t.Errorf("phone not masked: %q", res.Text)
	}
	if !strings.Contains(res.Text, "[MASKED_PHONE_2]") {
		t.Errorf("uk e164 not masked: %q", res.Text)
	}
	if strings.Contains(res.Text, "[MASKED_IP_") {
		t.Errorf("private IP must stay unmasked: %q", res.Text)
	}
	if !strings.Contains(res.Text, "[MASKED_SSN_1]") {
		t.Errorf("ssn not masked: %q", res.Text)
	}
}

func TestMaskPhoneDigitRunFilter(t *testing.T) {
	in := "ts 20250813151234, zip 12345-6789, ord 1234567890123"
	res := Mask(in)
	if strings.Contains(res.Text, "[MASKED_PHONE_") {
		t.Errorf("digit runs must not be masked as phones: %q", res.Text)
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

func TestMaskAPIKeySpaceSeparator(t *testing.T) {
	// "api key" (space-separated) must be detected like api_key / api-key.
	in := "API key: sk-1234567890abcdef"
	res := Mask(in)
	if strings.Contains(res.Text, "sk-1234567890abcdef") {
		t.Errorf("API key with space separator not masked: %q", res.Text)
	}
	if res.ByLabel["KEY"] != 1 {
		t.Errorf("expected KEY finding, got %+v", res.ByLabel)
	}
}

func TestMaskShortOpenAIKey(t *testing.T) {
	// Short sk- keys (10-19 chars) fall outside the OPENAI 20+ pattern.
	in := "use sk-1234567890abcdef to call the API"
	res := Mask(in)
	if strings.Contains(res.Text, "sk-1234567890abcdef") {
		t.Errorf("short sk- key not masked: %q", res.Text)
	}
	if res.ByLabel["OPENAI_SHORT"] != 1 {
		t.Errorf("expected OPENAI_SHORT finding, got %+v", res.ByLabel)
	}
}

func TestMaskAllMasksPrivateIPs(t *testing.T) {
	// MaskAll is for PII masking before sending text to a remote LLM, where
	// even private/loopback IPs are PII (unlike Mask / MaskCustom, which
	// suppress them as non-secrets).
	in := "Contact me at john.doe@example.com or call 555-123-4567. API key: sk-1234567890abcdef. IP: 192.168.1.1"
	res := MaskAll(in)
	for _, leak := range []string{"john.doe@example.com", "555-123-4567", "sk-1234567890abcdef", "192.168.1.1"} {
		if strings.Contains(res.Text, leak) {
			t.Errorf("secrets leaked: %q", res.Text)
		}
	}
	for _, ph := range []string{"[MASKED_EMAIL_1]", "[MASKED_PHONE_1]", "[MASKED_IP_1]"} {
		if !strings.Contains(res.Text, ph) {
			t.Errorf("missing %s in %q", ph, res.Text)
		}
	}
	if res.ByLabel["IP"] != 1 {
		t.Errorf("expected private IP masked by MaskAll, got %+v", res.ByLabel)
	}
}

func TestMaskAllSuppressVariantStillHidesPrivateIPs(t *testing.T) {
	// Mask (and MaskCustom) keep suppressing private IPs: only MaskAll flips.
	in := "db at 10.0.0.5"
	if res := Mask(in); strings.Contains(res.Text, "[MASKED_IP_") {
		t.Errorf("Mask must suppress private IPs: %q", res.Text)
	}
	if res := MaskAll(in); !strings.Contains(res.Text, "[MASKED_IP_1]") {
		t.Errorf("MaskAll must mask private IPs: %q", res.Text)
	}
}

func TestMaskGithubPATFormat(t *testing.T) {
	in := "token github_pat_ABCDEFGHIJKLMNOPQRSTUVWXYZ1234567890_abcdefghijklmnopqrstuvwxyz1234567890"
	res := Mask(in)
	if !strings.Contains(res.Text, "[MASKED_GITHUB_PAT_1]") {
		t.Errorf("github_pat token not masked: %q", res.Text)
	}
}

func TestMaskBase64APIKey(t *testing.T) {
	key := "AKIAIOSFODNN7EXAMPLE" // matches the AWS pattern once decoded
	enc := base64.StdEncoding.EncodeToString([]byte(key))
	in := "creds: " + enc
	res := Mask(in)
	if !strings.Contains(res.Text, "[MASKED_B64_1]") {
		t.Fatalf("base64 API key not masked: %q", res.Text)
	}
	if strings.Contains(res.Text, enc) {
		t.Fatalf("base64 API key leaked: %q", res.Text)
	}
	if res.ByLabel["B64"] != 1 {
		t.Errorf("expected 1 B64 finding, got %+v", res.ByLabel)
	}
	if back := res.Unmask(res.Text); back != in {
		t.Errorf("round-trip failed: got %q want %q", back, in)
	}
}

func TestMaskHexPassword(t *testing.T) {
	secret := `password="hunter2secret123"` // decoded content matches PASSWORD
	enc := hex.EncodeToString([]byte(secret))
	in := "config: " + enc
	res := Mask(in)
	if !strings.Contains(res.Text, "[MASKED_HEX_1]") {
		t.Fatalf("hex password not masked: %q", res.Text)
	}
	if strings.Contains(res.Text, enc) {
		t.Fatalf("hex password leaked: %q", res.Text)
	}
	if res.ByLabel["HEX"] != 1 {
		t.Errorf("expected 1 HEX finding, got %+v", res.ByLabel)
	}
	if back := res.Unmask(res.Text); back != in {
		t.Errorf("round-trip failed: got %q want %q", back, in)
	}
}

func TestMaskNormalBase64NotMasked(t *testing.T) {
	in := "data:image/png;base64," + base64.StdEncoding.EncodeToString(
		[]byte("a completely ordinary sentence that is absolutely not a secret"))
	res := Mask(in)
	if res.Text != in {
		t.Errorf("normal base64 data was masked: %q", res.Text)
	}
	if res.Replaced != 0 {
		t.Errorf("expected zero replacements for normal base64, got %d", res.Replaced)
	}
}
