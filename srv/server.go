package srv

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
)

const (
	appName       = "Fileloom"
	dateFormat    = "2006-01-02"
	maxEditorSize = 12 << 20
)

type Server struct {
	SiteDir    string
	WebDir     string
	OwnerEmail string

	mu        sync.RWMutex
	lastBuild BuildResult
}

type SiteConfig struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	BaseURL     string `json:"base_url"`
	Theme       string `json:"theme"`
	Footer      string `json:"footer"`
}

type Document struct {
	Path    string   `json:"path"`
	Type    string   `json:"type"`
	Title   string   `json:"title"`
	Slug    string   `json:"slug"`
	Date    string   `json:"date"`
	Status  string   `json:"status"`
	Tags    []string `json:"tags"`
	Excerpt string   `json:"excerpt"`
	HTML    string   `json:"html,omitempty"`
	URL     string   `json:"url"`
}

type BuildResult struct {
	GeneratedAt string `json:"generated_at"`
	Files       int    `json:"files"`
	Published   int    `json:"published"`
}

type documentMeta struct {
	doc Document
	mod time.Time
}

var tokenPattern = regexp.MustCompile(`\{\{\s*([a-zA-Z0-9_.-]+)\s*\}\}`)

const defaultSiteJSON = `{
  "title": "A Fileloom site",
  "description": "An HTML-first static site made with Fileloom.",
  "base_url": "http://localhost:8000",
  "theme": "default",
  "footer": "Made with Fileloom."
}
`

const defaultLayoutTemplate = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{title}} · {{site.title}}</title>
  <meta name="description" content="{{excerpt}}">
  <link rel="stylesheet" href="/theme/style.css">
</head>
<body>
  <header class="site-header">
    <div class="shell header-inner">
      <a class="brand" href="/">{{site.title}}</a>
      <nav>{{navigation}}</nav>
    </div>
  </header>
  <main class="shell">{{content}}</main>
  <footer class="site-footer"><div class="shell">{{site.footer}}</div></footer>
</body>
</html>
`

const defaultIndexTemplate = `<section class="hero">
  <p class="eyebrow">HTML-first · filesystem-backed · static</p>
  <h1>{{site.title}}</h1>
  <p class="lede">{{site.description}}</p>
</section>
<section class="section-heading"><h2>Latest writing</h2><a href="/rss.xml">RSS</a></section>
<div class="post-grid">{{posts}}</div>
`

const defaultPostTemplate = `<article class="article">
  <p class="eyebrow">{{date}} · {{tags}}</p>
  <h1>{{title}}</h1>
  {{if-excerpt}}
  <div class="article-body">{{content}}</div>
</article>
`

const defaultPageTemplate = `<article class="article page">
  <p class="eyebrow">Page</p>
  <h1>{{title}}</h1>
  <div class="article-body">{{content}}</div>
</article>
`

const defaultTagTemplate = `<section class="hero compact">
  <p class="eyebrow">Tag archive</p>
  <h1>{{tag}}</h1>
