package profile

// Profile represents the user's "digital self" — a structured view of their
// identity, communication preferences, expertise, interests, and opinions.
type Profile struct {
	Identity              IdentityProfile      `json:"identity"`
	Communication         CommunicationProfile `json:"communication"`
	Interests             Interests            `json:"interests"`
	Opinions              []string             `json:"opinions"`
	Preferences           []string             `json:"preferences"`
	Language              string               `json:"language,omitempty"`
	CloudModelPreference  string               `json:"cloud_model_preference,omitempty"`
}

// IdentityProfile captures the user's professional role, expertise, and working context.
type IdentityProfile struct {
	Role           string            `json:"role,omitempty"`
	Expertise      map[string]string `json:"expertise,omitempty"` // domain → level (e.g. "go" → "expert")
	WorkingContext *WorkingContext    `json:"working_context,omitempty"`
}

// WorkingContext captures the user's current project and team context.
type WorkingContext struct {
	CurrentProjects []string `json:"current_projects"`
	TeamSize        string   `json:"team_size,omitempty"`
	TechStack       []string `json:"tech_stack"`
}

// CommunicationProfile captures how the user prefers AI responses.
type CommunicationProfile struct {
	Tone        string `json:"tone,omitempty"`         // e.g. "direct, no fluff"
	Format      string `json:"format,omitempty"`       // e.g. "markdown with code"
	DetailLevel string `json:"detail_level,omitempty"` // e.g. "medium — skip basics, show trade-offs"
}

// Interests captures the user's primary and emerging interests.
type Interests struct {
	Primary  []string `json:"primary"`
	Emerging []string `json:"emerging"`
}
