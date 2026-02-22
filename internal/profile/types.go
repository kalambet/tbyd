package profile

// Profile represents the user's "digital self" — a structured view of their
// identity, communication preferences, expertise, interests, and opinions.
type Profile struct {
	Identity      IdentityProfile
	Communication CommunicationProfile
	Interests     []string
	Expertise     map[string]string // domain → level (e.g. "go" → "expert")
	Opinions      []string
	Preferences   []string
}

// IdentityProfile captures the user's professional role and working context.
type IdentityProfile struct {
	Role           string
	WorkingContext map[string]string // e.g. "current_projects" → "tbyd"
}

// CommunicationProfile captures how the user prefers AI responses.
type CommunicationProfile struct {
	Tone        string // e.g. "direct, no fluff"
	Format      string // e.g. "markdown with code"
	DetailLevel string // e.g. "medium — skip basics, show trade-offs"
}
