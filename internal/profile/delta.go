package profile

// ProfileDelta describes a set of changes to apply to the user profile.
// It is produced by the preference extraction pipeline and applied atomically
// by Manager.ApplyDelta.
type ProfileDelta struct {
	AddPreferences    []string
	RemovePreferences []string
	UpdateFields      map[string]string
}
