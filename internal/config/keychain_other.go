//go:build !darwin

package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

func secretsFilePath() (string, error) {
	dir := os.Getenv("XDG_DATA_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("cannot determine secrets path: %w", err)
		}
		dir = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(dir, "tbyd", "secrets.json"), nil
}

func keychainGet(service, account string) ([]byte, error) {
	p, err := secretsFilePath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("reading secrets file: %w", err)
	}
	var secrets map[string]map[string]string
	if err := json.Unmarshal(data, &secrets); err != nil {
		return nil, fmt.Errorf("parsing secrets file: %w", err)
	}
	svc, ok := secrets[service]
	if !ok {
		return nil, ErrNotFound
	}
	val, ok := svc[account]
	if !ok {
		return nil, ErrNotFound
	}
	return []byte(val), nil
}

func keychainSet(service, account, value string) error {
	p, err := secretsFilePath()
	if err != nil {
		return err
	}

	var secrets map[string]map[string]string

	data, err := os.ReadFile(p)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading secrets file: %w", err)
	}
	if err == nil {
		if err := json.Unmarshal(data, &secrets); err != nil {
			return fmt.Errorf("parsing secrets file: %w", err)
		}
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

	// Atomic write: write to temp file then rename into place.
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, out, 0o600); err != nil {
		return fmt.Errorf("writing secrets temp file: %w", err)
	}
	return os.Rename(tmp, p)
}
