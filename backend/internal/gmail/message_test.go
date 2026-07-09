package gmail

import (
	"bytes"
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"strings"
	"testing"
)

func TestBuildRawMessage_TextAndAttachment(t *testing.T) {
	attachment := []byte("%PDF-1.4 fake pdf bytes for testing")

	raw, err := buildRawMessage("sender@example.com", "recipient@example.com", "テスト件名", "本文です", attachment, "report.pdf")
	if err != nil {
		t.Fatalf("buildRawMessage: %v", err)
	}

	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		t.Fatalf("raw message is not valid base64url: %v", err)
	}

	msg, err := mail.ReadMessage(bytes.NewReader(decoded))
	if err != nil {
		t.Fatalf("decoded message is not a valid RFC 2822 message: %v", err)
	}

	if got := msg.Header.Get("From"); got != "sender@example.com" {
		t.Errorf("From = %q, want sender@example.com", got)
	}
	if got := msg.Header.Get("To"); got != "recipient@example.com" {
		t.Errorf("To = %q, want recipient@example.com", got)
	}

	wordDecoder := new(mime.WordDecoder)
	subject, err := wordDecoder.DecodeHeader(msg.Header.Get("Subject"))
	if err != nil {
		t.Fatalf("decode subject: %v", err)
	}
	if subject != "テスト件名" {
		t.Errorf("Subject = %q, want テスト件名", subject)
	}

	mediaType, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil {
		t.Fatalf("parse Content-Type: %v", err)
	}
	if !strings.HasPrefix(mediaType, "multipart/") {
		t.Fatalf("Content-Type = %q, want multipart/*", mediaType)
	}

	reader := multipart.NewReader(msg.Body, params["boundary"])

	part, err := reader.NextPart()
	if err != nil {
		t.Fatalf("read text part: %v", err)
	}
	textBody, err := io.ReadAll(part)
	if err != nil {
		t.Fatalf("read text part body: %v", err)
	}
	if string(textBody) != "本文です" {
		t.Errorf("text part body = %q, want 本文です", string(textBody))
	}

	part, err = reader.NextPart()
	if err != nil {
		t.Fatalf("read attachment part: %v", err)
	}
	if got := part.Header.Get("Content-Type"); got != "application/pdf" {
		t.Errorf("attachment Content-Type = %q, want application/pdf", got)
	}
	_, dispositionParams, err := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
	if err != nil {
		t.Fatalf("parse Content-Disposition: %v", err)
	}
	if dispositionParams["filename"] != "report.pdf" {
		t.Errorf("attachment filename = %q, want report.pdf", dispositionParams["filename"])
	}

	attachmentBody, err := io.ReadAll(part)
	if err != nil {
		t.Fatalf("read attachment part body: %v", err)
	}
	decodedAttachment, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(string(attachmentBody), "\r\n", ""))
	if err != nil {
		t.Fatalf("decode attachment base64: %v", err)
	}
	if !bytes.Equal(decodedAttachment, attachment) {
		t.Errorf("attachment bytes = %q, want %q", decodedAttachment, attachment)
	}

	if _, err := reader.NextPart(); err != io.EOF {
		t.Errorf("expected exactly 2 parts, got extra part or unexpected error: %v", err)
	}
}

func TestBuildRawMessage_NoAttachment(t *testing.T) {
	raw, err := buildRawMessage("sender@example.com", "recipient@example.com", "subject", "body", nil, "")
	if err != nil {
		t.Fatalf("buildRawMessage: %v", err)
	}

	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		t.Fatalf("raw message is not valid base64url: %v", err)
	}

	msg, err := mail.ReadMessage(bytes.NewReader(decoded))
	if err != nil {
		t.Fatalf("decoded message is not a valid RFC 2822 message: %v", err)
	}

	_, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil {
		t.Fatalf("parse Content-Type: %v", err)
	}

	reader := multipart.NewReader(msg.Body, params["boundary"])
	if _, err := reader.NextPart(); err != nil {
		t.Fatalf("read text part: %v", err)
	}
	if _, err := reader.NextPart(); err != io.EOF {
		t.Errorf("expected exactly 1 part with no attachment, got extra part or unexpected error: %v", err)
	}
}
