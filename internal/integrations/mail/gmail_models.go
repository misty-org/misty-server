package mail

type gmailProfile struct {
	EmailAddress  string `json:"emailAddress"`
	MessagesTotal int64  `json:"messagesTotal"`
	ThreadsTotal  int64  `json:"threadsTotal"`
	HistoryID     string `json:"historyId"`
}

type gmailLabel struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Type           string `json:"type"`
	MessagesTotal  int64  `json:"messagesTotal"`
	MessagesUnread int64  `json:"messagesUnread"`
	ThreadsTotal   int64  `json:"threadsTotal"`
	ThreadsUnread  int64  `json:"threadsUnread"`
	Color          struct {
		TextColor       string `json:"textColor"`
		BackgroundColor string `json:"backgroundColor"`
	} `json:"color"`
}

type gmailThreadList struct {
	Threads            []gmailThread `json:"threads"`
	NextPageToken      string        `json:"nextPageToken"`
	ResultSizeEstimate int64         `json:"resultSizeEstimate"`
}

type gmailThread struct {
	ID       string         `json:"id"`
	Snippet  string         `json:"snippet"`
	Messages []gmailMessage `json:"messages"`
}

type gmailMessage struct {
	ID           string    `json:"id"`
	ThreadID     string    `json:"threadId"`
	LabelIDs     []string  `json:"labelIds"`
	Snippet      string    `json:"snippet"`
	InternalDate string    `json:"internalDate"`
	Payload      gmailPart `json:"payload"`
}

type gmailPart struct {
	PartID   string        `json:"partId"`
	MimeType string        `json:"mimeType"`
	Filename string        `json:"filename"`
	Headers  []gmailHeader `json:"headers"`
	Body     gmailPartBody `json:"body"`
	Parts    []gmailPart   `json:"parts"`
}

type gmailHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type gmailPartBody struct {
	AttachmentID string `json:"attachmentId"`
	Size         int64  `json:"size"`
	Data         string `json:"data"`
}

type gmailDraft struct {
	ID      string       `json:"id"`
	Message gmailMessage `json:"message"`
}

func normalizeGmailFolder(accountID string, label gmailLabel) Folder {
	return Folder{
		Provenance: Provenance{Provider: ProviderGmail, ProviderID: label.ID, AccountID: accountID},
		Name:       label.Name, Kind: gmailFolderKind(label.ID), System: label.Type == "system",
		Total: label.ThreadsTotal, Unread: label.ThreadsUnread,
		TextColor: label.Color.TextColor, Background: label.Color.BackgroundColor,
	}
}

func gmailFolderKind(id string) FolderKind {
	switch id {
	case "INBOX":
		return FolderInbox
	case "SENT":
		return FolderSent
	case "DRAFT":
		return FolderDrafts
	case "TRASH":
		return FolderTrash
	case "SPAM":
		return FolderSpam
	case "STARRED":
		return FolderStarred
	case "IMPORTANT":
		return FolderImportant
	default:
		return FolderCustom
	}
}
