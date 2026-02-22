//go:build !darwin

package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

func secretsFilePath() string {
	dir := os.Getenv("XDG_DATA_HOME")
	if dir == "" {
		if home, err := os.UserHomeDir(); err == nil {
			dir = filepath.Join(home, ".local", "share")
		} else {
			dir = "."
		}
	}
	return filepath.Join(dir, "tbyd", "secrets.json")
}

func keychainGet(service, account string) ([]byte, error) {
	data, err := os.ReadFile(secretsFilePath())
	if err != nil {
		return nil, fmt.Errorf("keychain not available: %w", err)
	}
	var secrets map[string]map[string]string
	if err := json.Unmarshal(data, &secrets); err != nil {
		return nil, fmt.Errorf("parsing secrets file: %w", err)
	}
	svc, ok := secrets[service]
	if !ok {
		return nil, fmt.Errorf("service %q not found", service)
	}
	val, ok := svc[account]
	if !ok {
		return nil, fmt.Errorf("account %q not found in service %q", account, service)
	}
	return []byte(val), nil
}

func keychainSet(service, account, value string) error {
	p := secretsFilePath()

	var secrets map[string]map[string]string

	data, err := os.ReadFile(p)
	if err == nil {
		_ = json.Unmarshal(data, &secrets)
	}
	if secrets == nil {
		secrets = make(map[string]map[string]string)
	}
	if secrets[service] == nil {
		secrets[service] = make(map[string]string)
	}
	secrets[service][account] = value

	dir := filepath.Dir(p)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating secrets dir: %w", err)
	}
	out, err := json.MarshalIndent(secrets, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, out, 0o600)
}
