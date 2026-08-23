package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

const token = "test-token-0123456789abcdef"

func run(t *testing.T, header string) *httptest.ResponseRecorder {
	t.Helper()
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	req := httptest.NewRequest(http.MethodGet, "/backups", nil)
	if header != "" {
		req.Header.Set("Authorization", header)
	}
	rec := httptest.NewRecorder()
	Middleware(token, inner).ServeHTTP(rec, req)
	return rec
}

func TestValidToken(t *testing.T) {
	if rec := run(t, "Bearer "+token); rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	// Scheme is case-insensitive per RFC 7235.
	if rec := run(t, "bearer "+token); rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for lowercase scheme, got %d", rec.Code)
	}
}

func TestRejections(t *testing.T) {
	cases := map[string]string{
		"missing header": "",
		"empty bearer":   "Bearer ",
		"wrong token":    "Bearer wrong-token-9876543210fedcba",
		"prefix only":    "Bearer test-token-0123456789abcde",
		"token + extra":  "Bearer " + token + "x",
		"no scheme":      token,
		"basic scheme":   "Basic " + token,
	}
	for label, header := range cases {
		rec := run(t, header)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: expected 401, got %d", label, rec.Code)
		}
		if body := rec.Body.String(); body != `{"error":"unauthorized"}` {
			t.Errorf("%s: unexpected body %q (must never leak data)", label, body)
		}
	}
}