</section>
<div class="post-grid">{{posts}}</div>
`

const defaultStyleCSS = `:root {
  color-scheme: light;
  --ink: #18212b;
  --muted: #667384;
  --line: #dfe5ec;
  --paper: #fbfcfe;
  --accent: #7557ff;
  --accent-dark: #5038c8;
  --warm: #fff4db;
}
* { box-sizing: border-box; }
html { background: var(--paper); }
body { margin: 0; color: var(--ink); font: 16px/1.7 Inter, ui-sans-serif, system-ui, -apple-system, sans-serif; }
a { color: var(--accent-dark); }
.shell { width: min(1080px, calc(100% - 40px)); margin: 0 auto; }
.site-header { border-bottom: 1px solid var(--line); background: rgba(255,255,255,.86); backdrop-filter: blur(12px); position: sticky; top: 0; z-index: 2; }
.header-inner { min-height: 72px; display: flex; align-items: center; justify-content: space-between; gap: 24px; }
.brand { color: var(--ink); font-weight: 800; text-decoration: none; letter-spacing: -.03em; }
nav { display: flex; gap: 20px; }
nav a { color: var(--muted); text-decoration: none; font-size: .95rem; }
nav a:hover { color: var(--ink); }
.hero { padding: 96px 0 68px; max-width: 780px; }
.hero.compact { padding-bottom: 32px; }
.eyebrow { color: var(--accent-dark); font-size: .76rem; font-weight: 800; letter-spacing: .12em; text-transform: uppercase; }
h1, h2, h3 { line-height: 1.12; letter-spacing: -.04em; }
h1 { font-size: clamp(2.7rem, 8vw, 5.8rem); margin: 10px 0 22px; }
h2 { font-size: 1.55rem; margin: 0; }
.lede { color: var(--muted); font-size: clamp(1.1rem, 2vw, 1.35rem); max-width: 640px; }
.section-heading { display: flex; justify-content: space-between; align-items: baseline; border-bottom: 1px solid var(--line); padding-bottom: 13px; margin-bottom: 24px; }
.post-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(260px, 1fr)); gap: 18px; padding-bottom: 70px; }
.post-card { background: white; border: 1px solid var(--line); border-radius: 18px; padding: 24px; box-shadow: 0 12px 34px rgba(35, 45, 65, .05); }
.post-card h2 { font-size: 1.35rem; margin: 7px 0 10px; }
.post-card h2 a { color: var(--ink); text-decoration: none; }
.post-card p { color: var(--muted); margin: 0; }
.article { max-width: 790px; margin: 0 auto; padding: 88px 0 100px; }
.article h1 { font-size: clamp(2.6rem, 7vw, 5rem); }
.article-body { font-size: 1.12rem; }
.article-body img { max-width: 100%; height: auto; border-radius: 14px; }
.article-body blockquote { border-left: 4px solid var(--accent); margin-left: 0; padding-left: 20px; color: var(--muted); }
.site-footer { border-top: 1px solid var(--line); color: var(--muted); padding: 24px 0 48px; font-size: .9rem; }
@media (max-width: 640px) { .shell { width: min(100% - 28px, 1080px); } .header-inner { min-height: 60px; } nav { gap: 12px; } .hero { padding-top: 58px; } }
`

func New(siteDir, webDir, ownerEmail string) (*Server, error) {
	if siteDir == "" {
		siteDir = "site"
	}
	if webDir == "" {
		webDir = "web"
	}
	absSite, err := filepath.Abs(siteDir)
	if err != nil {
		return nil, fmt.Errorf("resolve site directory: %w", err)
	}
	absWeb, err := filepath.Abs(webDir)
	if err != nil {
		return nil, fmt.Errorf("resolve web directory: %w", err)
	}
	s := &Server{SiteDir: absSite, WebDir: absWeb, OwnerEmail: strings.TrimSpace(ownerEmail)}
	if err := s.ensureSite(); err != nil {
		return nil, err
	}
	if _, err := s.Build(); err != nil {
		return nil, fmt.Errorf("initial build: %w", err)
	}
	return s, nil
}

func (s *Server) ensureSite() error {
	for _, dir := range []string{
		filepath.Join(s.SiteDir, "content", "pages"),
		filepath.Join(s.SiteDir, "content", "posts"),
		filepath.Join(s.SiteDir, "media", "images"),
		filepath.Join(s.SiteDir, "themes", "default", "assets"),
		filepath.Join(s.SiteDir, "public"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}

	if err := writeIfMissing(filepath.Join(s.SiteDir, "site.json"), []byte(defaultSiteJSON)); err != nil {
		return err
	}
	themeDir := filepath.Join(s.SiteDir, "themes", "default")
	for name, contents := range map[string]string{
		"layout.html": defaultLayoutTemplate,
		"index.html":  defaultIndexTemplate,
		"post.html":   defaultPostTemplate,
		"page.html":   defaultPageTemplate,
		"tag.html":    defaultTagTemplate,
	} {
		if err := writeIfMissing(filepath.Join(themeDir, name), []byte(contents)); err != nil {
			return err
		}
	}
	if err := writeIfMissing(filepath.Join(themeDir, "assets", "style.css"), []byte(defaultStyleCSS)); err != nil {
		return err
	}

	pages, err := s.listDocuments()
	if err != nil {
		return err
	}
	if len(pages) == 0 {
		if err := s.writeDocument(Document{
			Path:   "pages/about.html",
			Type:   "page",
			Title:  "About",
			Slug:   "about",
			Date:   time.Now().Format(dateFormat),
			Status: "published",
			HTML:   "<p>This site is an editable collection of ordinary HTML files. Open the CMS to change it.</p>",
		}); err != nil {
			return err
		}
		if err := s.writeDocument(Document{
			Path:    filepath.ToSlash(filepath.Join("posts", time.Now().Format("2006"), "welcome-to-fileloom.html")),
			Type:    "post",
			Title:   "Welcome to Fileloom",
			Slug:    "welcome-to-fileloom",
			Date:    time.Now().Format(dateFormat),
			Status:  "published",
			Tags:    []string{"fileloom", "static-sites"},
			Excerpt: "A tiny visual CMS where HTML files are the source of truth.",
			HTML:    "<p>Fileloom keeps the source close to the metal: content is HTML, the filesystem is the database, and publishing produces ordinary static files.</p><p>Try editing this post, then build the site.</p>",
		}); err != nil {
			return err
		}
	}
	return nil
}

func writeIfMissing(path string, contents []byte) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat %s: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create parent for %s: %w", path, err)
	}
	if err := os.WriteFile(path, contents, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func (s *Server) loadSiteConfig() (SiteConfig, error) {
	config := SiteConfig{
		Title:       "A Fileloom site",
		Description: "An HTML-first static site made with Fileloom.",
		BaseURL:     "http://localhost:8000",
		Theme:       "default",
		Footer:      "Made with Fileloom.",
	}
	data, err := os.ReadFile(filepath.Join(s.SiteDir, "site.json"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return config, nil
		}
		return config, err
	}
	if err := json.Unmarshal(data, &config); err != nil {
		return config, fmt.Errorf("parse site.json: %w", err)
	}
	if config.Theme == "" {
		config.Theme = "default"
	}
	if config.BaseURL == "" {
		config.BaseURL = "http://localhost:8000"
	}
	return config, nil
}

func (s *Server) listDocuments() ([]Document, error) {
	root := filepath.Join(s.SiteDir, "content")
	var docs []Document
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".html") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		doc, err := parseDocument(filepath.ToSlash(rel), data, info.ModTime())
		if err != nil {
			return fmt.Errorf("parse %s: %w", rel, err)
		}
		docs = append(docs, doc)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list content: %w", err)
	}
	sort.SliceStable(docs, func(i, j int) bool {
		if docs[i].Date == docs[j].Date {
			return docs[i].Path < docs[j].Path
		}
		return docs[i].Date > docs[j].Date
	})
	return docs, nil
}

func parseDocument(rel string, data []byte, modified time.Time) (Document, error) {
	meta, body := parseFrontMatter(string(data))
	doc := Document{
		Path:    filepath.ToSlash(rel),
		Type:    documentType(rel),
		Title:   strings.TrimSpace(meta["title"]),
		Slug:    strings.TrimSpace(meta["slug"]),
		Date:    strings.TrimSpace(meta["date"]),
		Status:  strings.ToLower(strings.TrimSpace(meta["status"])),
		Tags:    parseTags(meta["tags"]),
		Excerpt: strings.TrimSpace(meta["excerpt"]),
		HTML:    strings.TrimSpace(body),
	}
	if doc.Title == "" {
		doc.Title = friendlyTitle(strings.TrimSuffix(filepath.Base(rel), filepath.Ext(rel)))
	}
	if doc.Slug == "" {
		doc.Slug = slugify(strings.TrimSuffix(filepath.Base(rel), filepath.Ext(rel)))
	}
	if doc.Date == "" {
		doc.Date = modified.Format(dateFormat)
	}
	if doc.Status == "" {
		doc.Status = "published"
	}
	if doc.Excerpt == "" {
		doc.Excerpt = excerptFromHTML(doc.HTML)
	}
	doc.URL = documentURL(doc)
	return doc, nil
}

func parseFrontMatter(source string) (map[string]string, string) {
	meta := map[string]string{}
	if !strings.HasPrefix(source, "---\n") {
		return meta, source
	}
	end := strings.Index(source[4:], "\n---")
	if end < 0 {
		return meta, source
	}
	end += 4
	front := source[4:end]
	bodyStart := end + len("\n---")
	if bodyStart < len(source) && source[bodyStart] == '\n' {
		bodyStart++
	}
	for _, line := range strings.Split(front, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		meta[strings.ToLower(strings.TrimSpace(key))] = strings.TrimSpace(value)
	}
	return meta, source[bodyStart:]
}

func parseTags(value string) []string {
	value = strings.TrimSpace(strings.Trim(value, "[]"))
	if value == "" {
		return nil
	}
	seen := map[string]bool{}
	var tags []string
	for _, raw := range strings.Split(value, ",") {
		tag := strings.TrimSpace(strings.Trim(raw, "\"'"))
		if tag != "" && !seen[strings.ToLower(tag)] {
			seen[strings.ToLower(tag)] = true
			tags = append(tags, tag)
		}
	}
	sort.Strings(tags)
	return tags
}

func (s *Server) writeDocument(doc Document) error {
	rel, err := normalizeContentPath(doc.Path)
	if err != nil {
		return err
	}
	if doc.Type == "" {
		doc.Type = documentType(rel)
	}
	if doc.Title == "" {
		doc.Title = friendlyTitle(doc.Slug)
	}
	if doc.Slug == "" {
		doc.Slug = slugify(doc.Title)
	}
	if doc.Date == "" {
		doc.Date = time.Now().Format(dateFormat)
	}
	if doc.Status == "" {
		doc.Status = "draft"
	}
	if doc.Excerpt == "" {
		doc.Excerpt = excerptFromHTML(doc.HTML)
	}
	path := filepath.Join(s.SiteDir, "content", filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create content directory: %w", err)
	}
	contents := serializeDocument(doc)
	tmp, err := os.CreateTemp(filepath.Dir(path), ".fileloom-*.tmp")
	if err != nil {
		return fmt.Errorf("create content temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.WriteString(contents); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write content: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close content: %w", err)
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace content: %w", err)
	}
	return nil
}

func serializeDocument(doc Document) string {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "title: %s\n", strings.TrimSpace(doc.Title))
	fmt.Fprintf(&b, "slug: %s\n", strings.TrimSpace(doc.Slug))
	fmt.Fprintf(&b, "date: %s\n", strings.TrimSpace(doc.Date))
	fmt.Fprintf(&b, "status: %s\n", strings.TrimSpace(doc.Status))
	if len(doc.Tags) > 0 {
		fmt.Fprintf(&b, "tags: [%s]\n", strings.Join(doc.Tags, ", "))
	}
	if strings.TrimSpace(doc.Excerpt) != "" {
		fmt.Fprintf(&b, "excerpt: %s\n", strings.TrimSpace(doc.Excerpt))
	}
	b.WriteString("---\n\n")
	b.WriteString(strings.TrimSpace(doc.HTML))
	b.WriteString("\n")
	return b.String()
}

func normalizeContentPath(value string) (string, error) {
	value = strings.TrimSpace(strings.TrimPrefix(value, "/"))
	value = strings.TrimPrefix(value, "content/")
	if value == "" || filepath.IsAbs(value) {
		return "", errors.New("content path is required")
	}
	for _, part := range strings.Split(filepath.ToSlash(value), "/") {
		if part == ".." {
			return "", errors.New("parent paths are not allowed")
		}
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(value)))
	if clean == "." || strings.HasPrefix(clean, "../") || !strings.EqualFold(filepath.Ext(clean), ".html") {
		return "", errors.New("content path must be an html file")
	}
	if !strings.HasPrefix(clean, "pages/") && !strings.HasPrefix(clean, "posts/") {
		return "", errors.New("content path must be under pages/ or posts/")
	}
	return clean, nil
}

func documentType(rel string) string {
	if strings.HasPrefix(filepath.ToSlash(rel), "posts/") {
		return "post"
	}
	return "page"
}

func documentURL(doc Document) string {
	rel := strings.TrimSuffix(filepath.ToSlash(doc.Path), ".html")
	if doc.Type == "post" {
		rel = strings.TrimPrefix(rel, "posts/")
	} else {
		rel = strings.TrimPrefix(rel, "pages/")
	}
	if rel == "" || rel == "index" {
		return "/"
	}
	return "/" + strings.Trim(rel, "/") + "/"
}

func friendlyTitle(value string) string {
	value = strings.ReplaceAll(value, "-", " ")
	value = strings.ReplaceAll(value, "_", " ")
	words := strings.Fields(value)
	for i := range words {
		if len(words[i]) > 0 {
			words[i] = strings.ToUpper(words[i][:1]) + words[i][1:]
		}
	}
	return strings.Join(words, " ")
}

func slugify(value string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			lastDash = false
		} else if b.Len() > 0 && !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func excerptFromHTML(source string) string {
	clean := regexp.MustCompile(`<[^>]+>`).ReplaceAllString(source, " ")
	clean = strings.Join(strings.Fields(clean), " ")
	if len(clean) > 180 {
		return strings.TrimSpace(clean[:177]) + "…"
	}
	return clean
}

func (s *Server) loadDocument(value string) (Document, error) {
	rel, err := normalizeContentPath(value)
	if err != nil {
		return Document{}, err
	}
	path := filepath.Join(s.SiteDir, "content", filepath.FromSlash(rel))
	data, err := os.ReadFile(path)
	if err != nil {
		return Document{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return Document{}, err
	}
	return parseDocument(rel, data, info.ModTime())
}

func (s *Server) Build() (BuildResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buildLocked()
}

func (s *Server) buildLocked() (BuildResult, error) {
	config, err := s.loadSiteConfig()
	if err != nil {
		return BuildResult{}, err
	}
	docs, err := s.listDocuments()
	if err != nil {
		return BuildResult{}, err
	}
	published := make([]Document, 0, len(docs))
	var pages, posts []Document
	for _, doc := range docs {
		if strings.EqualFold(doc.Status, "draft") || strings.EqualFold(doc.Status, "private") {
			continue
		}
		published = append(published, doc)
		if doc.Type == "post" {
			posts = append(posts, doc)
		} else if doc.Path != "pages/index.html" {
			pages = append(pages, doc)
		}
	}
	sort.SliceStable(posts, func(i, j int) bool { return posts[i].Date > posts[j].Date })
	sort.SliceStable(pages, func(i, j int) bool { return pages[i].Path < pages[j].Path })

	publicDir := filepath.Join(s.SiteDir, "public")
	if err := os.RemoveAll(publicDir); err != nil {
		return BuildResult{}, fmt.Errorf("clear public directory: %w", err)
	}
	if err := os.MkdirAll(publicDir, 0o755); err != nil {
		return BuildResult{}, err
	}
	themeDir := filepath.Join(s.SiteDir, "themes", config.Theme)
	if err := copyDir(filepath.Join(themeDir, "assets"), filepath.Join(publicDir, "theme")); err != nil {
		return BuildResult{}, fmt.Errorf("copy theme assets: %w", err)
	}
	if err := copyDir(filepath.Join(s.SiteDir, "media"), filepath.Join(publicDir, "media")); err != nil {
		return BuildResult{}, fmt.Errorf("copy media: %w", err)
	}

	layout := readThemeTemplate(themeDir, "layout.html", defaultLayoutTemplate)
	indexTemplate := readThemeTemplate(themeDir, "index.html", defaultIndexTemplate)
	postTemplate := readThemeTemplate(themeDir, "post.html", defaultPostTemplate)
	pageTemplate := readThemeTemplate(themeDir, "page.html", defaultPageTemplate)
	tagTemplate := readThemeTemplate(themeDir, "tag.html", defaultTagTemplate)
	navigation := navigationHTML(pages)
	postCards := postCardsHTML(posts)

	indexDoc := Document{Type: "page", Title: config.Title, Date: time.Now().Format(dateFormat), URL: "/"}
	indexValues := templateValues(indexDoc, config, navigation, postCards)
	indexValues["content"] = applyTokens(indexTemplate, indexValues)
	if err := writePublic(filepath.Join(publicDir, "index.html"), applyTokens(layout, indexValues)); err != nil {
		return BuildResult{}, err
	}

	files := 1
	for _, doc := range published {
		values := templateValues(doc, config, navigation, postCards)
		values["content"] = doc.HTML
		bodyTemplate := pageTemplate
		if doc.Type == "post" {
			bodyTemplate = postTemplate
		}
		body := applyTokens(bodyTemplate, values)
		values["content"] = body
		output := applyTokens(layout, values)
		outputPath := filepath.Join(publicDir, filepath.FromSlash(strings.TrimPrefix(doc.URL, "/")), "index.html")
		if doc.URL == "/" {
			outputPath = filepath.Join(publicDir, "index.html")
		}
		if err := writePublic(outputPath, output); err != nil {
			return BuildResult{}, err
		}
		files++
	}

	tagMap := map[string][]Document{}
	for _, post := range posts {
		for _, tag := range post.Tags {
			key := strings.ToLower(strings.TrimSpace(tag))
			if key != "" {
				tagMap[key] = append(tagMap[key], post)
			}
		}
	}
	for tag, tagPosts := range tagMap {
		tagName := tag
		if len(tagPosts) > 0 {
			for _, candidate := range tagPosts[0].Tags {
				if strings.EqualFold(candidate, tag) {
					tagName = candidate
					break
				}
			}
		}
		doc := Document{Type: "page", Title: tagName, Date: time.Now().Format(dateFormat), URL: "/tag/" + slugify(tagName) + "/"}
		values := templateValues(doc, config, navigation, postCardsHTML(tagPosts))
		values["tag"] = html.EscapeString(tagName)
		values["content"] = applyTokens(tagTemplate, values)
		output := applyTokens(layout, values)
		if err := writePublic(filepath.Join(publicDir, "tag", slugify(tagName), "index.html"), output); err != nil {
			return BuildResult{}, err
		}
		files++
	}

	if err := writePublic(filepath.Join(publicDir, "rss.xml"), renderRSS(config, posts)); err != nil {
		return BuildResult{}, err
	}
	files++
	if err := writePublic(filepath.Join(publicDir, "sitemap.xml"), renderSitemap(config, published, tagMap)); err != nil {
		return BuildResult{}, err
	}
	files++

	result := BuildResult{GeneratedAt: time.Now().Format(time.RFC3339), Files: files, Published: len(published)}
	s.lastBuild = result
	return result, nil
}

func readThemeTemplate(themeDir, name, fallback string) string {
	data, err := os.ReadFile(filepath.Join(themeDir, name))
	if err != nil {
		return fallback
	}
	return string(data)
}

func templateValues(doc Document, config SiteConfig, navigation, posts string) map[string]string {
	tags := make([]string, 0, len(doc.Tags))
	for _, tag := range doc.Tags {
		tags = append(tags, `<a href="/tag/`+url.PathEscape(slugify(tag))+`/">`+html.EscapeString(tag)+`</a>`)
	}
	year := doc.Date
	if len(year) >= 4 {
		year = year[:4]
	}
	return map[string]string{
		"title":            html.EscapeString(doc.Title),
		"date":             html.EscapeString(doc.Date),
		"excerpt":          html.EscapeString(doc.Excerpt),
		"slug":             html.EscapeString(doc.Slug),
		"url":              html.EscapeString(doc.URL),
		"type":             html.EscapeString(doc.Type),
		"year":             html.EscapeString(year),
		"tags":             strings.Join(tags, " · "),
		"site.title":       html.EscapeString(config.Title),
		"site.description": html.EscapeString(config.Description),
		"site.footer":      html.EscapeString(config.Footer),
		"site.base_url":    html.EscapeString(config.BaseURL),
		"site.theme":       html.EscapeString(config.Theme),
		"theme.css":        "/theme/style.css",
		"navigation":       navigation,
		"posts":            posts,
	}
}

func applyTokens(source string, values map[string]string) string {
	return tokenPattern.ReplaceAllStringFunc(source, func(token string) string {
		match := tokenPattern.FindStringSubmatch(token)
		if len(match) == 2 {
			return values[match[1]]
		}
		return ""
	})
}

func navigationHTML(pages []Document) string {
	items := []string{`<a href="/">Home</a>`}
	for _, page := range pages {
		items = append(items, `<a href="`+html.EscapeString(page.URL)+`">`+html.EscapeString(page.Title)+`</a>`)
	}
	return strings.Join(items, "")
}

func postCardsHTML(posts []Document) string {
	if len(posts) == 0 {
		return `<p class="empty-state">No published posts yet.</p>`
	}
	var b strings.Builder
	for _, post := range posts {
		fmt.Fprintf(&b, `<article class="post-card"><p class="eyebrow">%s</p><h2><a href="%s">%s</a></h2><p>%s</p></article>`, html.EscapeString(post.Date), html.EscapeString(post.URL), html.EscapeString(post.Title), html.EscapeString(post.Excerpt))
	}
	return b.String()
}

func renderRSS(config SiteConfig, posts []Document) string {
	base := strings.TrimRight(config.BaseURL, "/")
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<rss version="2.0"><channel>`)
	fmt.Fprintf(&b, "<title>%s</title><link>%s/</link><description>%s</description>", html.EscapeString(config.Title), html.EscapeString(base), html.EscapeString(config.Description))
	for _, post := range posts {
		fmt.Fprintf(&b, "<item><title>%s</title><link>%s%s</link><guid>%s%s</guid><pubDate>%s</pubDate><description><![CDATA[%s]]></description></item>", html.EscapeString(post.Title), html.EscapeString(base), html.EscapeString(post.URL), html.EscapeString(base), html.EscapeString(post.URL), html.EscapeString(post.Date), post.HTML)
	}
	b.WriteString(`</channel></rss>`)
	return b.String()
}

