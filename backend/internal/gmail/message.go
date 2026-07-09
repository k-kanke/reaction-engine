package gmail

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"mime"
	"mime/multipart"
	"net/textproto"
)

// attachmentLineWidth is RFC 2045's 76-character limit for base64-encoded
// body lines.
const attachmentLineWidth = 76

// buildRawMessage builds one RFC 2822 multipart/mixed message (a plain
// text body, plus attachment when non-empty) and returns it base64url
// encoded, as gmailapi.Message.Raw requires.
func buildRawMessage(from, to, subject, body string, attachment []byte, attachmentFilename string) (string, error) {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	fmt.Fprintf(&buf, "From: %s\r\n", from)
	fmt.Fprintf(&buf, "To: %s\r\n", to)
	fmt.Fprintf(&buf, "Subject: %s\r\n", mime.QEncoding.Encode("UTF-8", subject))
	fmt.Fprintf(&buf, "MIME-Version: 1.0\r\n")
	fmt.Fprintf(&buf, "Content-Type: multipart/mixed; boundary=%s\r\n\r\n", writer.Boundary())

	textHeader := textproto.MIMEHeader{}
	textHeader.Set("Content-Type", "text/plain; charset=UTF-8")
	textPart, err := writer.CreatePart(textHeader)
	if err != nil {
		return "", fmt.Errorf("create text part: %w", err)
	}
	if _, err := textPart.Write([]byte(body)); err != nil {
		return "", fmt.Errorf("write text part: %w", err)
	}

	if len(attachment) > 0 {
		attachmentHeader := textproto.MIMEHeader{}
		attachmentHeader.Set("Content-Type", "application/pdf")
		attachmentHeader.Set("Content-Transfer-Encoding", "base64")
		attachmentHeader.Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, attachmentFilename))
		attachmentPart, err := writer.CreatePart(attachmentHeader)
		if err != nil {
			return "", fmt.Errorf("create attachment part: %w", err)
		}
		encoded := base64.StdEncoding.EncodeToString(attachment)
		for i := 0; i < len(encoded); i += attachmentLineWidth {
			end := min(i+attachmentLineWidth, len(encoded))
			if _, err := fmt.Fprintf(attachmentPart, "%s\r\n", encoded[i:end]); err != nil {
				return "", fmt.Errorf("write attachment part: %w", err)
			}
		}
	}

	if err := writer.Close(); err != nil {
		return "", fmt.Errorf("close multipart writer: %w", err)
	}

	return base64.RawURLEncoding.EncodeToString(buf.Bytes()), nil
}
