package buildworker

import (
	"context"
	"net/http/httptest"
	"testing"
)

func TestServiceRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles a real wasip1 binary")
	}
	builder := testBuilder(Config{})
	srv := httptest.NewServer(NewService(builder, "secret").Handler())
	defer srv.Close()

	client := NewClient(srv.URL, "secret")
	res, err := client.Build(context.Background(), Request{Source: packProject(t, stdOnlyProject)})
	if err != nil {
		t.Fatalf("client build: %v", err)
	}
	if !res.Success || len(res.WASM) == 0 {
		t.Fatalf("expected successful build, got %+v", res.Failure)
	}
	if res.Digest != SourceDigest(res.WASM) {
		t.Errorf("digest mismatch over the wire")
	}
}

func TestServiceRejectsBadToken(t *testing.T) {
	srv := httptest.NewServer(NewService(testBuilder(Config{}), "secret").Handler())
	defer srv.Close()

	client := NewClient(srv.URL, "wrong")
	if _, err := client.Build(context.Background(), Request{Source: []byte("x")}); err == nil {
		t.Fatal("expected auth rejection")
	}
}
