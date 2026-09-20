// Package fsutil provides small shared filesystem helpers.
package fsutil

import (
	"os"
	"path"
	"path/filepath"
	"strings"
)

// WriteFileAtomic writes data to path atomically: write to a temp file in the
// same directory, set permissions, then rename over the target.
func WriteFileAtomic(targetPath string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(targetPath), filepath.Base(targetPath)+".tmp*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(name)
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		_ = os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Rename(name, targetPath); err != nil {
		_ = os.Remove(name)
		return err
	}
	return nil
}

// NormalizePath converts backslashes to forward slashes and cleans redundant
// path elements, ensuring uniform POSIX representation across all OS platforms.
func NormalizePath(p string) string {
	if p == "" {
		return ""
	}
	p = strings.ReplaceAll(p, "\\", "/")
	return path.Clean(p)
}

// NormalizeRelPath converts backslashes to forward slashes, cleans redundant
// path elements, and strips any leading "./" prefix.
func NormalizeRelPath(p string) string {
	if p == "" {
		return ""
	}
	p = strings.ReplaceAll(p, "\\", "/")
	n := path.Clean(p)
	if n == "." {
		return "."
	}
	return strings.TrimPrefix(n, "./")
}
