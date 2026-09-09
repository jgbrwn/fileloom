package srv

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileloomBuildsFilesystemSite(t *testing.T) {
	siteDir := filepath.Join(t.TempDir(), "site")
	server, err := New(siteDir, filepath.Join(t.TempDir(), "web"), "")
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	index, err := os.ReadFile(filepath.Join(siteDir, "public", "index.html"))
	if err != nil {
		t.Fatalf("read generated index: %v", err)
	}
	if !strings.Contains(string(index), "Welcome to Fileloom") {
		t.Fatalf("generated index did not contain seeded post: %s", index)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("GET / status = %d, want 200", res.Code)
	}
	if !strings.Contains(res.Body.String(), "A Fileloom site") {
		t.Fatalf("public response did not contain site title: %s", res.Body)
	}
}

func TestEditorSaveExtractsBodyAndRebuilds(t *testing.T) {
	siteDir := filepath.Join(t.TempDir(), "site")
	server, err := New(siteDir, filepath.Join(t.TempDir(), "web"), "")
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	form := "file=pages%2Fabout.html&html=%3C%21doctype+html%3E%3Chtml%3E%3Cbody%3E%3Cp%3EChanged%3C%2Fp%3E%3C%2Fbody%3E%3C%2Fhtml%3E"
	req := httptest.NewRequest(http.MethodPost, "/_cms/api/editor-save", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("save status = %d: %s", res.Code, res.Body)
	}

	generated, err := os.ReadFile(filepath.Join(siteDir, "public", "about", "index.html"))
	if err != nil {
		t.Fatalf("read generated page: %v", err)
	}
	if !strings.Contains(string(generated), "<p>Changed</p>") {
		t.Fatalf("generated page did not include saved body: %s", generated)
	}
}

func TestPathValidation(t *testing.T) {
	for _, value := range []string{"../secret.html", "/tmp/secret.html", "media/file.html", "pages/about.txt"} {
		if _, err := normalizeContentPath(value); err == nil {
			t.Errorf("normalizeContentPath(%q) unexpectedly succeeded", value)
		}
	}
	if got, err := normalizeContentPath("content/posts/2026/hello.html"); err != nil || got != "posts/2026/hello.html" {
		t.Fatalf("normalize content path = %q, %v", got, err)
	}
}
