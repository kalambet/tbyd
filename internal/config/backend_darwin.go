//go:build darwin

package config

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

const defaultsDomain = "com.tbyd.app"

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
		if strings.Contains(s, "does not exist") {
			return "", false, nil
		}
		return "", false, err
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