func renderSitemap(config SiteConfig, docs []Document, tags map[string][]Document) string {
	base := strings.TrimRight(config.BaseURL, "/")
	urls := []string{"/"}
	for _, doc := range docs {
		urls = append(urls, doc.URL)
	}
	for tag := range tags {
		urls = append(urls, "/tag/"+slugify(tag)+"/")
	}
	sort.Strings(urls)
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n<urlset xmlns=" + `"http://www.sitemaps.org/schemas/sitemap/0.9">`)
	for _, item := range urls {
		fmt.Fprintf(&b, "<url><loc>%s%s</loc></url>", html.EscapeString(base), html.EscapeString(item))
	}
	b.WriteString(`</urlset>`)
	return b.String()
}

func copyDir(source, destination string) error {
	if _, err := os.Stat(source); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		out, err := os.Create(target)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(out, in)
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
}

func writePublic(path, contents string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		return fmt.Errorf("write generated file %s: %w", path, err)
	}
	return nil
}

func (s *Server) Serve(addr string) error {
	mux := s.Handler()
	slog.Info("starting Fileloom", "addr", addr, "site", s.SiteDir)
	return http.ListenAndServe(addr, mux)
}

func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(s.route)
}

func (s *Server) route(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/_cms" {
		http.Redirect(w, r, "/_cms/", http.StatusFound)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/_cms") {
		if !s.cmsAllowed(r) {
			writeJSONError(w, http.StatusForbidden, "CMS access is restricted to the configured owner")
			return
		}
		s.routeCMS(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/media/") {
		s.serveFile(w, r, filepath.Join(s.SiteDir, "media"), "/media/")
		return
	}
	s.servePublic(w, r)
}

