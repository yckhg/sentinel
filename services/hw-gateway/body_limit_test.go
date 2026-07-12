package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestMaxBytesMiddleware locks the request-body cap (#41): a body over the limit
// must fail the read (so handlers reject it), while bodies at/under the limit
// pass through untouched.
func TestMaxBytesMiddleware(t *testing.T) {
	handler := maxBytesMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.ReadAll(r.Body); err != nil {
			http.Error(w, "too large", http.StatusRequestEntityTooLarge)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))

	cases := []struct {
		name    string
		size    int
		wantErr bool
	}{
		{"under limit", maxRequestBodyBytes - 1, false},
		{"at limit", maxRequestBodyBytes, false},
		{"over limit", maxRequestBodyBytes + 1, true},
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(make([]byte, c.size)))
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		gotErr := rr.Code == http.StatusRequestEntityTooLarge
		if gotErr != c.wantErr {
			t.Errorf("%s: size=%d got413=%v want=%v (code=%d)", c.name, c.size, gotErr, c.wantErr, rr.Code)
		}
	}
}
