//go:build !darwin

package config

import "fmt"

func keychainExec(service, account string) ([]byte, error) {
	return nil, fmt.Errorf("keychain not supported on this platform")
}