func (s *Server) routeCMS(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/_cms/":
		if r.Method != http.MethodGet {
			methodNotAllowed(w)
			return
		}
		s.serveWebFile(w, r, "admin.html")
	case r.URL.Path == "/_cms/new":
		if r.Method != http.MethodGet {
			methodNotAllowed(w)
			return
		}
		s.serveWebFile(w, r, "new.html")
	case r.URL.Path == "/_cms/editor":
		if r.Method != http.MethodGet {
			methodNotAllowed(w)
			return
		}
		s.handleEditor(w, r)
	case r.URL.Path == "/_cms/editor/frame":
		if r.Method != http.MethodGet {
			methodNotAllowed(w)
			return
		}
		s.handleEditorFrame(w, r)
	case strings.HasPrefix(r.URL.Path, "/_cms/assets/"):
		s.serveFile(w, r, s.WebDir, "/_cms/assets/")
	case strings.HasPrefix(r.URL.Path, "/_cms/vvveb/"):
		s.serveFile(w, r, filepath.Join(s.WebDir, "vvvebjs"), "/_cms/vvveb/")
	case strings.HasPrefix(r.URL.Path, "/_cms/media/"):
		s.serveFile(w, r, filepath.Join(s.SiteDir, "media"), "/_cms/media/")
	case strings.HasPrefix(r.URL.Path, "/_cms/api/"):
		s.handleAPI(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) serveWebFile(w http.ResponseWriter, r *http.Request, name string) {
	path := filepath.Join(s.WebDir, name)
	if _, err := os.Stat(path); err != nil {
		http.Error(w, "Fileloom web asset not found", http.StatusNotFound)
		return
	}
	setContentType(w, path)
	http.ServeFile(w, r, path)
}

func (s *Server) serveFile(w http.ResponseWriter, r *http.Request, root, prefix string) {
	rel := strings.TrimPrefix(r.URL.Path, prefix)
	rel = strings.TrimPrefix(rel, "/")
	if rel == "" {
		rel = "index.html"
	}
	rel, err := safeRelativePath(rel)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	path := filepath.Join(root, filepath.FromSlash(rel))
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}
	setContentType(w, path)
	http.ServeFile(w, r, path)
}

