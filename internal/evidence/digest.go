package evidence

import (
	"crypto/sha256"
	"encoding/hex"
)

// Digest returns a stable SHA-256 hex digest of content for evidence
// integrity checks.
func Digest(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}
