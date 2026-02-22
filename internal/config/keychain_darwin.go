//go:build darwin

package config

import "os/exec"

func keychainGet(service, account string) ([]byte, error) {
	return exec.Command(
		"security", "find-generic-password",
		"-s", service,
		"-a", account,
		"-w",
	).Output()
}

func keychainSet(service, account, value string) error {
	// Delete any existing item first (add-generic-password fails on duplicates).
	_ = exec.Command(
		"security", "delete-generic-password",
		"-s", service,
		"-a", account,
	).Run()

	return exec.Command(
		"security", "add-generic-password",
		"-s", service,
		"-a", account,
		"-w", value,
	).Run()
}
