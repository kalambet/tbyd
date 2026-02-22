//go:build darwin

package config

import (
	"errors"
	"os/exec"
)

func keychainGet(service, account string) ([]byte, error) {
	out, err := exec.Command(
		"security", "find-generic-password",
		"-s", service,
		"-a", account,
		"-w",
	).Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return out, nil
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
