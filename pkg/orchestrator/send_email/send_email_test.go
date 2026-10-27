package send_email

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"os"
	"strings"
	"testing"
	"time"
)

// loadDotEnv is a helper to load the .env file for the test
func loadDotEnv(filepath string) error {
	file, err := os.Open(filepath)
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			k := strings.TrimSpace(parts[0])
			v := strings.Trim(strings.TrimSpace(parts[1]), `"'`)
			if os.Getenv(k) == "" {
				os.Setenv(k, v)
			}
		}
	}
	return scanner.Err()
}

func TestBuildMessageContentType(t *testing.T) {
	plain := string(buildMessage("from@x.com", "to@x.com", "Subject", "plain body", false))
	if !strings.Contains(plain, "Content-Type: text/plain") {
		t.Errorf("expected text/plain Content-Type, got: %s", plain)
	}

	html := string(buildMessage("from@x.com", "to@x.com", "Subject", "<p>html body</p>", true))
	if !strings.Contains(html, "Content-Type: text/html") {
		t.Errorf("expected text/html Content-Type, got: %s", html)
	}
	if !strings.Contains(html, "<p>html body</p>") {
		t.Errorf("expected HTML body preserved, got: %s", html)
	}
}

func TestBuildMultipartMessageWithAttachment(t *testing.T) {
	dbBytes := []byte("fake-sqlite-bytes")
	msg, err := buildMultipartMessage("from@x.com", "to@x.com", "Subject", "<p>body</p>", true, []Attachment{
		{Filename: "appdb.db", Data: dbBytes, ContentType: "application/x-sqlite3"},
	})
	if err != nil {
		t.Fatalf("buildMultipartMessage: %v", err)
	}

	m, err := mail.ReadMessage(strings.NewReader(string(msg)))
	if err != nil {
		t.Fatalf("mail.ReadMessage: %v", err)
	}
	mediaType, params, err := mime.ParseMediaType(m.Header.Get("Content-Type"))
	if err != nil {
		t.Fatalf("parse content type: %v", err)
	}
	if mediaType != "multipart/mixed" {
		t.Fatalf("expected multipart/mixed, got %s", mediaType)
	}

	mr := multipart.NewReader(m.Body, params["boundary"])
	var sawBody, sawAttachment bool
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("next part: %v", err)
		}
		raw, err := io.ReadAll(part)
		if err != nil {
			t.Fatalf("read part: %v", err)
		}
		if disp := part.Header.Get("Content-Disposition"); strings.Contains(disp, "appdb.db") {
			sawAttachment = true
			decoded, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(string(raw), "\r\n", ""))
			if err != nil {
				t.Fatalf("decode attachment: %v", err)
			}
			if string(decoded) != string(dbBytes) {
				t.Errorf("expected attachment bytes %q, got %q", dbBytes, decoded)
			}
		} else if strings.Contains(string(raw), "<p>body</p>") {
			sawBody = true
		}
	}
	if !sawBody {
		t.Errorf("expected to find the HTML body part, raw message: %s", msg)
	}
	if !sawAttachment {
		t.Errorf("expected to find the appdb.db attachment part, raw message: %s", msg)
	}
}

func TestBuildJobReportEmpty(t *testing.T) {
	subject, body := BuildJobReport("scan_market", nil)
	if subject != "scan_market" {
		t.Errorf("expected subject to be exactly the job name, got %q", subject)
	}
	if !strings.Contains(body, "No rows to report") {
		t.Errorf("expected an empty-state message, got: %s", body)
	}
	if strings.Contains(body, "<table") {
		t.Errorf("expected no table for an empty row list, got: %s", body)
	}
}

func TestBuildJobReportWithRows(t *testing.T) {
	rows := []Row{
		{Account: "74234603", Strategy: "sig-voo-buy-tecl", Symbol: "TECL", Signal: "BUY", Status: "STAGED", Detail: "335 shares @ $193.84 (staged_order id=42)"},
		{Account: "74234603", Strategy: "mara_tree", Symbol: "MARA", Signal: "NO_BUY", Status: "NO_BUY", Detail: "no buy signal triggered"},
	}
	subject, body := BuildJobReport("scan_market", rows)
	if subject != "scan_market" {
		t.Errorf("expected subject to be exactly the job name, got %q", subject)
	}
	if !strings.Contains(body, "<table") {
		t.Fatalf("expected a table for a non-empty row list, got: %s", body)
	}
	for _, want := range []string{"74234603", "sig-voo-buy-tecl", "TECL", "STAGED", "mara_tree", "MARA", "NO_BUY"} {
		if !strings.Contains(body, want) {
			t.Errorf("expected report to contain %q, got: %s", want, body)
		}
	}
	for _, h := range []string{"Account", "Strategy", "Symbol", "Signal", "Status", "Detail"} {
		if !strings.Contains(body, ">"+h+"<") {
			t.Errorf("expected column header %q, got: %s", h, body)
		}
	}
}

func TestBuildJobReportEscapesDetail(t *testing.T) {
	_, body := BuildJobReport("scan_market", []Row{
		{Strategy: "x", Symbol: "Y", Signal: "BUY", Status: "ERROR", Detail: "<script>alert(1)</script>"},
	})
	if strings.Contains(body, "<script>") {
		t.Errorf("expected detail text to be HTML-escaped, got: %s", body)
	}
}

func TestBuildJobReportSummary(t *testing.T) {
	subject, body := BuildJobReportSummary("submit_orders", Summary{
		SubjectSuffix: "no staged orders",
		Lines:         []string{"Staged orders found: 0."},
	}, nil)
	if subject != "submit_orders -- no staged orders" {
		t.Errorf("subject = %q", subject)
	}
	for _, want := range []string{"Staged orders found: 0.", "No rows to report"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q: %s", want, body)
		}
	}
	// An empty Summary must be byte-for-byte the legacy BuildJobReport.
	rows := []Row{{Strategy: "s", Symbol: "X", Status: "STAGED"}}
	s1, b1 := BuildJobReport("scan_market", rows)
	s2, b2 := BuildJobReportSummary("scan_market", Summary{}, rows)
	if s1 != s2 || b1 != b2 {
		t.Errorf("empty Summary changed the legacy report")
	}
}

// TestSendEmailLive sends one real email through the configured SMTP
// account -- useful for confirming SMTP_USER/SMTP_PASS/EMAIL_RECIPIENT
// actually work end to end. It is opt-in only (set EMAIL_LIVE_TEST=1): this
// package's .env carries real Gmail credentials, and `go test ./...` must
// never have the side effect of emailing a real inbox on every run.
func TestSendEmailLive(t *testing.T) {
	if os.Getenv("EMAIL_LIVE_TEST") == "" {
		t.Skip("set EMAIL_LIVE_TEST=1 to send a real test email through the configured SMTP account")
	}

	// Attempt to load .env from the root of the project
	err := loadDotEnv("../../../.env")
	if err != nil {
		t.Logf("Could not load ../../.env, assuming env vars are already set or this is CI: %v", err)
	}

	subject := "send_email package: live SMTP test"
	body := fmt.Sprintf("This is a one-off connectivity test of pkg/send_email.SendEmail, sent at %s.", time.Now().Format(time.RFC822))

	if err := SendEmail(nil, "live-test", subject, body); err != nil {
		t.Fatalf("Failed to send email: %v", err)
	}

	t.Log("Successfully sent live test email!")
}
