//go:build !darwin

package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// fileBackend stores config as a flat JSON object in an XDG-compatible path.
// This is the default for Linux and other non-macOS platforms.
type fileBackend struct {
	path string
	data map[string]any
}

func newPlatformBackend() ConfigBackend {
	p := configFilePath()
	b := &fileBackend{path: p, data: make(map[string]any)}
	b.load()
	return b
}

func configFilePath() string {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		if home, err := os.UserHomeDir(); err == nil {
			dir = filepath.Join(home, ".config")
		} else {
			dir = "."
		}
	}
	return filepath.Join(dir, "tbyd", "config.json")
}

func (b *fileBackend) load() {
	data, err := os.ReadFile(b.path)
	if err != nil {
		return
	}
	_ = json.Unmarshal(data, &b.data)
}

func (b *fileBackend) save() error {
	dir := filepath.Dir(b.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating config dir: %w", err)
	}
	data, err := json.MarshalIndent(b.data, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(b.path, data, 0o600)
}

func (b *fileBackend) GetString(key string) (string, bool, error) {
	v, ok := b.data[key]
	if !ok {
		return "", false, nil
	}
	s, ok := v.(string)
	if !ok {
		return fmt.Sprintf("%v", v), true, nil
	}
	return s, true, nil
}

func (b *fileBackend) GetInt(key string) (int, bool, error) {
	v, ok := b.data[key]
	if !ok {
		return 0, false, nil
	}
	switch val := v.(type) {
	case float64:
		return int(val), true, nil
	case string:
		i, err := strconv.Atoi(val)
		if err != nil {
			return 0, true, fmt.Errorf("invalid integer for %s: %w", key, err)
		}
		return i, true, nil
	default:
		return 0, true, fmt.Errorf("invalid type for %s", key)
	}
}

func (b *fileBackend) SetString(key, val string) error {
	b.data[key] = val
	return b.save()
}

func (b *fileBackend) SetInt(key string, val int) error {
	b.data[key] = val
	return b.save()
}

func (b *fileBackend) Delete(key string) error {
	delete(b.data, key)
	return b.save()
}
