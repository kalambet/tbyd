package storage

import (
	"errors"
	"time"
)

// ErrNotFound is returned when a requested record does not exist.
var ErrNotFound = errors.New("not found")

// ErrAlreadyReviewed is returned when a pending delta has already been accepted or rejected.
var ErrAlreadyReviewed = errors.New("delta already reviewed")

type Interaction struct {
	ID             string    `json:"id"`
	CreatedAt      time.Time `json:"created_at"`
	UserQuery      string    `json:"user_query"`
	EnrichedPrompt string    `json:"enriched_prompt"`
	CloudModel     string    `json:"cloud_model"`
	CloudResponse  string    `json:"cloud_response"`
	Status         string    `json:"status"`
	FeedbackScore  int       `json:"feedback_score"`
	FeedbackNotes  string    `json:"feedback_notes,omitempty"`
	VectorIDs      string    `json:"vector_ids"` // JSON array stored as text
}

type Job struct {
	ID          string
	Type        string
	PayloadJSON string
	Status      string // "pending", "running", "completed", "failed"
	Attempts    int
	MaxAttempts int
	RunAfter    time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
	LastError   string
}

type ContextDoc struct {
	ID        string
	Title     string
	Content   string
	Source    string
	Tags      string // JSON array stored as text
	CreatedAt time.Time
	VectorID  string
	Metadata  string // JSON object stored as text
}

// PendingProfileDelta is a proposed change to the user profile waiting for
// human review. It is produced by background synthesis jobs and reviewed via
// the management API.
type PendingProfileDelta struct {
	ID          string
	DeltaJSON   string
	Description string
	Source      string     // "nightly_synthesis" | "feedback_aggregation"
	Accepted    *bool      // nil = not reviewed
	ReviewedAt  *time.Time
	CreatedAt   time.Time
}
