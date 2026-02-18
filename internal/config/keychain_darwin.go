//go:build darwin

package config

import "os/exec"

func keychainExec(service, account string) ([]byte, error) {
	return exec.Command(
		"security", "find-generic-password",
		"-s", service,
		"-a", account,
		"-w",
	).Output()
}
