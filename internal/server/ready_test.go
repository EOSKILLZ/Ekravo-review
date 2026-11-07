package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestReadyzChecksDependency(t *testing.T) {
	get := func(h Handlers) int {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
		testServer(t, h).Handler().ServeHTTP(rec, req)
		return rec.Code
	}

	if code := get(Handlers{Ready: func(context.Context) error { return errors.New("db down") }}); code != http.StatusServiceUnavailable {
		t.Fatalf("failing dependency status = %d, want 503", code)
	}
	if code := get(Handlers{Ready: func(context.Context) error { return nil }}); code != http.StatusOK {
		t.Fatalf("healthy dependency status = %d, want 200", code)
	}
	if code := get(Handlers{}); code != http.StatusOK {
		t.Fatalf("no readiness hook status = %d, want 200", code)
	}
}