func safeRelativePath(value string) (string, error) {
	value = strings.TrimPrefix(value, "/")
	if value == "" || filepath.IsAbs(value) {
		return "", errors.New("invalid relative path")
	}
	for _, part := range strings.Split(filepath.ToSlash(value), "/") {
		if part == ".." {
			return "", errors.New("parent paths are not allowed")
		}
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(value)))
	if clean == "." || strings.HasPrefix(clean, "../") {
		return "", errors.New("invalid relative path")
	}
	return clean, nil
}

func setContentType(w http.ResponseWriter, path string) {
	if contentType := mime.TypeByExtension(filepath.Ext(path)); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
}

func (s *Server) servePublic(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w)
		return
	}
	requested := strings.TrimPrefix(r.URL.Path, "/")
	candidates := []string{}
	if requested == "" {
		candidates = append(candidates, "index.html")
	} else {
		if strings.HasSuffix(requested, "/") {
			candidates = append(candidates, requested+"index.html")
		} else {
			candidates = append(candidates, requested, requested+"/index.html", requested+".html")
		}
	}
	publicDir := filepath.Join(s.SiteDir, "public")
	for _, candidate := range candidates {
		rel, err := safeRelativePath(candidate)
		if err != nil {
			continue
		}
		path := filepath.Join(publicDir, filepath.FromSlash(rel))
		info, err := os.Stat(path)
		if err == nil && !info.IsDir() {
			setContentType(w, path)
			http.ServeFile(w, r, path)
			return
		}
	}
	http.NotFound(w, r)
}

