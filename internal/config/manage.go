package config

import (
	"fmt"
	"strconv"
)

// KeyInfo describes a config key for display purposes.
type KeyInfo struct {
	Key    string
	EnvVar string
	Value  string
}

// ShowAll returns all config key/value pairs from the current config.
func ShowAll(cfg Config) []KeyInfo {
	var result []KeyInfo
	for _, s := range specs {
		if s.secret {
			continue
		}
		result = append(result, KeyInfo{
			Key:    s.key,
			EnvVar: s.env,
			Value:  fmt.Sprintf("%v", s.extract(cfg)),
		})
	}
	return result
}

// SetKey writes a config key to the platform backend.
func SetKey(key, value string) error {
	b := newPlatformBackend()

	for _, s := range specs {
		if s.key != key {
			continue
		}
		if s.secret {
			return fmt.Errorf("cannot set secret %q via config; use environment variable %s", key, s.env)
		}
		switch s.typ {
		case kString:
			return b.SetString(key, value)
		case kInt:
			i, err := strconv.Atoi(value)
			if err != nil {
				return fmt.Errorf("invalid integer value for %s: %w", key, err)
			}
			return b.SetInt(key, i)
		}
	}

	return fmt.Errorf("unknown config key: %q", key)
}

// ValidKeys returns the list of valid non-secret config key names.
func ValidKeys() []string {
	var keys []string
	for _, s := range specs {
		if !s.secret {
			keys = append(keys, s.key)
		}
	}
	return keys
}
