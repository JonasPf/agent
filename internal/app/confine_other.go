//go:build !linux

package app

import "errors"

// The agent is a Linux program: Landlock is the only confinement it has, and
// there is no second mechanism for a second platform. These exist so the package
// still builds elsewhere — a laptop runs the tests and the interface — and so
// that a wrapper reached here fails rather than running a tool unconfined.

func landlockAvailable() (bool, string) {
	return false, "Landlock is a Linux facility and this is not Linux; " +
		"run the agent in its container (task dev) for a confined tool"
}

func applyPolicy(policy) error {
	return errors.New("no confinement is implemented for this platform")
}
