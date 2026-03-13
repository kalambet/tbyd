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
	return setKeyWith(newPlatformBackend(), key, value)
}

// setKeyWith is the injectable implementation used by SetKey and tests.
func setKeyWith(b ConfigBackend, key, value string) error {
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
		case kBool:
			if _, err := strconv.ParseBool(value); err != nil {
				return fmt.Errorf("invalid boolean value for %s: %w", key, err)
			}
			return b.SetString(key, value)
		}
	}

	return fmt.Errorf("unknown config key: %q", key)
}

// IsKeySet reports whether the given key has been explicitly stored in the
// platform backend (ignoring environment variable overrides and defaults).
// It uses GetString for all key types because SetKey stores kBool values via
// SetString (see the kBool case above). If SetKey ever switches to a typed
// setter for booleans, this function must be updated in tandem.
func IsKeySet(key string) (bool, error) {
	return isKeySetWith(newPlatformBackend(), key)
}

// isKeySetWith is the injectable implementation used by IsKeySet and tests.
func isKeySetWith(b ConfigBackend, key string) (bool, error) {
	_, ok, err := b.GetString(key)
	if err != nil {
		return false, err
	}
	return ok, nil
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
