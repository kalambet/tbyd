package config

// ConfigBackend abstracts platform-specific config storage.
// macOS uses UserDefaults (via `defaults` CLI), Linux can use
// XDG config files, GSettings, or any other native mechanism.
type ConfigBackend interface {
	GetString(key string) (val string, ok bool, err error)
	GetInt(key string) (val int, ok bool, err error)
	SetString(key, val string) error
	SetInt(key string, val int) error
	Delete(key string) error
}
