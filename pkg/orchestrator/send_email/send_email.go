// Package send_email sends job-run/report emails over SMTP and logs every
// attempt (success or failure) to the send_email table so there's a durable
// record of what was sent, to whom, and when, without relying on the SMTP
// provider's own outbound log.
package send_email

import (
	"bytes"
	"database/sql"
	"encoding/base64"
	"fmt"
	"mime/multipart"
	"net/smtp"
	"net/textproto"
	"os"
	"time"
)

// Config holds the configuration for sending emails.
type Config struct {
	Host      string
	Port      string
	Username  string
	Password  string
	Recipient string
}

// LoadConfig loads the email configuration from environment variables.
func LoadConfig() Config {
	host := os.Getenv("SMTP_HOST")
	if host == "" {
		host = "smtp.gmail.com"
	}
	port := os.Getenv("SMTP_PORT")
	if port == "" {
		port = "587" // default TLS port for Gmail
	}
	return Config{
		Host:      host,
		Port:      port,
		Username:  os.Getenv("SMTP_USER"),
		Password:  os.Getenv("SMTP_PASS"),
		Recipient: os.Getenv("EMAIL_RECIPIENT"),
	}
}

// SendEmail sends a simple text email to the configured recipient and logs
// the attempt to db (pass nil to skip logging, e.g. in tests).
func SendEmail(db *sql.DB, jobContext, subject, body string) error {
	return send(db, jobContext, subject, body, false, nil)
}

// SendHTMLEmail sends an HTML email (e.g. a signal digest table) to the
// configured recipient and logs the attempt to db (pass nil to skip
// logging, e.g. in tests).
func SendHTMLEmail(db *sql.DB, jobContext, subject, htmlBody string) error {
	return send(db, jobContext, subject, htmlBody, true, nil)
}

// Attachment is one file to attach to an outgoing email. ContentType
// defaults to "application/octet-stream" when left blank.
type Attachment struct {
	Filename    string
	Data        []byte
	ContentType string
}

// SendHTMLEmailWithAttachments sends an HTML email with zero or more file
// attachments (e.g. a SQLite DB snapshot) to the configured recipient and
// logs the attempt to db (pass nil to skip logging, e.g. in tests).
func SendHTMLEmailWithAttachments(db *sql.DB, jobContext, subject, htmlBody string, attachments ...Attachment) error {
	return send(db, jobContext, subject, htmlBody, true, attachments)
}

func send(db *sql.DB, jobContext, subject, body string, html bool, attachments []Attachment) error {
	cfg := LoadConfig()
	if cfg.Host == "" || cfg.Username == "" || cfg.Password == "" || cfg.Recipient == "" {
		err := fmt.Errorf("email configuration is incomplete (check SMTP_USER, SMTP_PASS, and EMAIL_RECIPIENT in the config object)")
		logAttempt(db, cfg.Recipient, subject, jobContext, "FAILED", err.Error())
		return err
	}

	auth := smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)
	from := "darian.hickman+trade@gmail.com"
	var msg []byte
	var err error
	if len(attachments) == 0 {
		msg = buildMessage(from, cfg.Recipient, subject, body, html)
	} else {
		msg, err = buildMultipartMessage(from, cfg.Recipient, subject, body, html, attachments)
		if err != nil {
			logAttempt(db, cfg.Recipient, subject, jobContext, "FAILED", err.Error())
			return err
		}
	}

	addr := fmt.Sprintf("%s:%s", cfg.Host, cfg.Port)
	err = smtp.SendMail(addr, auth, from, []string{cfg.Recipient}, msg)
	if err != nil {
		logAttempt(db, cfg.Recipient, subject, jobContext, "FAILED", err.Error())
		return err
	}
	logAttempt(db, cfg.Recipient, subject, jobContext, "SENT", "")
	return nil
}

// buildMessage renders the raw RFC 5322 message body/headers smtp.SendMail
// expects, with a text/plain or text/html Content-Type depending on html.
func buildMessage(from, to, subject, body string, html bool) []byte {
	contentType := "text/plain; charset=\"UTF-8\""
	if html {
		contentType = "text/html; charset=\"UTF-8\""
	}

	return fmt.Appendf(nil, "From: %s\r\n"+
		"To: %s\r\n"+
		"Subject: %s\r\n"+
		"MIME-Version: 1.0\r\n"+
		"Content-Type: %s\r\n"+
		"\r\n"+
		"%s\r\n", from, to, subject, contentType, body)
}

// buildMultipartMessage renders a multipart/mixed RFC 5322 message: the
// text/html (or text/plain) body as the first part, followed by one
// base64-encoded part per attachment.
func buildMultipartMessage(from, to, subject, body string, html bool, attachments []Attachment) ([]byte, error) {
	contentType := "text/plain; charset=\"UTF-8\""
	if html {
		contentType = "text/html; charset=\"UTF-8\""
	}

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	fmt.Fprintf(&buf, "From: %s\r\n", from)
	fmt.Fprintf(&buf, "To: %s\r\n", to)
	fmt.Fprintf(&buf, "Subject: %s\r\n", subject)
	buf.WriteString("MIME-Version: 1.0\r\n")
	fmt.Fprintf(&buf, "Content-Type: multipart/mixed; boundary=%q\r\n\r\n", mw.Boundary())

	bodyHeader := textproto.MIMEHeader{"Content-Type": {contentType}}
	bodyPart, err := mw.CreatePart(bodyHeader)
	if err != nil {
		return nil, fmt.Errorf("create body part: %w", err)
	}
	if _, err := bodyPart.Write([]byte(body)); err != nil {
		return nil, fmt.Errorf("write body part: %w", err)
	}

	for _, a := range attachments {
		ct := a.ContentType
		if ct == "" {
			ct = "application/octet-stream"
		}
		header := textproto.MIMEHeader{
			"Content-Type":              {ct},
			"Content-Transfer-Encoding": {"base64"},
			"Content-Disposition":       {fmt.Sprintf("attachment; filename=%q", a.Filename)},
		}
		part, err := mw.CreatePart(header)
		if err != nil {
			return nil, fmt.Errorf("create attachment part %q: %w", a.Filename, err)
		}
		encoded := base64.StdEncoding.EncodeToString(a.Data)
		for len(encoded) > 0 {
			n := min(len(encoded), 76)
			if _, err := part.Write([]byte(encoded[:n] + "\r\n")); err != nil {
				return nil, fmt.Errorf("write attachment part %q: %w", a.Filename, err)
			}
			encoded = encoded[n:]
		}
	}

	if err := mw.Close(); err != nil {
		return nil, fmt.Errorf("close multipart writer: %w", err)
	}
	return buf.Bytes(), nil
}

// logAttempt writes one send_email audit row. db may be nil (skips
// logging); logging failures are swallowed since they must never mask the
// underlying send outcome already returned to the caller.
func logAttempt(db *sql.DB, toAddr, subject, jobContext, status, detail string) {
	if db == nil {
		return
	}
	if err := EnsureSchema(db); err != nil {
		return
	}
	_, _ = db.Exec(`
INSERT INTO send_email (to_addr, subject, job_context, status, detail, sent_at)
VALUES (?, ?, ?, ?, ?, ?)`,
		toAddr, subject, jobContext, status, detail, time.Now().UTC().Format(time.RFC3339))
}
