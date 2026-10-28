//go:build !linux && !darwin && !windows

package approvalcli

import "io"

// readSecretLine has no echo-off implementation on this platform; it still
// reads exactly one line so the approval flow remains functional.
func readSecretLine(rw io.ReadWriter) ([]byte, error) {
	return readLine(rw)
}
