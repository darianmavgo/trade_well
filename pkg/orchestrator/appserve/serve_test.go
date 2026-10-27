package appserve

import (
	"context"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

// The shutdown path App Engine's SIGTERM takes: serve real requests, then on
// cancellation stop cleanly and return nil so Serve's deferred replicator flush runs.
func TestServeUntilDoneStopsCleanlyWhenTheContextIsCancelled(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/ok", func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "fine") })

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serveUntilDone(ctx, &http.Server{Handler: mux}, ln, time.Second) }()

	url := "http://" + ln.Addr().String() + "/ok"
	var resp *http.Response
	for i := 0; i < 50; i++ { // wait for the goroutine to start serving
		if resp, err = http.Get(url); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("server never came up: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "fine" {
		t.Fatalf("body = %q", body)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("a clean shutdown must return nil, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("serveUntilDone did not return after the context was cancelled")
	}
	if _, err := http.Get(url); err == nil {
		t.Error("the listener must be closed after shutdown")
	}
}

func TestServeUntilDoneReturnsARealServerError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ln.Close() // serving on a closed listener fails immediately
	err = serveUntilDone(context.Background(), &http.Server{}, ln, time.Second)
	if err == nil {
		t.Error("a listener error must be returned, not swallowed")
	}
}