func (s *Server) cmsAllowed(r *http.Request) bool {
	if s.OwnerEmail == "" {
		return true
	}
	email := strings.TrimSpace(r.Header.Get("X-ExeDev-Email"))
	if email == "" {
		return true
	}
	return strings.EqualFold(email, s.OwnerEmail)
}

func (s *Server) handleAPI(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/_cms/api/site":
		s.handleSiteAPI(w, r)
	case "/_cms/api/items":
		s.handleItemsAPI(w, r)
	case "/_cms/api/build":
		s.handleBuildAPI(w, r)
	case "/_cms/api/editor-save":
		s.handleEditorSaveAPI(w, r)
	case "/_cms/api/media":
		s.handleMediaAPI(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleSiteAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	s.mu.RLock()
	lastBuild := s.lastBuild
	s.mu.RUnlock()
	config, err := s.loadSiteConfig()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	docs, err := s.listDocuments()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	pages, posts := 0, 0
	for _, doc := range docs {
		if doc.Type == "post" {
			posts++
		} else {
			pages++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"app":         appName,
		"site":        config,
		"pages":       pages,
		"posts":       posts,
		"media_count": mediaCount(filepath.Join(s.SiteDir, "media")),
		"last_build":  lastBuild,
	})
}

func (s *Server) handleItemsAPI(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		docs, err := s.listDocuments()
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		items := make([]map[string]any, 0, len(docs))
		for _, doc := range docs {
			items = append(items, map[string]any{
				"path":       doc.Path,
				"type":       doc.Type,
				"title":      doc.Title,
				"date":       doc.Date,
				"status":     doc.Status,
				"tags":       doc.Tags,
				"excerpt":    doc.Excerpt,
				"url":        doc.URL,
				"editor_url": "/_cms/editor?path=" + url.QueryEscape(doc.Path),
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	case http.MethodPost:
		s.handleCreateItem(w, r)
	default:
		methodNotAllowed(w)
	}
}

type createItemInput struct {
	Type    string `json:"type"`
	Title   string `json:"title"`
	Slug    string `json:"slug"`
	Date    string `json:"date"`
	Status  string `json:"status"`
	Tags    string `json:"tags"`
	Excerpt string `json:"excerpt"`
	HTML    string `json:"html"`
}

func (s *Server) handleCreateItem(w http.ResponseWriter, r *http.Request) {
	var input createItemInput
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		if err := json.NewDecoder(io.LimitReader(r.Body, maxEditorSize)).Decode(&input); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
	} else {
		if err := r.ParseForm(); err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		input = createItemInput{
			Type: r.FormValue("type"), Title: r.FormValue("title"), Slug: r.FormValue("slug"),
			Date: r.FormValue("date"), Status: r.FormValue("status"), Tags: r.FormValue("tags"),
			Excerpt: r.FormValue("excerpt"), HTML: r.FormValue("html"),
		}
	}
	input.Type = strings.ToLower(strings.TrimSpace(input.Type))
	if input.Type != "page" && input.Type != "post" {
		input.Type = "post"
	}
	input.Title = strings.TrimSpace(input.Title)
	if input.Title == "" {
		writeJSONError(w, http.StatusBadRequest, "title is required")
		return
	}
	input.Slug = slugify(input.Slug)
	if input.Slug == "" {
		input.Slug = slugify(input.Title)
	}
	if input.Date == "" {
		input.Date = time.Now().Format(dateFormat)
	}
	if input.Status != "published" {
		input.Status = "draft"
	}
	if input.HTML == "" {
		input.HTML = "<h1>" + html.EscapeString(input.Title) + "</h1><p>Start writing here.</p>"
	}
	path := filepath.ToSlash(filepath.Join("pages", input.Slug+".html"))
	if input.Type == "post" {
		year := time.Now().Format("2006")
		if len(input.Date) >= 4 {
			year = input.Date[:4]
		}
		path = filepath.ToSlash(filepath.Join("posts", year, input.Slug+".html"))
	}
	if _, err := os.Stat(filepath.Join(s.SiteDir, "content", filepath.FromSlash(path))); err == nil {
		writeJSONError(w, http.StatusConflict, "an item with that slug already exists")
		return
	}
	doc := Document{
		Path: path, Type: input.Type, Title: input.Title, Slug: input.Slug,
		Date: input.Date, Status: input.Status, Tags: parseTags(input.Tags),
		Excerpt: input.Excerpt, HTML: input.HTML,
	}
	if err := s.writeDocument(doc); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if input.Status == "published" {
		if _, err := s.Build(); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "item saved but build failed: "+err.Error())
			return
		}
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"ok": true, "item": map[string]string{
			"path": path, "editor_url": "/_cms/editor?path=" + url.QueryEscape(path),
		},
	})
}

