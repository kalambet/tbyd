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
	// The -U flag updates the item if it exists, or creates it if it doesn't.
	return exec.Command(
		"security", "add-generic-password",
		"-U",
		"-s", service,
		"-a", account,
		"-w", value,
	).Run()
}
