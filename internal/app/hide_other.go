//go:build !linux

package app

// Off Linux there is no /proc to read an environment out of, and the confinement
// is a profile rather than a path grant.
func hideProcess() error { return nil }
