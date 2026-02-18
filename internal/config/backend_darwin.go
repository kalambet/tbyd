//go:build darwin

package config

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

const defaultsDomain = "com.tbyd.app"

func defaultDataDir() string {
	if homeDir, err := os.UserHomeDir(); err == nil {
		return filepath.Join(homeDir, "Library", "Application Support", "tbyd")
	}
	return "tbyd-data"
}

func apiKeyHint() string {
	return " or macOS Keychain (service: tbyd, account: openrouter_api_key)"
}

type darwinBackend struct {
	domain string
}

func newPlatformBackend() ConfigBackend {
	return &darwinBackend{domain: defaultsDomain}
}

func (b *darwinBackend) read(key string) (string, bool, error) {
	cmd := exec.Command("defaults", "read", b.domain, key)
	out, err := cmd.CombinedOutput()
	s := strings.TrimSpace(string(out))
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return "", false, nil
		}
		return "", false, fmt.Errorf("reading default for key '%s': %w, output: %s", key, err, s)
	}
	return s, true, nil
}

func (b *darwinBackend) GetString(key string) (string, bool, error) {
	return b.read(key)
}

func (b *darwinBackend) GetInt(key string) (int, bool, error) {
	s, ok, err := b.read(key)
	if !ok || err != nil {
		return 0, ok, err
	}
	i, err := strconv.Atoi(s)
	if err != nil {
		return 0, true, fmt.Errorf("invalid integer for %s: %w", key, err)
	}
	return i, true, nil
}

func (b *darwinBackend) SetString(key, val string) error {
	return exec.Command("defaults", "write", b.domain, key, "-string", val).Run()
}

func (b *darwinBackend) SetInt(key string, val int) error {
	return exec.Command("defaults", "write", b.domain, key, "-int", strconv.Itoa(val)).Run()
}

func (b *darwinBackend) Delete(key string) error {
	return exec.Command("defaults", "delete", b.domain, key).Run()
}
