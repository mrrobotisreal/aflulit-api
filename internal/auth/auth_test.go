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

func TestSplitRoutesTokensByPath(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	const asrToken = "asr-token-fedcba9876543210"
	h := Split(token, asrToken, "/v1/transcriptions", inner)
	try := func(path, bearer string) int {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	if try("/v1/transcriptions", asrToken) != 200 || try("/v1/transcriptions", token) != 401 || try("/v1/transcriptions", "") != 401 {
		t.Fatal("ASR path must accept only the ASR token")
	}
	if try("/backups", token) != 200 || try("/backups", asrToken) != 401 || try("/other", asrToken) != 401 {
		t.Fatal("backup and unknown paths must accept only the main token")
	}
	shared := Split(token, "", "/v1/transcriptions", inner)
	req := httptest.NewRequest(http.MethodPost, "/v1/transcriptions", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	shared.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatal("with no ASR token, the main token must open the ASR path")
	}
}