func (s *Server) handleBuildAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	result, err := s.Build()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "build": result})
}

func (s *Server) handleEditorSaveAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxEditorSize)
	if err := r.ParseForm(); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	path := r.FormValue("file")
	if path == "" {
		path = r.FormValue("path")
	}
	doc, err := s.loadDocument(path)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	doc.HTML = strings.TrimSpace(extractBodyHTML(r.FormValue("html")))
	if err := s.writeDocument(doc); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var build BuildResult
	if !strings.EqualFold(doc.Status, "draft") && !strings.EqualFold(doc.Status, "private") {
		build, err = s.Build()
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "saved but build failed: "+err.Error())
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "path": doc.Path, "build": build})
}

func extractBodyHTML(source string) string {
	lower := strings.ToLower(source)
	start := strings.Index(lower, "<body")
	if start < 0 {
		return source
	}
	start = strings.Index(source[start:], ">") + start + 1
	if start <= 0 {
		return source
	}
	end := strings.Index(lower[start:], "</body>")
	if end < 0 {
		return source[start:]
	}
	return source[start : start+end]
}

func (s *Server) handleEditor(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		http.Redirect(w, r, "/_cms/", http.StatusFound)
		return
	}
	doc, err := s.loadDocument(path)
	if err != nil {
		http.Error(w, "Content item not found", http.StatusNotFound)
		return
	}
	source, err := os.ReadFile(filepath.Join(s.WebDir, "vvvebjs", "editor.html"))
	if err != nil {
		http.Error(w, "VvvebJs editor is not installed", http.StatusNotFound)
		return
	}
	pageURL := "/_cms/editor/frame?path=" + url.QueryEscape(doc.Path)
	pages := map[string]map[string]string{
		"current": {
			"name": "current", "file": doc.Path, "url": pageURL,
			"title": doc.Title, "description": doc.Excerpt,
		},
	}
	pagesJSON, _ := json.Marshal(pages)
	pagesJSON = bytes.ReplaceAll(pagesJSON, []byte("<"), []byte(`\\u003c`))
	htmlSource := string(source)
	htmlSource = strings.ReplaceAll(htmlSource, `<base href="">`, `<base href="/_cms/vvveb/">`)
	htmlSource = strings.ReplaceAll(htmlSource, `<title>VvvebJs</title>`, `<title>Fileloom · Visual editor</title>`)
	htmlSource = strings.ReplaceAll(htmlSource, `window.mediaPath = '../../media';`, `window.mediaPath = '/_cms/media/'; window.mediaScanUrl = '/_cms/api/media'; window.uploadUrl = '/_cms/api/media';`)
	htmlSource = strings.ReplaceAll(htmlSource, `Vvveb.themeBaseUrl = 'demo/landing/';`, `Vvveb.themeBaseUrl = '/_cms/vvveb/';`)
	htmlSource = strings.ReplaceAll(htmlSource, `<script src="demo/landing/sections/sections.js"></script>`, "")
	htmlSource = strings.ReplaceAll(htmlSource, `<script src="demo/landing/styles/styles.js"></script>`, "")
	htmlSource = strings.ReplaceAll(htmlSource, `<script src="libs/builder/plugin-google-fonts.js"></script>`, "")
	htmlSource = strings.ReplaceAll(htmlSource, `<script src="libs/builder/plugin-ai-assistant.js"></script>`, "")
	htmlSource = regexp.MustCompile(`data-vvveb-url="[^"]*"`).ReplaceAllString(htmlSource, `data-vvveb-url="/_cms/api/editor-save"`)
	htmlSource = strings.ReplaceAll(htmlSource, "save.php", "/_cms/api/editor-save")
	badge := `<style>#fileloom-editor-badge{position:fixed;left:16px;bottom:16px;z-index:9999;background:#7557ff;color:#fff;border-radius:999px;padding:7px 12px;font:700 11px/1 system-ui;letter-spacing:.1em;box-shadow:0 8px 20px #0002}#fileloom-editor-badge span{opacity:.7;font-weight:500;letter-spacing:0}</style><div id="fileloom-editor-badge">FILELOOM <span>visual editor</span></div>`
	htmlSource = strings.Replace(htmlSource, "</head>", badge+"</head>", 1)
	marker := "let pages = defaultPages;"
	boot := `window.fileloomPages = ` + string(pagesJSON) + `;` + "\n\t" + "let pages = window.fileloomPages || defaultPages;"
	if !strings.Contains(htmlSource, marker) {
		http.Error(w, "VvvebJs editor template is incompatible", http.StatusInternalServerError)
		return
	}
	htmlSource = strings.Replace(htmlSource, marker, boot, 1)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, htmlSource)
}

