//go:build !darwin

package config

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
)

func defaultDataDir() string {
	dir := os.Getenv("XDG_DATA_HOME")
	if dir == "" {
		if home, err := os.UserHomeDir(); err == nil {
			dir = filepath.Join(home, ".local", "share")
		} else {
			return "tbyd-data"
		}
	}
	return filepath.Join(dir, "tbyd")
}

func apiKeyHint() string {
	return ""
}

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
		if !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "[WARN] could not read config file %s: %v. Using default values.\n", b.path, err)
		}
		return
	}
	if err := json.Unmarshal(data, &b.data); err != nil {
		fmt.Fprintf(os.Stderr, "[WARN] could not parse config file %s: %v. Using default values.\n", b.path, err)
	}
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
		if val < math.MinInt || val > math.MaxInt || val != math.Trunc(val) {
			return 0, true, fmt.Errorf("value %v for %s is not a valid integer or is out of range", val, key)
		}
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
