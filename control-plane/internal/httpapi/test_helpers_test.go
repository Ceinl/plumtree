package httpapi

import (
	"io"
	"net/http/httptest"
	"testing"
)

func serveTestRequest(t *testing.T, server *Server, method, path string, body io.Reader, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, body)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	return rec
}
