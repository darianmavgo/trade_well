package trader

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// LoggingRoundTripper intercepts and logs all HTTP traffic between schwaber and Charles Schwab API endpoints.
type LoggingRoundTripper struct {
	Transport http.RoundTripper
	Debug     bool
	LogFile   string
	mu        sync.Mutex
}

func CreateBaseTransport() http.RoundTripper {
	baseTransport, ok := http.DefaultTransport.(*http.Transport)
	var tr *http.Transport
	if ok {
		tr = baseTransport.Clone()
	} else {
		tr = &http.Transport{Proxy: http.ProxyFromEnvironment}
	}

	if certPath := os.Getenv("SSL_CERT_FILE"); certPath != "" {
		pool, err := x509.SystemCertPool()
		if pool == nil || err != nil {
			pool = x509.NewCertPool()
		}
		if data, err := os.ReadFile(certPath); err == nil {
			pool.AppendCertsFromPEM(data)
			if tr.TLSClientConfig == nil {
				tr.TLSClientConfig = &tls.Config{}
			}
			tr.TLSClientConfig.RootCAs = pool
		}
	}
	return tr
}

func NewLoggingHTTPClient(debug bool, logFilePath string) *http.Client {
	if logFilePath == "" {
		logFilePath = "schwab_api.log"
	}

	return &http.Client{
		Timeout: 30 * time.Second,
		Transport: &LoggingRoundTripper{
			Transport: CreateBaseTransport(),
			Debug:     debug,
			LogFile:   logFilePath,
		},
	}
}

func (l *LoggingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	startTime := time.Now()

	// 1. Capture and duplicate Request Body
	var reqBodyBytes []byte
	if req.Body != nil {
		reqBodyBytes, _ = io.ReadAll(req.Body)
		req.Body = io.NopCloser(bytes.NewBuffer(reqBodyBytes))
	}

	// 2. Execute Request
	transport := l.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	resp, err := transport.RoundTrip(req)
	duration := time.Since(startTime)

	if err != nil {
		l.logEntry(fmt.Sprintf(
			"[%s] ERROR -> %s %s | Duration: %v | Error: %v\n",
			time.Now().Format(time.RFC3339), req.Method, req.URL.String(), duration, err,
		))
		return nil, err
	}

	// 3. Capture and duplicate Response Body
	var respBodyBytes []byte
	if resp.Body != nil {
		respBodyBytes, _ = io.ReadAll(resp.Body)
		resp.Body = io.NopCloser(bytes.NewBuffer(respBodyBytes))
	}

	// 4. Format detailed log output
	logBuf := bytes.NewBuffer(nil)
	logBuf.WriteString(fmt.Sprintf("==================== SCHWAB API CALL ====================\n"))
	logBuf.WriteString(fmt.Sprintf("Timestamp: %s | Duration: %v\n", time.Now().Format("2006-01-02 15:04:05.000"), duration))
	logBuf.WriteString(fmt.Sprintf("Request:   %s %s\n", req.Method, req.URL.String()))

	if len(reqBodyBytes) > 0 {
		logBuf.WriteString(fmt.Sprintf("Req Body:  %s\n", string(reqBodyBytes)))
	}

	logBuf.WriteString(fmt.Sprintf("Response:  Status %d (%s)\n", resp.StatusCode, resp.Status))
	if loc := resp.Header.Get("Location"); loc != "" {
		logBuf.WriteString(fmt.Sprintf("Location:  %s\n", loc))
	}
	if len(respBodyBytes) > 0 {
		logBuf.WriteString(fmt.Sprintf("Resp Body: %s\n", string(respBodyBytes)))
	}
	logBuf.WriteString(fmt.Sprintf("=========================================================\n\n"))

	entry := logBuf.String()

	// Write to console if debug mode enabled
	if l.Debug {
		fmt.Print(entry)
	}

	// Write to file
	l.logEntry(entry)

	return resp, nil
}

func (l *LoggingRoundTripper) logEntry(msg string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	dir := filepath.Dir(l.LogFile)
	if dir != "" && dir != "." {
		_ = os.MkdirAll(dir, 0755)
	}

	f, err := os.OpenFile(l.LogFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return
	}
	defer f.Close()

	_, _ = f.WriteString(msg)
}
