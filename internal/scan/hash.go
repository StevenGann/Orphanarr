package scan

import (
	"crypto/sha256"
	"encoding/hex"
)

// hashString is used only for content identity, never for verification.
// A fingerprint answers "is this the same payload"; it does not answer "did
// these bytes arrive intact", which is what the post-copy source re-check
// (I13) is for.
func hashString(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:16])
}
