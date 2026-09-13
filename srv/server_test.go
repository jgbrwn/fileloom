package srv

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
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

func TestEditorAPIGetAndJSONSaveContract(t *testing.T) {
	siteDir := filepath.Join(t.TempDir(), "site")
	server, err := New(siteDir, filepath.Join(t.TempDir(), "web"), "owner@example.com")
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	getReq := httptest.NewRequest(http.MethodGet, "/_cms/api/editor?path=pages%2Fabout.html", nil)
	getReq.Header.Set("X-ExeDev-Email", "owner@example.com")
	getRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(getRes, getReq)
	if getRes.Code != http.StatusOK {
		t.Fatalf("editor GET status = %d: %s", getRes.Code, getRes.Body)
	}
	var opened struct {
		HTML         string `json:"html"`
		SourceSHA256 string `json:"source_sha256"`
		PublicURL    string `json:"public_url"`
		Editor       struct {
			Selected string `json:"selected"`
		} `json:"editor"`
	}
	if err := json.Unmarshal(getRes.Body.Bytes(), &opened); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(opened.HTML, "ordinary HTML file") || opened.SourceSHA256 == "" || opened.Editor.Selected != "vvveb" {
		t.Fatalf("unexpected editor document: %#v", opened)
	}
	if opened.PublicURL != "/about/" {
		t.Fatalf("unexpected public URL: %q", opened.PublicURL)
	}
	deckflowReq := httptest.NewRequest(http.MethodGet, "/_cms/api/editor?path=pages%2Fabout.html&engine=deckflow", nil)
	deckflowReq.Header.Set("X-ExeDev-Email", "owner@example.com")
	deckflowRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(deckflowRes, deckflowReq)
	if deckflowRes.Code != http.StatusOK || !strings.Contains(deckflowRes.Body.String(), `"selected":"deckflow"`) {
		t.Fatalf("deckflow editor selection = %d: %s", deckflowRes.Code, deckflowRes.Body)
	}
	unknownReq := httptest.NewRequest(http.MethodGet, "/_cms/api/editor?path=pages%2Fabout.html&engine=unknown", nil)
	unknownReq.Header.Set("X-ExeDev-Email", "owner@example.com")
	unknownRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(unknownRes, unknownReq)
	if unknownRes.Code != http.StatusBadRequest {
		t.Fatalf("unknown editor selection status = %d: %s", unknownRes.Code, unknownRes.Body)
	}

	payload, _ := json.Marshal(map[string]string{
		"path":        "pages/about.html",
		"html":        "<!doctype html><html><body><p>JSON saved body</p></body></html>",
		"base_sha256": opened.SourceSHA256,
		"engine":      "deckflow",
	})
	saveReq := httptest.NewRequest(http.MethodPost, "/_cms/api/editor-save", bytes.NewReader(payload))
	saveReq.Header.Set("Content-Type", "application/json")
	saveReq.Header.Set("X-ExeDev-Email", "owner@example.com")
	saveRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(saveRes, saveReq)
	if saveRes.Code != http.StatusOK {
		t.Fatalf("JSON save status = %d: %s", saveRes.Code, saveRes.Body)
	}
	var saved struct {
		SourceSHA256 string `json:"source_sha256"`
	}
	if err := json.Unmarshal(saveRes.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	if saved.SourceSHA256 == "" || saved.SourceSHA256 == opened.SourceSHA256 {
		t.Fatalf("save did not return a refreshed SHA: %#v", saved)
	}
	if got := strings.Trim(saveRes.Header().Get("ETag"), `"`); got != saved.SourceSHA256 {
		t.Fatalf("save ETag = %q, SHA = %q", got, saved.SourceSHA256)
	}
	source, err := os.ReadFile(filepath.Join(siteDir, "content", "pages", "about.html"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(source), "title: About") || !strings.Contains(string(source), "JSON saved body") {
		t.Fatalf("JSON save did not preserve source metadata/body: %s", source)
	}

	stalePayload, _ := json.Marshal(map[string]string{
		"path":        "pages/about.html",
		"html":        "<p>stale</p>",
		"base_sha256": opened.SourceSHA256,
	})
	staleReq := httptest.NewRequest(http.MethodPost, "/_cms/api/editor-save", bytes.NewReader(stalePayload))
	staleReq.Header.Set("Content-Type", "application/json")
	staleReq.Header.Set("X-ExeDev-Email", "owner@example.com")
	staleRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(staleRes, staleReq)
	if staleRes.Code != http.StatusConflict {
		t.Fatalf("stale JSON save status = %d: %s", staleRes.Code, staleRes.Body)
	}
	var conflict map[string]any
	if err := json.Unmarshal(staleRes.Body.Bytes(), &conflict); err != nil {
		t.Fatal(err)
	}
	if conflict["code"] != "source_conflict" || conflict["current_sha256"] != saved.SourceSHA256 {
		t.Fatalf("unexpected conflict response: %#v", conflict)
	}

	missingSHA, _ := json.Marshal(map[string]string{"path": "pages/about.html", "html": "<p>no precondition</p>"})
	missingReq := httptest.NewRequest(http.MethodPost, "/_cms/api/editor-save", bytes.NewReader(missingSHA))
	missingReq.Header.Set("Content-Type", "application/json")
	missingReq.Header.Set("X-ExeDev-Email", "owner@example.com")
	missingRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(missingRes, missingReq)
	if missingRes.Code != http.StatusPreconditionRequired {
		t.Fatalf("missing precondition status = %d: %s", missingRes.Code, missingRes.Body)
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

func TestStatusMetadataPreservesFrontMatterNewKeys(t *testing.T) {
	server, err := New(filepath.Join(t.TempDir(), "site"), filepath.Join(t.TempDir(), "web"), "")
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	if _, err := server.patchDocumentSource("pages/about.html", "", false, map[string]string{"status": "scheduled", "publish_at": "2026-09-12T12:00:00Z"}, "", "schedule test"); err != nil {
		t.Fatalf("patch status: %v", err)
	}
	doc, err := server.loadDocument("pages/about.html")
	if err != nil {
		t.Fatal(err)
	}
	if doc.Status != "scheduled" || doc.PublishAt != "2026-09-12T12:00:00Z" {
		t.Fatalf("metadata = status %q publish_at %q", doc.Status, doc.PublishAt)
	}
	source, err := os.ReadFile(filepath.Join(server.SiteDir, "content", "pages", "about.html"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(source), "status: scheduled\n") || !strings.Contains(string(source), "\npublish_at: 2026-09-12T12:00:00Z\n") {
		t.Fatalf("front matter keys were not separated: %s", source)
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

func TestVvvebMediaContractAndEditorIntegration(t *testing.T) {
	siteDir := filepath.Join(t.TempDir(), "site")
	server, err := New(siteDir, filepath.Join("..", "web"), "owner@example.com")
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	if err := os.WriteFile(filepath.Join(siteDir, "media", "sample.png"), []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	getReq := httptest.NewRequest(http.MethodGet, "/_cms/api/media", nil)
	getReq.Header.Set("X-ExeDev-Email", "owner@example.com")
	getRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(getRes, getReq)
	if getRes.Code != http.StatusOK {
		t.Fatalf("media tree status = %d: %s", getRes.Code, getRes.Body)
	}
	var tree map[string]any
	if err := json.Unmarshal(getRes.Body.Bytes(), &tree); err != nil {
		t.Fatal(err)
	}
	if tree["type"] != "folder" || tree["name"] != "" {
		t.Fatalf("unexpected Vvveb media root: %#v", tree)
	}
	items, ok := tree["items"].([]any)
	if !ok || len(items) == 0 {
		t.Fatalf("media tree has no items: %#v", tree)
	}
	var hasSampleURL func([]any) bool
	hasSampleURL = func(values []any) bool {
		for _, value := range values {
			item, ok := value.(map[string]any)
			if !ok {
				continue
			}
			if item["url"] == "/media/sample.png" {
				return true
			}
			children, _ := item["items"].([]any)
			if hasSampleURL(children) {
				return true
			}
		}
		return false
	}
	if !hasSampleURL(items) {
		t.Fatalf("media item URL missing: %#v", tree)
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "contract.png")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(part, "png")
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	uploadReq := httptest.NewRequest(http.MethodPost, "/_cms/api/media?format=vvveb", &body)
	uploadReq.Header.Set("Content-Type", writer.FormDataContentType())
	uploadReq.Header.Set("X-ExeDev-Email", "owner@example.com")
	uploadRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(uploadRes, uploadReq)
	if uploadRes.Code != http.StatusCreated || strings.TrimSpace(uploadRes.Body.String()) != "contract.png" {
		t.Fatalf("Vvveb upload response = %d %q", uploadRes.Code, uploadRes.Body.String())
	}
	source, err := os.ReadFile(filepath.Join(server.WebDir, "vvvebjs", "editor.html"))
	if err != nil {
		t.Fatal(err)
	}
	integration := server.prepareVvvebEditor(string(source), []byte(`{"current":{}}`), "test")
	for _, expected := range []string{"/_cms/assets/fileloom-vvveb.js", "window.mediaPath = '/media'", "format=vvveb"} {
		if !strings.Contains(integration, expected) {
			t.Fatalf("editor integration missing %q", expected)
		}
	}
	if strings.Contains(integration, `\tlet renameUrl`) {
		t.Fatal("editor integration emitted a literal tab escape in JavaScript")
	}
}

func TestDeckflowEditorRouteAndVvvebFallback(t *testing.T) {
	siteDir := filepath.Join(t.TempDir(), "site")
	server, err := New(siteDir, filepath.Join("..", "web"), "owner@example.com")
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	config, err := server.loadSiteConfig()
	if err != nil {
		t.Fatal(err)
	}
	config.EditorEngine = "deckflow"
	if err := server.saveSiteConfig(config); err != nil {
		t.Fatal(err)
	}
	request := func(query string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/_cms/editor?path=pages%2Fabout.html"+query, nil)
		req.Header.Set("X-ExeDev-Email", "owner@example.com")
		res := httptest.NewRecorder()
		server.Handler().ServeHTTP(res, req)
		return res
	}
	deckflow := request("")
	if deckflow.Code != http.StatusOK || !strings.Contains(deckflow.Body.String(), "/_cms/assets/editor-dist/assets/") {
		t.Fatalf("Deckflow editor route = %d: %s", deckflow.Code, deckflow.Body)
	}
	vvveb := request("&engine=vvveb")
	if vvveb.Code != http.StatusOK || !strings.Contains(vvveb.Body.String(), "fileloom-vvveb.js") {
		t.Fatalf("Vvveb fallback route = %d: %s", vvveb.Code, vvveb.Body)
	}
}

func TestGeneratedCodeAssets(t *testing.T) {
	siteDir := filepath.Join(t.TempDir(), "site")
	server, err := New(siteDir, filepath.Join("..", "web"), "")
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	index, err := os.ReadFile(filepath.Join(siteDir, "public", "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(index), "/theme/fileloom-code.css") || !strings.Contains(string(index), "/theme/fileloom-code.js") {
		t.Fatalf("generated page missing code assets")
	}
	for _, name := range []string{"fileloom-code.css", "fileloom-code.js"} {
		if _, err := os.Stat(filepath.Join(siteDir, "public", "theme", name)); err != nil {
			t.Fatalf("generated code asset %s missing: %v", name, err)
		}
	}
	_ = server
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
	exportReq := httptest.NewRequest(http.MethodPost, "/_cms/api/export", nil)
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

func TestCMSRejectsMultipleIdentityHeaderValues(t *testing.T) {
	server, err := New(filepath.Join(t.TempDir(), "site"), filepath.Join(t.TempDir(), "web"), "owner@example.com")
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/_cms/api/site", nil)
	req.Header.Add("X-ExeDev-Email", "owner@example.com")
	req.Header.Add("X-ExeDev-Email", "attacker@example.com")
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusNotFound {
		t.Fatalf("multiple identity headers status = %d, want 404", res.Code)
	}
}

func TestCMSOriginChecksAndCacheHeaders(t *testing.T) {
	server, err := New(filepath.Join(t.TempDir(), "site"), filepath.Join(t.TempDir(), "web"), "owner@example.com")
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	request := func(origin string) *httptest.ResponseRecorder {
		form := "file=pages%2Fabout.html&html=%3Cbody%3E%3Cp%3EOrigin%3C%2Fp%3E%3C%2Fbody%3E"
		req := httptest.NewRequest(http.MethodPost, "http://example.test/_cms/api/editor-save", strings.NewReader(form))
		req.Host = "example.test"
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("X-ExeDev-Email", "owner@example.com")
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		res := httptest.NewRecorder()
		server.Handler().ServeHTTP(res, req)
		return res
	}
	if res := request("https://evil.example"); res.Code != http.StatusNotFound {
		t.Fatalf("cross-origin mutation status = %d, want 404", res.Code)
	}
	if res := request("http://example.test"); res.Code != http.StatusOK {
		t.Fatalf("same-origin mutation status = %d, want 200", res.Code)
	}
	get := httptest.NewRequest(http.MethodGet, "/_cms/api/site", nil)
	get.Header.Set("X-ExeDev-Email", "owner@example.com")
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, get)
	if res.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("CMS cache control = %q, want no-store", res.Header().Get("Cache-Control"))
	}
}

func TestSanitizeSVGUpload(t *testing.T) {
	server, err := New(filepath.Join(t.TempDir(), "site"), filepath.Join(t.TempDir(), "web"), "owner@example.com")
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "unsafe.svg")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(part, `<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)" viewBox="0 0 10 10"><script>alert(1)</script><foreignObject><body>bad</body></foreignObject><path d="M0 0h10v10z" fill="url(#paint)" href="https://evil.example/x"/><defs><linearGradient id="paint"><stop offset="0" stop-color="#fff"/></linearGradient></defs></svg>`)
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/_cms/api/media", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("X-ExeDev-Email", "owner@example.com")
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("SVG upload status = %d: %s", res.Code, res.Body)
	}
	data, err := os.ReadFile(filepath.Join(server.SiteDir, "media", "unsafe.svg"))
	if err != nil {
		t.Fatal(err)
	}
	output := string(data)
	for _, forbidden := range []string{"script", "foreignObject", "onload", "javascript:", "https://evil.example"} {
		if strings.Contains(strings.ToLower(output), strings.ToLower(forbidden)) {
			t.Fatalf("sanitized SVG still contains %q: %s", forbidden, output)
		}
	}
	if !strings.Contains(output, "<path") || !strings.Contains(output, "viewBox") {
		t.Fatalf("sanitized SVG lost safe content: %s", output)
	}
}

func TestSVGSanitizerRejectsDirectives(t *testing.T) {
	if _, err := sanitizeSVG([]byte(`<!DOCTYPE svg><svg></svg>`)); err == nil {
		t.Fatal("DOCTYPE SVG was accepted")
	}
	if _, err := sanitizeSVG([]byte(`<svg><path d="M0 0"/></svg>`)); err != nil {
		t.Fatalf("safe SVG rejected: %v", err)
	}
	bom := append([]byte{0xef, 0xbb, 0xbf}, []byte(`<svg><use href="#icon"/></svg>`)...)
	sanitized, err := sanitizeSVG(bom)
	if err != nil || !strings.Contains(string(sanitized), "<use") {
		t.Fatalf("BOM/use SVG was not preserved: %v %s", err, sanitized)
	}
	unicodeSVG := `<svg><path fill="éurl(https://evil.example/x)"/></svg>`
	if _, err := sanitizeSVG([]byte(unicodeSVG)); err != nil {
		t.Fatalf("unicode SVG unexpectedly rejected: %v", err)
	}
}

func TestSymlinkedContentFailsClosed(t *testing.T) {
	root := t.TempDir()
	siteDir := filepath.Join(root, "site")
	server, err := New(siteDir, filepath.Join(root, "web"), "")
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	outside := filepath.Join(root, "secret.html")
	if err := os.WriteFile(outside, []byte("<p>secret</p>"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(siteDir, "content", "pages", "leak.html")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := server.Build(); err == nil {
		t.Fatal("build accepted a symlinked content file")
	}
}

func TestRevisionRetention(t *testing.T) {
	server, err := New(filepath.Join(t.TempDir(), "site"), filepath.Join(t.TempDir(), "web"), "")
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	for i := 0; i < maxRevisionsPerPath+5; i++ {
		if err := server.recordWorkspaceRevision("pages/about.html", []byte(fmt.Sprintf("revision-%d", i)), "retention test"); err != nil {
			t.Fatalf("record revision %d: %v", i, err)
		}
	}
	revisions, err := server.listRevisions("pages/about.html")
	if err != nil {
		t.Fatal(err)
	}
	if len(revisions) > maxRevisionsPerPath {
		t.Fatalf("revision count = %d, want <= %d", len(revisions), maxRevisionsPerPath)
	}
}

func TestScheduledPublishingRestoresSourceWhenBuildFails(t *testing.T) {
	server, err := New(filepath.Join(t.TempDir(), "site"), filepath.Join(t.TempDir(), "web"), "")
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	doc := Document{Path: "pages/retry.html", Type: "page", Title: "Retry", Slug: "retry", Date: "2026-09-11", Status: "scheduled", PublishAt: time.Now().Add(-time.Minute).UTC().Format(time.RFC3339), HTML: "<p>Retry</p>"}
	if err := server.writeDocument(doc); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(server.SiteDir, "themes", "default", "assets", "unsafe-link.css")
	outside := filepath.Join(t.TempDir(), "outside.css")
	if err := os.WriteFile(outside, []byte("body{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, target); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	server.publishDue()
	updated, err := server.loadDocument(doc.Path)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != "scheduled" {
		t.Fatalf("scheduled document status = %q after failed build, want scheduled", updated.Status)
	}
}

func TestGitRemoteHardening(t *testing.T) {
	if err := validateRemoteURL("git@github.com:owner/repo.git"); err != nil {
		t.Fatalf("SCP remote rejected: %v", err)
	}
	for _, value := range []string{"https://user:secret@example.com/repo.git", "https://example.com/repo.git?token=secret", "file:///tmp/repo"} {
		if err := validateRemoteURLForAutomation(value, true); err == nil {
			t.Fatalf("unsafe unattended remote accepted: %s", value)
		}
	}
	if err := validateRemoteURL("file:///tmp/repo"); err != nil {
		t.Fatalf("manual file remote rejected: %v", err)
	}
}
func TestOptionalCSPProfiles(t *testing.T) {
	t.Setenv("FILELOOM_CMS_CSP", "default")
	t.Setenv("FILELOOM_PUBLIC_CSP", "default")
	server, err := New(filepath.Join(t.TempDir(), "site"), filepath.Join(t.TempDir(), "web"), "owner@example.com")
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	cms := httptest.NewRequest(http.MethodGet, "/_cms/api/site", nil)
	cms.Header.Set("X-ExeDev-Email", "owner@example.com")
	cmsRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(cmsRes, cms)
	if cmsRes.Header().Get("Content-Security-Policy") != defaultCMSCSP {
		t.Fatalf("CMS CSP was not applied")
	}
	public := httptest.NewRequest(http.MethodGet, "/", nil)
	publicRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(publicRes, public)
	if publicRes.Header().Get("Content-Security-Policy") != defaultPublicCSP {
		t.Fatalf("public CSP was not applied")
	}
}

func TestGitPushURLIsValidated(t *testing.T) {
	root := t.TempDir()
	server, err := New(filepath.Join(root, "site"), filepath.Join(root, "web"), "")
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	if _, err := runGit(server.SiteDir, "init", "-q"); err != nil {
		t.Fatalf("init site repo: %v", err)
	}
	if _, err := runGit(server.SiteDir, "remote", "add", "origin", "https://github.com/example/site.git"); err != nil {
		t.Fatalf("add remote: %v", err)
	}
	if _, err := runGit(server.SiteDir, "config", "remote.origin.pushurl", "file:///tmp/local-site.git"); err != nil {
		t.Fatalf("set push URL: %v", err)
	}
	if err := server.gitPush(GitConfig{Remote: "origin", Branch: "main", AutoPush: true}); err == nil {
		t.Fatal("unsafe effective push URL was accepted")
	}
	if err := validateRemoteURL("ssh://git@github.com/example/site.git"); err != nil {
		t.Fatalf("SSH remote with username rejected: %v", err)
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
