package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Regression test for the 2026-11-17 panel lockout: /parham serves two
// documents at one URL (login page / panel shell, picked by the session
// cookie). A browser that cached the login page revalidates with
// If-Modified-Since; the handler must still answer 200 with the document
// matching the request's auth state — never a 304 that pins the browser to
// the cached login form after a successful login.
func TestServePanelPageIgnoresConditionalHeaders(t *testing.T) {
	login := []byte("<html>login</html>")
	index := []byte("<html>panel</html>")
	future := time.Now().Add(24 * time.Hour).UTC().Format(http.TimeFormat)

	// Unauthenticated, with a conditional header that would make
	// http.ServeFile answer 304: must get the full login page.
	req := httptest.NewRequest(http.MethodGet, "/parham", nil)
	req.Header.Set("If-Modified-Since", future)
	rec := httptest.NewRecorder()
	servePanelPage(rec, req, login, index, t.TempDir())
	if rec.Code != http.StatusOK || rec.Body.String() != string(login) {
		t.Fatalf("unauth: code=%d body=%q, want 200 + login page", rec.Code, rec.Body.String())
	}
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Fatalf("unauth: Cache-Control=%q, want no-store", cc)
	}

	// Authenticated (valid session cookie), same conditional header: must
	// get the full panel shell, not the cached login page.
	sessionMu.Lock()
	sessions["panel-page-test-token"] = time.Now().Add(time.Hour)
	sessionMu.Unlock()
	defer func() {
		sessionMu.Lock()
		delete(sessions, "panel-page-test-token")
		sessionMu.Unlock()
	}()
	req2 := httptest.NewRequest(http.MethodGet, "/parham", nil)
	req2.AddCookie(&http.Cookie{Name: "parham_session", Value: "panel-page-test-token"})
	req2.Header.Set("If-Modified-Since", future)
	rec2 := httptest.NewRecorder()
	servePanelPage(rec2, req2, login, index, t.TempDir())
	if rec2.Code != http.StatusOK || rec2.Body.String() != string(index) {
		t.Fatalf("auth: code=%d body=%q, want 200 + panel shell", rec2.Code, rec2.Body.String())
	}
}
