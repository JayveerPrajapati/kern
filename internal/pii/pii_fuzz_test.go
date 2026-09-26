package pii

import (
	"encoding/base64"
	"encoding/hex"
	"testing"
)

// FuzzMask asserts Mask's documented contracts over arbitrary text (including
// invalid UTF-8 and NUL bytes):
//
//   - it must never panic (a panic fails the fuzz target);
//   - the Unmask(Mask(text)) round-trip must restore the original text —
//     placeholders are documented as unique per label (e.g. [MASKED_IP_1],
//     [MASKED_IP_2]) so unmasking is lossless. A collision between two
//     generated placeholders (or with a literal placeholder in the input)
//     breaks the round-trip and is a masking bug.
func FuzzMask(f *testing.F) {
	seeds := []string{
		"",
		"hello world",
		"server at 8.8.8.8 failed; contact ops@corp.example.com",
		`aws_secret_access_key = "c9as98c27as6c987scx87as69c0as97c6x9as123"`,
		"AKIAIOSFODNN7EXAMPLE",
		"ghp_abcdefghijklmnopqrstuvwxyz1234567890",
		"sk-proj-4f8a2b9c1d0e3f5a7b8c9d0e1f2a3b4c5d6e7f8a",
		"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxIn0.abc1234567",
		"mysql://root:supersecretpw@db.internal:3306/app",
		"password = hunter2secret123 token = abc123",
		"-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEAx\n-----END RSA PRIVATE KEY-----",
		"Authorization: Bearer sk-live-abcdefghijklmnopqrstuvwxyz123456",
		// Hex-encoded secret (decodes to a PASSWORD) — triggers the encoding
		// pre-pass, which emits [MASKED_HEX_1] placeholders.
		"config: " + hex.EncodeToString([]byte(`password="hunter2secret123"`)),
		// A plain long hex run — triggers the HEX pattern label, which emits
		// [MASKED_HEX_N] placeholders from the same namespace as the
		// encoding pre-pass.
		"token 9f8a7b6c5d4e3f2a1b0c9d8e7f6a5b4c3d2e1f0a9b8c7d6e5f4a3b2c1d0e9f8a seen",
		"creds: " + base64.StdEncoding.EncodeToString([]byte("AKIAIOSFODNN7EXAMPLE")),
		"[MASKED_IP_1] 8.8.8.8", // literal placeholder text in the input
		"\x00\x01\x02",
		"\xff\xfe\xfd",
		`token = "0123456789abcdef0123456789abcdef"`,
		"date 12.03.2026 ts 20250813151234",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, text string) {
		res := Mask(text)
		if back := res.Unmask(res.Text); back != text {
			t.Fatalf("round-trip failed:\n in: %q\nmasked: %q\n out: %q\nmapping: %v", text, res.Text, back, res.Mapping)
		}
	})
}
