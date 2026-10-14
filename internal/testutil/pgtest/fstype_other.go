//go:build !darwin && !linux

package pgtest

// filesystemType is unknown on this platform, so no volume is rejected up front.
func filesystemType(string) string { return "" }
