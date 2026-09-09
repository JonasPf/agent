//go:build !linux

package app

import "errors"

// Off Linux the wrapper is never used: macOS confines with Seatbelt, which puts
// a profile in front of the command instead of restricting the process that
// runs it. These exist so the package builds and so the failure, if the wrapper
// were ever reached here, is an error rather than a command running unconfined.

func landlockAvailable() (bool, string) {
	return false, "Landlock is a Linux facility"
}

func applyPolicy(policy) error {
	return errors.New("no confinement is implemented for this platform")
}
