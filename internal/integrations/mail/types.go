// Package mail defines provider-neutral mailbox concepts and adapters.
package mail

import (
	"context"
	"errors"
	"time"
)

const (
	ProviderGmail   = "gmail"
	ProviderOutlook = "outlook"
)

var (
	ErrInvalidConfiguration = errors.New("mail provider configuration is invalid")
	ErrInvalidInput         = errors.New("mail provider input is invalid")
	ErrResponseTooLarge     = errors.New("mail provider response is too large")
	ErrBodyTooLarge         = errors.New("mail body is too large")
)

// Provenance keeps the external identity attached to every normalized value.
type Provenance struct {
	Provider   string
	ProviderID string
	AccountID  string
}

type Address struct {
	Name  string
	Email string
}

type Account struct {
	Provenance
	Email       string
	DisplayName string
	Total       int64
	Unread      int64
}

type FolderKind string

const (
	FolderCustom    FolderKind = "custom"
	FolderInbox     FolderKind = "inbox"
	FolderSent      FolderKind = "sent"
	FolderDrafts    FolderKind = "drafts"
	FolderTrash     FolderKind = "trash"
	FolderSpam      FolderKind = "spam"
	FolderStarred   FolderKind = "starred"
	FolderImportant FolderKind = "important"
)

type Folder struct {
	Provenance
	Name       string
	Kind       FolderKind
	System     bool
	Total      int64
	Unread     int64
	TextColor  string
	Background string
}

type Attachment struct {
	Provenance
	MessageID   string
	Filename    string
	ContentType string
	Size        int64
	Inline      bool
	ContentID   string
}

// Body contains the normalized email content (both plain text and optional rich HTML).
type Body struct {
	Text      string
	HTML      string
	HadHTML   bool
	Truncated bool
}

type Message struct {
	Provenance
	ThreadID    string
	RFC822ID    string
	Subject     string
	From        Address
	To          []Address
	Cc          []Address
	Bcc         []Address
	ReplyTo     []Address
	SentAt      time.Time
	Snippet     string
	Body        Body
	Labels      []string
	Unread      bool
	Starred     bool
	Draft       bool
	Attachments []Attachment
}

type Thread struct {
	Provenance
	Subject       string
	Snippet       string
	Participants  []Address
	Messages      []Message
	Labels        []string
	LastMessageAt time.Time
	Unread        bool
	Starred       bool
}

type ThreadPage struct {
	Threads        []Thread
	NextPageToken  string
	EstimatedTotal int64
}

type ListThreadsRequest struct {
	PageToken string
	Query     string
	FolderIDs []string
	PageSize  int
}

// ThreadChanges uses pointers so false remains an explicit requested state.
type ThreadChanges struct {
	Read     *bool
	Archived *bool
	Starred  *bool
}

type ThreadChangeResult struct {
	ThreadID      string
	AddedLabels   []string
	RemovedLabels []string
}

type DraftAttachment struct {
	Filename    string
	ContentType string
	Data        []byte
	Inline      bool
	ContentID   string
}

type DraftInput struct {
	ThreadID    string
	To          []Address
	Cc          []Address
	Bcc         []Address
	ReplyTo     []Address
	Subject     string
	Text        string
	Attachments []DraftAttachment
}

type Draft struct {
	Provenance
	ThreadID string
	Message  Message
}

// Provider exposes mailbox operations without leaking provider response types.
// Sending is deliberately limited to an explicit existing-draft operation.
type Provider interface {
	Account(context.Context) (Account, error)
	ListFolders(context.Context) ([]Folder, error)
	ListThreads(context.Context, ListThreadsRequest) (ThreadPage, error)
	GetThread(context.Context, string) (Thread, error)
	ModifyThread(context.Context, string, ThreadChanges) (ThreadChangeResult, error)
	CreateDraft(context.Context, DraftInput) (Draft, error)
	UpdateDraft(context.Context, string, DraftInput) (Draft, error)
	SendDraft(context.Context, string) (Message, error)
}

type ProviderError struct {
	StatusCode int
	Code       string
	Message    string
}

func (e *ProviderError) Error() string {
	if e.Message == "" {
		return "mail provider request failed"
	}
	return "mail provider request failed: " + e.Message
}
