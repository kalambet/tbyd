package storage

import (
	"errors"
	"time"
)

// ErrNotFound is returned when a requested record does not exist.
var ErrNotFound = errors.New("not found")

type Interaction struct {
	ID             string
	CreatedAt      time.Time
	UserQuery      string
	EnrichedPrompt string
	CloudModel     string
	CloudResponse  string
	Status         string
	FeedbackScore  int
	FeedbackNotes  string
	VectorIDs      string // JSON array stored as text
}

type ContextDoc struct {
	ID        string
	Title     string
	Content   string
	Source    string
	Tags      string // JSON array stored as text
	CreatedAt time.Time
	VectorID  string
}
