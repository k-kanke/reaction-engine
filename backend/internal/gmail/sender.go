// Package gmail is cmd/gmail-sender's Gmail API boundary
// (GMAIL_SEND_BACKEND=real). It replaces the earlier local stub -- see
// cmd/gmail-sender/README.md -- with a real send through the Gmail API,
// authenticated as the sending account via an OAuth2 refresh token minted
// once by cmd/gmail-oauth-setup (never a service account: personal Gmail
// accounts, unlike Google Workspace, don't support domain-wide
// delegation).
package gmail

import (
	"context"
	"fmt"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	gmailapi "google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"
)

// Sender lets cmd/gmail-sender send a report email without depending on
// the Gmail API directly, matching this codebase's ...Store/...Reader
// interface-per-backend convention (internal/media.MediaReader,
// internal/writer.JSONLStore).
type Sender interface {
	// Send delivers one email from the account GmailSender was
	// constructed for. attachment/attachmentFilename are omitted from the
	// message when attachment is empty.
	Send(ctx context.Context, to, subject, body string, attachment []byte, attachmentFilename string) error
}

// GmailSender sends through the real Gmail API using OAuth2 user
// credentials (never Application Default Credentials -- there is no
// service account identity that can send as a personal Gmail address).
type GmailSender struct {
	service *gmailapi.Service
	from    string
}

// NewGmailSender builds a Gmail API client from a long-lived OAuth2
// refresh token. clientID/clientSecret identify the OAuth client that
// issued refreshToken (cmd/gmail-oauth-setup uses the same pair), and from
// is the Gmail address that owns it -- used only for the message's From
// header; the Gmail API itself always sends as whichever account the
// token belongs to, regardless of this value.
func NewGmailSender(ctx context.Context, clientID, clientSecret, refreshToken, from string) (*GmailSender, error) {
	cfg := &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Endpoint:     google.Endpoint,
		Scopes:       []string{gmailapi.GmailSendScope},
	}
	tokenSource := cfg.TokenSource(ctx, &oauth2.Token{RefreshToken: refreshToken})

	service, err := gmailapi.NewService(ctx, option.WithTokenSource(tokenSource))
	if err != nil {
		return nil, fmt.Errorf("gmail: new service: %w", err)
	}
	return &GmailSender{service: service, from: from}, nil
}

func (s *GmailSender) Send(ctx context.Context, to, subject, body string, attachment []byte, attachmentFilename string) error {
	raw, err := buildRawMessage(s.from, to, subject, body, attachment, attachmentFilename)
	if err != nil {
		return fmt.Errorf("gmail: build message: %w", err)
	}

	message := &gmailapi.Message{Raw: raw}
	if _, err := s.service.Users.Messages.Send("me", message).Context(ctx).Do(); err != nil {
		return fmt.Errorf("gmail: send message: %w", err)
	}
	return nil
}
