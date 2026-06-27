package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPostDeploySendsDevTokenOnly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/dev/deploy" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("X-Plumtree-Dev-Token"); got != "secret" {
			t.Fatalf("dev token = %q", got)
		}
		if got := r.Header.Get("Authorization"); got != "" {
			t.Fatalf("authorization = %q", got)
		}
		var body deployRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.AppName != "counter" {
			t.Fatalf("body = %+v", body)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"app":{"name":"counter"},"deploy":{"id":"dep_000001","claimUrl":"http://server/claim/dep_000001/claim-token","claimExpiresAt":"2026-06-24T12:00:30Z"},"claimUrl":"http://server/claim/dep_000001/claim-token"}`))
	}))
	defer srv.Close()

	res, err := postDeploy(context.Background(), srv.URL, "secret", deployRequest{
		AppName:           "counter",
		AppType:           "tui",
		ArtifactDigest:    "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ArtifactSizeBytes: 1,
		SourceDigest:      "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Deploy.ID != "dep_000001" || responseClaimURL(res) == "" {
		t.Fatalf("response = %+v", res)
	}
}

func TestPutDeploySendsClaimToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/dev/deploy/dep_000001" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if r.Method != http.MethodPut {
			t.Fatalf("method = %q", r.Method)
		}
		if got := r.Header.Get("X-Plumtree-Dev-Token"); got != "secret" {
			t.Fatalf("dev token = %q", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer claim-token" {
			t.Fatalf("authorization = %q", got)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"app":{"handle":"alice/counter","activeDeployId":"dep_000001"},"deploy":{"id":"dep_000001","claimed":true}}`))
	}))
	defer srv.Close()

	res, err := putDeploy(context.Background(), srv.URL, "secret", "dep_000001", "claim-token", deployRequest{
		AppName:           "counter",
		AppType:           "tui",
		ArtifactDigest:    "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ArtifactSizeBytes: 1,
		SourceDigest:      "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.App.Handle != "alice/counter" || !res.Deploy.Claimed {
		t.Fatalf("response = %+v", res)
	}
}

func TestGetDeployInspectSendsClaimToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/dev/deploy/dep_000001" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Fatalf("method = %q", r.Method)
		}
		if got := r.Header.Get("X-Plumtree-Dev-Token"); got != "secret" {
			t.Fatalf("dev token = %q", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer claim-token" {
			t.Fatalf("authorization = %q", got)
		}
		_, _ = w.Write([]byte(`{"app":{"handle":"alice/counter","activeDeployId":"dep_000001","claimed":true},"deploy":{"id":"dep_000001","appType":"tui"},"artifact":{"digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","sizeBytes":12}}`))
	}))
	defer srv.Close()

	res, err := getDeployInspect(context.Background(), srv.URL, "secret", "dep_000001", "claim-token")
	if err != nil {
		t.Fatal(err)
	}
	if res.App.Handle != "alice/counter" || res.Deploy.ID != "dep_000001" {
		t.Fatalf("response = %+v", res)
	}
}

func TestGetDeployLogsSendsClaimToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/dev/deploy/dep_000001/logs" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("X-Plumtree-Dev-Token"); got != "secret" {
			t.Fatalf("dev token = %q", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer claim-token" {
			t.Fatalf("authorization = %q", got)
		}
		_, _ = w.Write([]byte(`{"sessions":[{"id":"ses_000001","deployId":"dep_000001","startedAt":"2026-06-24T12:00:00Z"}]}`))
	}))
	defer srv.Close()

	res, err := getDeployLogs(context.Background(), srv.URL, "secret", "dep_000001", "claim-token")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Sessions) != 1 || res.Sessions[0].ID != "ses_000001" {
		t.Fatalf("response = %+v", res)
	}
}
