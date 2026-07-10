package buildworker

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"
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

func TestServiceBuildAdmissionBoundsQueue(t *testing.T) {
	s := &Service{buildSlots: make(chan struct{}, 1), queueSlots: make(chan struct{}, 1)}
	if !s.acquireBuild(context.Background()) {
		t.Fatal("first build should acquire the worker")
	}
	second := make(chan bool, 1)
	go func() { second <- s.acquireBuild(context.Background()) }()
	deadline := time.Now().Add(time.Second)
	for len(s.queueSlots) != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(s.queueSlots) != 1 {
		t.Fatal("second build did not enter the queue")
	}
	if s.acquireBuild(context.Background()) {
		t.Fatal("build above the worker and queue capacity was admitted")
	}
	s.releaseBuild()
	if !<-second {
		t.Fatal("queued build was not admitted after release")
	}
	s.releaseBuild()
}

func TestServiceRejectsBadToken(t *testing.T) {
	srv := httptest.NewServer(NewService(testBuilder(Config{}), "secret").Handler())
	defer srv.Close()

	client := NewClient(srv.URL, "wrong")
	if _, err := client.Build(context.Background(), Request{Source: []byte("x")}); err == nil {
		t.Fatal("expected auth rejection")
	}
}
