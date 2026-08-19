package mail

type graphUser struct {
	ID                string `json:"id"`
	DisplayName       string `json:"displayName"`
	Mail              string `json:"mail"`
	UserPrincipalName string `json:"userPrincipalName"`
}

type graphFolder struct {
	ID              string `json:"id"`
	DisplayName     string `json:"displayName"`
	WellKnownName   string `json:"wellKnownName"`
	TotalItemCount  int64  `json:"totalItemCount"`
	UnreadItemCount int64  `json:"unreadItemCount"`
}

type graphEmailAddress struct {
	Name    string `json:"name"`
	Address string `json:"address"`
}

type graphRecipient struct {
	EmailAddress graphEmailAddress `json:"emailAddress"`
}

type graphItemBody struct {
	ContentType string `json:"contentType"`
	Content     string `json:"content"`
}

type graphFlag struct {
	FlagStatus string `json:"flagStatus"`
}

type graphAttachment struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	ContentType string `json:"contentType"`
	Size        int64  `json:"size"`
	IsInline    bool   `json:"isInline"`
	ContentID   string `json:"contentId"`
}

type graphMessage struct {
	ID                string            `json:"id"`
	ConversationID    string            `json:"conversationId"`
	InternetMessageID string            `json:"internetMessageId"`
	Subject           string            `json:"subject"`
	BodyPreview       string            `json:"bodyPreview"`
	Body              graphItemBody     `json:"body"`
	From              graphRecipient    `json:"from"`
	ToRecipients      []graphRecipient  `json:"toRecipients"`
	CcRecipients      []graphRecipient  `json:"ccRecipients"`
	BccRecipients     []graphRecipient  `json:"bccRecipients"`
	ReplyTo           []graphRecipient  `json:"replyTo"`
	ReceivedDateTime  string            `json:"receivedDateTime"`
	SentDateTime      string            `json:"sentDateTime"`
	IsRead            bool              `json:"isRead"`
	IsDraft           bool              `json:"isDraft"`
	ParentFolderID    string            `json:"parentFolderId"`
	Flag              graphFlag         `json:"flag"`
	Attachments       []graphAttachment `json:"attachments"`
}

type graphFolderPage struct {
	Value    []graphFolder `json:"value"`
	NextLink string        `json:"@odata.nextLink"`
}

type graphMessagePage struct {
	Value    []graphMessage `json:"value"`
	NextLink string         `json:"@odata.nextLink"`
}

type graphAttachmentPage struct {
	Value    []graphAttachment `json:"value"`
	NextLink string            `json:"@odata.nextLink"`
}

type graphErrorEnvelope struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}
