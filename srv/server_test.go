package srv

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
func TestSourceAwareSaveRevisionAndRestore(t *testing.T) {
	siteDir := filepath.Join(t.TempDir(), "site")
	server, err := New(siteDir, filepath.Join(t.TempDir(), "web"), "owner@example.com")
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	source := "---\n# preserve this comment\ntitle: About\nslug: about\ndate: 2026-09-11\nstatus: published\ncustom: keep-me\n---\n\n<section>Old body</section>\n"
	filePath := filepath.Join(siteDir, "content", "pages", "about.html")
	if err := os.WriteFile(filePath, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	baseSHA := sourceSHA256([]byte(source))
	form := url.Values{"file": {"pages/about.html"}, "base_sha256": {baseSHA}, "html": {"<!doctype html><html><body><section>New body</section></body></html>"}}
	req := httptest.NewRequest(http.MethodPost, "/_cms/api/editor-save", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-ExeDev-Email", "owner@example.com")
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("save status = %d: %s", res.Code, res.Body)
	}
	updated, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(updated), "# preserve this comment") || !strings.Contains(string(updated), "custom: keep-me") || !strings.Contains(string(updated), "<section>New body</section>") {
		t.Fatalf("source-aware save did not preserve source: %s", updated)
	}
	revisions, err := server.listRevisions("pages/about.html")
	if err != nil || len(revisions) != 1 {
		t.Fatalf("revisions = %d, err=%v", len(revisions), err)
	}
	if _, err := server.restoreRevision("pages/about.html", revisions[0].ID); err != nil {
		t.Fatalf("restore: %v", err)
	}
	restored, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(restored) != source {
		t.Fatalf("restore did not recover original source:\n%s", restored)
	}
}

func TestScheduledPublishingAndPublicOwnerToolbar(t *testing.T) {
	siteDir := filepath.Join(t.TempDir(), "site")
	server, err := New(siteDir, filepath.Join(t.TempDir(), "web"), "owner@example.com")
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	doc := Document{Path: "pages/scheduled.html", Type: "page", Title: "Scheduled", Slug: "scheduled", Date: "2026-09-11", Status: "scheduled", PublishAt: time.Now().Add(-time.Minute).UTC().Format(time.RFC3339), HTML: "<p>Scheduled body</p>"}
	if err := server.writeDocument(doc); err != nil {
		t.Fatal(err)
	}
	if _, err := server.Build(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(siteDir, "public", "scheduled", "index.html")); err != nil {
		t.Fatalf("due scheduled page not built: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/about/", nil)
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, req)
	if strings.Contains(res.Body.String(), "Edit this page") {
		t.Fatalf("owner toolbar leaked to unauthenticated visitor")
	}
	ownerReq := httptest.NewRequest(http.MethodGet, "/about/", nil)
	ownerReq.Header.Set("X-ExeDev-Email", "owner@example.com")
	ownerRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(ownerRes, ownerReq)
	if !strings.Contains(ownerRes.Body.String(), "Edit this page") {
		t.Fatalf("owner toolbar missing")
	}
	if strings.Contains(res.Body.String(), "Edit this page") {
		t.Fatalf("owner toolbar leaked to unauthenticated visitor")
	}
}

func TestThemeTokensAndExport(t *testing.T) {
	siteDir := filepath.Join(t.TempDir(), "site")
	server, err := New(siteDir, filepath.Join(t.TempDir(), "web"), "owner@example.com")
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	getReq := httptest.NewRequest(http.MethodGet, "/_cms/api/theme-tokens?theme=default", nil)
	getReq.Header.Set("X-ExeDev-Email", "owner@example.com")
	getRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(getRes, getReq)
	if getRes.Code != http.StatusOK || !strings.Contains(getRes.Body.String(), "--accent") {
		t.Fatalf("theme tokens response = %d: %s", getRes.Code, getRes.Body)
	}
	var tokenData struct {
		SHA256 string `json:"sha256"`
	}
	if err := json.Unmarshal(getRes.Body.Bytes(), &tokenData); err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{"theme": "default", "sha256": tokenData.SHA256, "tokens": map[string]string{"--accent": "#123456"}}
	body, _ := json.Marshal(payload)
	postReq := httptest.NewRequest(http.MethodPost, "/_cms/api/theme-tokens?theme=default", bytes.NewReader(body))
	postReq.Header.Set("Content-Type", "application/json")
	postReq.Header.Set("X-ExeDev-Email", "owner@example.com")
	postRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(postRes, postReq)
	if postRes.Code != http.StatusOK {
		t.Fatalf("theme token save = %d: %s", postRes.Code, postRes.Body)
	}
	css, _ := os.ReadFile(filepath.Join(siteDir, "themes", "default", "assets", "style.css"))
	if !strings.Contains(string(css), "--accent: #123456") {
		t.Fatalf("token was not patched: %s", css)
	}
	exportReq := httptest.NewRequest(http.MethodGet, "/_cms/api/export", nil)
	exportReq.Header.Set("X-ExeDev-Email", "owner@example.com")
	exportRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(exportRes, exportReq)
	if exportRes.Code != http.StatusOK || exportRes.Header().Get("Content-Type") != "application/zip" {
		t.Fatalf("export = %d %s", exportRes.Code, exportRes.Body)
	}
	archive, err := zip.NewReader(bytes.NewReader(exportRes.Body.Bytes()), int64(exportRes.Body.Len()))
	if err != nil {
		t.Fatalf("read export zip: %v", err)
	}
	foundIndex := false
	for _, file := range archive.File {
		if file.Name == "index.html" {
			foundIndex = true
		}
		if strings.Contains(file.Name, ".fileloom") || strings.HasPrefix(file.Name, ".git") {
			t.Fatalf("private file included in export: %s", file.Name)
		}
	}
	if !foundIndex {
		t.Fatal("export did not contain index.html")
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
