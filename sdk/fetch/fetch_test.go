package fetch

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	legacy "github.com/Ceinl/plumtree/sdk"
)

func TestUnavailableErrorMapping(t *testing.T) {
	if !errors.Is(normalize(legacy.ErrFetchUnavailable), ErrUnavailable) {
		t.Fatal("unavailable host error was not mapped to the package contract")
	}
}

func TestRequestCancelsAfterFetchStarts(t *testing.T) {
	started := make(chan struct{})
	requestCanceled := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		close(started)
		select {
		case <-request.Context().Done():
			close(requestCanceled)
		case <-release:
		}
	}))
	defer server.Close()
	defer close(release)

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan Result, 1)
	go func() { result <- Get(server.URL).Run(ctx) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("request did not start")
	}
	cancel()
	select {
	case got := <-result:
		if !errors.Is(got.Err, context.Canceled) {
			t.Fatalf("cancelled request err = %v", got.Err)
		}
	case <-time.After(time.Second):
		t.Fatal("request did not stop after cancellation")
	}
	select {
	case <-requestCanceled:
	case <-time.After(time.Second):
		t.Fatal("underlying request did not receive cancellation")
	}
}

func TestRequestValidationAndCancellation(t *testing.T) {
	if result := Request("GET", "", nil).Run(context.Background()); !errors.Is(result.Err, ErrInvalid) {
		t.Fatalf("empty URL err = %v", result.Err)
	}
	if result := Get(strings.Repeat("x", 2049)).Run(context.Background()); !errors.Is(result.Err, ErrTooLarge) {
		t.Fatalf("large URL err = %v", result.Err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if result := Get("http://127.0.0.1").Run(ctx); !errors.Is(result.Err, context.Canceled) {
		t.Fatalf("cancelled err = %v", result.Err)
	}
}
