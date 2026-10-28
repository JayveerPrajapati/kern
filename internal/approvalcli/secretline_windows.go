//go:build windows

package approvalcli

import (
	"io"
	"os"

	"golang.org/x/sys/windows"
)

// readSecretLine disables console echo while the operator types a passphrase.
// On a non-console reader (test buffer) it falls back to a plain line read.
func readSecretLine(rw io.ReadWriter) ([]byte, error) {
	f, ok := rw.(*os.File)
	if !ok {
		return readLine(rw)
	}
	h := windows.Handle(f.Fd())
	var mode uint32
	if err := windows.GetConsoleMode(h, &mode); err != nil {
		return readLine(rw)
	}
	if err := windows.SetConsoleMode(h, mode&^windows.ENABLE_ECHO_INPUT); err != nil {
		return readLine(rw)
	}
	defer func() { _ = windows.SetConsoleMode(h, mode) }()
	return readLine(rw)
}
