//go:build !darwin && !linux

package project

// No native stdlib watcher on this platform: newNativeFileWatcher falls back
// to the external inotifywait/fswatch tool or the polling fallback.

func newNativeSource(root string) nativeSource { return nil }
