package srv

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
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
	server, err := New(siteDir, filepath.Join(t.TempDir(), "web"), "owner@example.com")
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	form := "file=pages%2Fabout.html&html=%3C%21doctype+html%3E%3Chtml%3E%3Cbody%3E%3Cp%3EChanged%3C%2Fp%3E%3C%2Fbody%3E%3C%2Fhtml%3E"
	req := httptest.NewRequest(http.MethodPost, "/_cms/api/editor-save", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-ExeDev-Email", "owner@example.com")
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

func TestCMSRequiresConfiguredExeDevEmail(t *testing.T) {
	siteDir := filepath.Join(t.TempDir(), "site")
	server, err := New(siteDir, filepath.Join(t.TempDir(), "web"), "owner@example.com")
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	for _, test := range []struct {
		name  string
		email string
		want  int
	}{
		{name: "missing header", want: http.StatusNotFound},
		{name: "wrong account", email: "other@example.com", want: http.StatusNotFound},
		{name: "accepted account", email: "owner@example.com", want: http.StatusOK},
		{name: "accepted account case insensitive", email: "OWNER@EXAMPLE.COM", want: http.StatusOK},
	} {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/_cms/api/site", nil)
			if test.email != "" {
				req.Header.Set("X-ExeDev-Email", test.email)
			}
			res := httptest.NewRecorder()
			server.Handler().ServeHTTP(res, req)
			if res.Code != test.want {
				t.Fatalf("CMS status = %d, want %d; body=%s", res.Code, test.want, res.Body)
			}
		})
	}

	redirectReq := httptest.NewRequest(http.MethodGet, "/_cms", nil)
	redirectRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(redirectRes, redirectReq)
	if redirectRes.Code != http.StatusNotFound {
		t.Fatalf("unauthorized /_cms status = %d, want 404", redirectRes.Code)
	}

	unconfigured, err := New(filepath.Join(t.TempDir(), "site"), filepath.Join(t.TempDir(), "web"), "")
	if err != nil {
		t.Fatalf("new unconfigured server: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/_cms/api/site", nil)
	req.Header.Set("X-ExeDev-Email", "owner@example.com")
	res := httptest.NewRecorder()
	unconfigured.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusNotFound {
		t.Fatalf("unconfigured CMS status = %d, want 404", res.Code)
	}
}
func TestBaseURLOverrideAndGeneratedArchives(t *testing.T) {
	siteDir := filepath.Join(t.TempDir(), "site")
	server, err := NewWithOptions(siteDir, filepath.Join(t.TempDir(), "web"), "", "example.test")
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	config, err := server.loadSiteConfig()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if config.BaseURL != "https://example.test" {
		t.Fatalf("base URL = %q, want https://example.test", config.BaseURL)
	}
	rss, err := os.ReadFile(filepath.Join(siteDir, "public", "rss.xml"))
	if err != nil {
		t.Fatalf("read RSS: %v", err)
	}
	if !strings.Contains(string(rss), "https://example.test/2026/welcome-to-fileloom/") {
		t.Fatalf("RSS did not use override: %s", rss)
	}
	for _, generated := range []string{"category/notes/index.html", "archive/2026/index.html"} {
		if _, err := os.Stat(filepath.Join(siteDir, "public", filepath.FromSlash(generated))); err != nil {
			t.Fatalf("expected generated archive %s: %v", generated, err)
		}
	}
}

func TestThemeActivation(t *testing.T) {
	siteDir := filepath.Join(t.TempDir(), "site")
	server, err := New(siteDir, filepath.Join(t.TempDir(), "web"), "")
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(siteDir, "themes", "sunset", "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"layout.html", "index.html", "page.html", "post.html", "tag.html", "category.html", "archive.html"} {
		if err := os.WriteFile(filepath.Join(siteDir, "themes", "sunset", name), []byte(defaultLayoutTemplate), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(siteDir, "themes", "sunset", "assets", "style.css"), []byte("body{background:pink}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := server.activateTheme("sunset"); err != nil {
		t.Fatalf("activate theme: %v", err)
	}
	config, err := server.loadSiteConfig()
	if err != nil || config.Theme != "sunset" {
		t.Fatalf("theme = %q, err=%v", config.Theme, err)
	}
}
func TestGitIntegrationDoesNotWalkParentRepo(t *testing.T) {
	root := t.TempDir()
	if output, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("init parent repo: %v: %s", err, output)
	}
	server, err := New(filepath.Join(root, "site"), filepath.Join(t.TempDir(), "web"), "")
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	status := server.gitStatus(GitConfig{Remote: "origin"})
	if status.Available {
		t.Fatal("git integration incorrectly discovered the parent project repository")
	}
	if !strings.Contains(status.Error, "site/.git") {
		t.Fatalf("unexpected Git status error: %q", status.Error)
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
