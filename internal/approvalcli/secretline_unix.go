//go:build linux || darwin

package approvalcli

import (
	"io"
	"os"

	"golang.org/x/sys/unix"
)

// readSecretLine disables terminal echo while the operator types a passphrase.
// A non-file reader (test buffer) falls back to a plain line read.
func readSecretLine(rw io.ReadWriter) ([]byte, error) {
	f, ok := rw.(*os.File)
	if !ok {
		return readLine(rw)
	}
	fd := int(f.Fd())
	old, err := unix.IoctlGetTermios(fd, ioctlReadTermios)
	if err != nil {
		return readLine(rw)
	}
	noEcho := *old
	noEcho.Lflag &^= unix.ECHO
	if err := unix.IoctlSetTermios(fd, ioctlWriteTermios, &noEcho); err != nil {
		return readLine(rw)
	}
	defer func() { _ = unix.IoctlSetTermios(fd, ioctlWriteTermios, old) }()
	return readLine(rw)
}
