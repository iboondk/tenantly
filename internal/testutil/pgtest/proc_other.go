//go:build !unix

package pgtest

// processAlive is conservative off unix: it reports true so nothing is removed.
func processAlive(int) bool { return true }
