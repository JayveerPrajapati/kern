//go:build !linux

package landlock

// LandlockAvailable is a non-Linux stub: Landlock is a Linux kernel feature.
// The sandbox calls it only on the Linux branch, but the symbol must exist on
// every platform for the package to compile.
func LandlockAvailable(prefix []string) bool { return false }