func (s *Server) handleEditorFrame(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	doc, err := s.loadDocument(path)
	if err != nil {
		http.Error(w, "Content item not found", http.StatusNotFound)
		return
	}
	config, err := s.loadSiteConfig()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	css, _ := os.ReadFile(filepath.Join(s.SiteDir, "themes", config.Theme, "assets", "style.css"))
	cssText := strings.ReplaceAll(string(css), "</style>", "<\\/style>")
	body := doc.HTML
	frame := `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>` + html.EscapeString(doc.Title) + `</title><style>` + cssText + `</style><style>body{min-height:100vh}body:before{content:"Fileloom content canvas";display:block;margin:20px auto 0;max-width:790px;color:#8c97a6;font:600 11px/1.2 system-ui;letter-spacing:.12em;text-transform:uppercase}</style></head><body>` + body + `</body></html>`
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, frame)
}

func (s *Server) handleMediaAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusNotImplemented, "media uploads are coming next; add files under site/media for now")
		return
	}
	root := filepath.Join(s.SiteDir, "media")
	result, err := mediaTree(root)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func mediaTree(root string) ([]map[string]any, error) {
	var files []map[string]any
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || path == root {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		files = append(files, map[string]any{
			"name": filepath.Base(rel), "type": "file", "path": rel,
			"size": info.Size(),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i]["path"].(string) < files[j]["path"].(string) })
	return files, nil
}

func mediaCount(root string) int {
	files, err := mediaTree(root)
	if err != nil {
		return 0
	}
	return len(files)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"ok": false, "error": message})
}

func methodNotAllowed(w http.ResponseWriter) {
	w.Header().Set("Allow", "GET, HEAD, POST")
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}
