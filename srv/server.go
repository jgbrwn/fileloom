package srv

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
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
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	htmlnode "golang.org/x/net/html"
)

const (
	appName       = "Fileloom"
	dateFormat    = "2006-01-02"
	maxEditorSize = 12 << 20
)

type Server struct {
	SiteDir         string
	WebDir          string
	OwnerEmail      string
	BaseURLOverride string

	mu        sync.RWMutex
	writeMu   sync.Mutex
	lastBuild BuildResult
	scheduler sync.Once
}

type SiteConfig struct {
	Title       string    `json:"title"`
	Description string    `json:"description"`
	BaseURL     string    `json:"base_url"`
	Theme       string    `json:"theme"`
	Footer      string    `json:"footer"`
	Git         GitConfig `json:"git"`
}

type GitConfig struct {
	Enabled    bool   `json:"enabled"`
	AutoCommit bool   `json:"auto_commit"`
	AutoPush   bool   `json:"auto_push"`
	CommitOn   string `json:"commit_on"`
	Remote     string `json:"remote"`
	RemoteURL  string `json:"remote_url,omitempty"`
	Branch     string `json:"branch"`
}

type ThemeToken struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type ThemeInfo struct {
	Name        string `json:"name"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Author      string `json:"author"`
	License     string `json:"license"`
	Active      bool   `json:"active"`
	HasLayout   bool   `json:"has_layout"`
	HasStyles   bool   `json:"has_styles"`
}

type GitStatus struct {
	Available  bool   `json:"available"`
	Repo       string `json:"repo"`
	Branch     string `json:"branch"`
	Remote     string `json:"remote"`
	RemoteName string `json:"remote_name"`
	Clean      bool   `json:"clean"`
	Changes    int    `json:"changes"`
	Error      string `json:"error,omitempty"`
}

type Document struct {
	Path      string   `json:"path"`
	Type      string   `json:"type"`
	Title     string   `json:"title"`
	Slug      string   `json:"slug"`
	Date      string   `json:"date"`
	Status    string   `json:"status"`
	Tags      []string `json:"tags"`
	Excerpt   string   `json:"excerpt"`
	Category  string   `json:"category,omitempty"`
	PublishAt string   `json:"publish_at,omitempty"`
	HTML      string   `json:"html,omitempty"`
	URL       string   `json:"url"`
}

type BuildResult struct {
	GeneratedAt string       `json:"generated_at"`
	Files       int          `json:"files"`
	Published   int          `json:"published"`
	Checks      []CheckIssue `json:"checks,omitempty"`
}

type CheckIssue struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Path     string `json:"path"`
	Message  string `json:"message"`
	Line     int    `json:"line,omitempty"`
}

type Revision struct {
	ID        string `json:"id"`
	Path      string `json:"path"`
	CreatedAt string `json:"created_at"`
	Reason    string `json:"reason"`
	SHA256    string `json:"sha256"`
	Size      int64  `json:"size"`
}

type sourceDocument struct {
	Document
	Source       []byte
	BodyStart    int
	FrontMatter  bool
	MetaValuePos map[string][2]int
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
  "footer": "Made with Fileloom.",
  "git": {
    "enabled": false,
    "auto_commit": false,
    "auto_push": false,
    "commit_on": "build",
    "remote": "origin",
    "branch": ""
  }
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
  <footer class="site-footer"><div class="shell">{{site.footer}} <span class="fileloom-attribution">Powered by <a href="https://github.com/jgbrwn/fileloom" rel="noreferrer">Fileloom</a> and <a href="https://github.com/givanz/VvvebJs" rel="noreferrer">VvvebJs</a>.</span></div></footer>
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

const defaultCategoryTemplate = `<section class="hero compact">
  <p class="eyebrow">Category</p>
  <h1>{{category}}</h1>
</section>
<div class="post-grid">{{posts}}</div>
`

const defaultArchiveTemplate = `<section class="hero compact">
  <p class="eyebrow">Archive</p>
  <h1>{{year}}</h1>
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
	return NewWithOptions(siteDir, webDir, ownerEmail, "")
}

func NewWithOptions(siteDir, webDir, ownerEmail, baseURL string) (*Server, error) {
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
	s := &Server{
		SiteDir:         absSite,
		WebDir:          absWeb,
		OwnerEmail:      strings.TrimSpace(ownerEmail),
		BaseURLOverride: strings.TrimSpace(baseURL),
	}
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

	if err := writeIfMissing(filepath.Join(s.SiteDir, ".gitignore"), []byte("/public/\n/.fileloom/\n*.tmp\n")); err != nil {
		return err
	}
	if err := writeIfMissing(filepath.Join(s.SiteDir, "site.json"), []byte(defaultSiteJSON)); err != nil {
		return err
	}
	themeDir := filepath.Join(s.SiteDir, "themes", "default")
	for name, contents := range map[string]string{
		"layout.html":   defaultLayoutTemplate,
		"index.html":    defaultIndexTemplate,
		"post.html":     defaultPostTemplate,
		"page.html":     defaultPageTemplate,
		"tag.html":      defaultTagTemplate,
		"category.html": defaultCategoryTemplate,
		"archive.html":  defaultArchiveTemplate,
	} {
		if err := writeIfMissing(filepath.Join(themeDir, name), []byte(contents)); err != nil {
			return err
		}
	}
	if err := writeIfMissing(filepath.Join(themeDir, "assets", "style.css"), []byte(defaultStyleCSS)); err != nil {
		return err
	}
	if err := writeIfMissing(filepath.Join(themeDir, "theme.json"), []byte(`{
  "title": "Daybreak",
  "description": "The original Fileloom light theme.",
  "author": "Fileloom",
  "license": "MIT"
}
`)); err != nil {
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
			Path:     filepath.ToSlash(filepath.Join("posts", time.Now().Format("2006"), "welcome-to-fileloom.html")),
			Type:     "post",
			Title:    "Welcome to Fileloom",
			Slug:     "welcome-to-fileloom",
			Date:     time.Now().Format(dateFormat),
			Status:   "published",
			Tags:     []string{"fileloom", "static-sites"},
			Category: "Notes",
			Excerpt:  "A tiny visual CMS where HTML files are the source of truth.",
			HTML:     "<p>Fileloom keeps the source close to the metal: content is HTML, the filesystem is the database, and publishing produces ordinary static files.</p><p>Try editing this post, then build the site.</p>",
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
		Git:         GitConfig{CommitOn: "build", Remote: "origin"},
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
	if config.Git.CommitOn == "" {
		config.Git.CommitOn = "build"
	}
	if config.Git.Remote == "" {
		config.Git.Remote = "origin"
	}
	if override := strings.TrimSpace(s.BaseURLOverride); override != "" {
		config.BaseURL = normalizeBaseURL(override)
	} else if envBaseURL := strings.TrimSpace(os.Getenv("FILELOOM_BASE_URL")); envBaseURL != "" {
		config.BaseURL = normalizeBaseURL(envBaseURL)
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
	source, err := parseSourceDocument(rel, data, modified)
	if err != nil {
		return Document{}, err
	}
	return source.Document, nil
}

func parseSourceDocument(rel string, data []byte, modified time.Time) (sourceDocument, error) {
	meta, body, bodyStart, positions, hasFrontMatter := parseFrontMatterSource(string(data))
	doc := Document{
		Path:      filepath.ToSlash(rel),
		Type:      documentType(rel),
		Title:     strings.TrimSpace(meta["title"]),
		Slug:      strings.TrimSpace(meta["slug"]),
		Date:      strings.TrimSpace(meta["date"]),
		Status:    strings.ToLower(strings.TrimSpace(meta["status"])),
		Tags:      parseTags(meta["tags"]),
		Category:  strings.TrimSpace(meta["category"]),
		PublishAt: strings.TrimSpace(meta["publish_at"]),
		Excerpt:   strings.TrimSpace(meta["excerpt"]),
		HTML:      strings.TrimSpace(body),
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
	return sourceDocument{Document: doc, Source: append([]byte(nil), data...), BodyStart: bodyStart, FrontMatter: hasFrontMatter, MetaValuePos: positions}, nil
}

func parseFrontMatter(source string) (map[string]string, string) {
	meta, body, _, _, _ := parseFrontMatterSource(source)
	return meta, body
}

func parseFrontMatterSource(source string) (map[string]string, string, int, map[string][2]int, bool) {
	meta := map[string]string{}
	positions := map[string][2]int{}
	if !strings.HasPrefix(source, "---\n") {
		return meta, source, 0, positions, false
	}
	closingOffset := strings.Index(source[4:], "\n---")
	if closingOffset < 0 {
		return meta, source, 0, positions, false
	}
	closingOffset += 4
	bodyStart := closingOffset + len("\n---")
	if bodyStart < len(source) && source[bodyStart] == '\n' {
		bodyStart++
	}
	for lineStart := 4; lineStart < closingOffset; {
		lineEnd := strings.IndexByte(source[lineStart:closingOffset], '\n')
		if lineEnd < 0 {
			lineEnd = closingOffset
		} else {
			lineEnd += lineStart
		}
		line := source[lineStart:lineEnd]
		if key, value, ok := strings.Cut(line, ":"); ok {
			key = strings.ToLower(strings.TrimSpace(key))
			trimmed := strings.TrimSpace(value)
			meta[key] = trimmed
			if trimmed != "" {
				leading := len(value) - len(strings.TrimLeft(value, " \t"))
				valueStart := lineStart + strings.IndexByte(line, ':') + 1 + leading
				positions[key] = [2]int{valueStart, valueStart + len(trimmed)}
			} else {
				valueStart := lineStart + strings.IndexByte(line, ':') + 1
				positions[key] = [2]int{valueStart, valueStart}
			}
		}
		if lineEnd >= closingOffset {
			break
		}
		lineStart = lineEnd + 1
	}
	return meta, source[bodyStart:], bodyStart, positions, true
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
	if err := writeAtomicFile(path, []byte(contents)); err != nil {
		return fmt.Errorf("write content: %w", err)
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
	if strings.TrimSpace(doc.PublishAt) != "" {
		fmt.Fprintf(&b, "publish_at: %s\n", strings.TrimSpace(doc.PublishAt))
	}
	if len(doc.Tags) > 0 {
		fmt.Fprintf(&b, "tags: [%s]\n", strings.Join(doc.Tags, ", "))
	}
	if strings.TrimSpace(doc.Category) != "" {
		fmt.Fprintf(&b, "category: %s\n", strings.TrimSpace(doc.Category))
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

func normalizeBaseURL(value string) string {
	value = strings.TrimSpace(strings.TrimRight(value, "/"))
	if value == "" {
		return ""
	}
	parsed, err := url.Parse(value)
	if err == nil && parsed.Scheme != "" && parsed.Host != "" {
		return value
	}
	return "https://" + value
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

func (s *Server) loadSourceDocument(value string) (sourceDocument, error) {
	rel, err := normalizeContentPath(value)
	if err != nil {
		return sourceDocument{}, err
	}
	path := filepath.Join(s.SiteDir, "content", filepath.FromSlash(rel))
	data, err := os.ReadFile(path)
	if err != nil {
		return sourceDocument{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return sourceDocument{}, err
	}
	return parseSourceDocument(rel, data, info.ModTime())
}

func sourceSHA256(source []byte) string {
	digest := sha256.Sum256(source)
	return hex.EncodeToString(digest[:])
}

func fileSHA256(filePath string) string {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return ""
	}
	return sourceSHA256(data)
}

type sourceConflictError struct {
	Path string
}

func (e *sourceConflictError) Error() string {
	return fmt.Sprintf("%s changed since it was opened; reload before saving", e.Path)
}

func writeAtomicFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".fileloom-write-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func patchSourceBody(source []byte, bodyStart int, body string) []byte {
	if bodyStart < 0 || bodyStart > len(source) {
		return append([]byte(nil), source...)
	}
	body = strings.TrimSpace(body)
	if body != "" {
		body += "\n"
	}
	patched := make([]byte, 0, bodyStart+len(body))
	patched = append(patched, source[:bodyStart]...)
	patched = append(patched, body...)
	return patched
}

func patchFrontMatter(source []byte, updates map[string]string) []byte {
	text := string(source)
	_, _, _, positions, hasFrontMatter := parseFrontMatterSource(text)
	if !hasFrontMatter {
		keys := make([]string, 0, len(updates))
		for key := range updates {
			keys = append(keys, strings.ToLower(strings.TrimSpace(key)))
		}
		sort.Strings(keys)
		var front strings.Builder
		front.WriteString("---\n")
		for _, key := range keys {
			value := strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(updates[key]), "\r", " "), "\n", " ")
			fmt.Fprintf(&front, "%s: %s\n", key, value)
		}
		front.WriteString("---\n\n")
		front.WriteString(text)
		return []byte(front.String())
	}
	var replacements [][3]string
	for key, value := range updates {
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(value), "\r", " "), "\n", " ")
		if pos, ok := positions[key]; ok {
			replacements = append(replacements, [3]string{strconv.Itoa(pos[0]), strconv.Itoa(pos[1]), value})
			continue
		}
	}
	// Apply existing values from the end so byte offsets remain valid.
	for i := len(replacements) - 1; i >= 0; i-- {
		start, _ := strconv.Atoi(replacements[i][0])
		end, _ := strconv.Atoi(replacements[i][1])
		text = text[:start] + replacements[i][2] + text[end:]
	}
	missing := make([]string, 0)
	_, _, closingOffset, _, _ := parseFrontMatterSourceOffsets(text)
	for key, value := range updates {
		key = strings.ToLower(strings.TrimSpace(key))
		if _, ok := positions[key]; ok {
			continue
		}
		value = strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(value), "\r", " "), "\n", " ")
		missing = append(missing, fmt.Sprintf("%s: %s\n", key, value))
	}
	if len(missing) > 0 && closingOffset >= 0 {
		sort.Strings(missing)
		addition := strings.Join(missing, "")
		text = text[:closingOffset] + addition + text[closingOffset:]
	}
	return []byte(text)
}

func parseFrontMatterSourceOffsets(source string) (int, int, int, map[string][2]int, bool) {
	if !strings.HasPrefix(source, "---\n") {
		return 0, 0, -1, nil, false
	}
	closingOffset := strings.Index(source[4:], "\n---")
	if closingOffset < 0 {
		return 0, 0, -1, nil, false
	}
	closingOffset += 4
	bodyStart := closingOffset + len("\n---")
	if bodyStart < len(source) && source[bodyStart] == '\n' {
		bodyStart++
	}
	return 4, bodyStart, closingOffset, nil, true
}

func (s *Server) patchDocumentSource(pathValue string, body string, patchBody bool, metadata map[string]string, baseSHA, reason string) (Document, error) {
	source, err := s.loadSourceDocument(pathValue)
	if err != nil {
		return Document{}, err
	}
	if baseSHA != "" && !strings.EqualFold(strings.TrimSpace(baseSHA), sourceSHA256(source.Source)) {
		return Document{}, &sourceConflictError{Path: source.Document.Path}
	}
	if err := s.recordRevision(source.Document.Path, reason); err != nil {
		return Document{}, err
	}
	patched := source.Source
	if patchBody {
		patched = patchSourceBody(patched, source.BodyStart, body)
	}
	if len(metadata) > 0 {
		patched = patchFrontMatter(patched, metadata)
	}
	path, err := normalizeContentPath(source.Document.Path)
	if err != nil {
		return Document{}, err
	}
	filePath := filepath.Join(s.SiteDir, "content", filepath.FromSlash(path))
	if err := writeAtomicFile(filePath, patched); err != nil {
		return Document{}, fmt.Errorf("write %s: %w", path, err)
	}
	return s.loadDocument(path)
}

func revisionDirectory(siteDir, rel string) string {
	return filepath.Join(siteDir, ".fileloom", "revisions", filepath.FromSlash(rel))
}

func (s *Server) recordRevision(rel, reason string) error {
	rel, err := normalizeContentPath(rel)
	if err != nil {
		return err
	}
	filePath := filepath.Join(s.SiteDir, "content", filepath.FromSlash(rel))
	source, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}
	return s.recordWorkspaceRevision(rel, source, reason)
}

func (s *Server) recordWorkspaceRevision(rel string, source []byte, reason string) error {
	rel = filepath.ToSlash(filepath.Clean(rel))
	if rel == "." || strings.HasPrefix(rel, "../") || filepath.IsAbs(rel) {
		return errors.New("invalid revision path")
	}
	created := time.Now().UTC()
	id := created.Format("20060102T150405.000000000Z") + "-" + sourceSHA256(source)[:12]
	dir := filepath.Join(s.SiteDir, ".fileloom", "revisions", filepath.FromSlash(rel))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	revision := Revision{ID: id, Path: rel, CreatedAt: created.Format(time.RFC3339Nano), Reason: strings.TrimSpace(reason), SHA256: sourceSHA256(source), Size: int64(len(source))}
	if err := writeAtomicFile(filepath.Join(dir, id+".html"), source); err != nil {
		return err
	}
	metadata, err := json.MarshalIndent(revision, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomicFile(filepath.Join(dir, id+".json"), append(metadata, '\n'))
}

func (s *Server) listRevisions(rel string) ([]Revision, error) {
	rel, err := normalizeContentPath(rel)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(revisionDirectory(s.SiteDir, rel))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []Revision{}, nil
		}
		return nil, err
	}
	var revisions []Revision
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(revisionDirectory(s.SiteDir, rel), entry.Name()))
		if err != nil {
			return nil, err
		}
		var revision Revision
		if err := json.Unmarshal(data, &revision); err != nil {
			continue
		}
		revisions = append(revisions, revision)
	}
	sort.Slice(revisions, func(i, j int) bool { return revisions[i].CreatedAt > revisions[j].CreatedAt })
	return revisions, nil
}

func (s *Server) restoreRevision(rel, id string) (Document, error) {
	rel, err := normalizeContentPath(rel)
	if err != nil {
		return Document{}, err
	}
	if !regexp.MustCompile(`^[A-Za-z0-9_.-]+$`).MatchString(id) {
		return Document{}, errors.New("invalid revision id")
	}
	dir := revisionDirectory(s.SiteDir, rel)
	metadata, err := os.ReadFile(filepath.Join(dir, id+".json"))
	if err != nil {
		return Document{}, err
	}
	var revision Revision
	if err := json.Unmarshal(metadata, &revision); err != nil {
		return Document{}, err
	}
	source, err := os.ReadFile(filepath.Join(dir, id+".html"))
	if err != nil {
		return Document{}, err
	}
	if sourceSHA256(source) != revision.SHA256 {
		return Document{}, errors.New("revision checksum mismatch")
	}
	if err := s.recordRevision(rel, "Before restore "+id); err != nil {
		return Document{}, err
	}
	if err := writeAtomicFile(filepath.Join(s.SiteDir, "content", filepath.FromSlash(rel)), source); err != nil {
		return Document{}, err
	}
	return s.loadDocument(rel)
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
	now := time.Now().UTC()
	published := make([]Document, 0, len(docs))
	var pages, posts []Document
	for _, doc := range docs {
		if !documentIsPublishable(doc, now) {
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
	outputDir, err := os.MkdirTemp(s.SiteDir, ".fileloom-build-*")
	if err != nil {
		return BuildResult{}, fmt.Errorf("create build directory: %w", err)
	}
	defer os.RemoveAll(outputDir)
	themeDir := filepath.Join(s.SiteDir, "themes", config.Theme)
	themeStylesheetURL := fmt.Sprintf("/theme/style.css?v=%d", time.Now().UnixNano())
	if err := copyDir(filepath.Join(themeDir, "assets"), filepath.Join(outputDir, "theme")); err != nil {
		return BuildResult{}, fmt.Errorf("copy theme assets: %w", err)
	}
	if err := copyDir(filepath.Join(s.SiteDir, "media"), filepath.Join(outputDir, "media")); err != nil {
		return BuildResult{}, fmt.Errorf("copy media: %w", err)
	}

	layout := readThemeTemplate(themeDir, "layout.html", defaultLayoutTemplate)
	indexTemplate := readThemeTemplate(themeDir, "index.html", defaultIndexTemplate)
	postTemplate := readThemeTemplate(themeDir, "post.html", defaultPostTemplate)
	pageTemplate := readThemeTemplate(themeDir, "page.html", defaultPageTemplate)
	tagTemplate := readThemeTemplate(themeDir, "tag.html", defaultTagTemplate)
	categoryTemplate := readThemeTemplate(themeDir, "category.html", defaultCategoryTemplate)
	archiveTemplate := readThemeTemplate(themeDir, "archive.html", defaultArchiveTemplate)
	navigation := navigationHTML(pages)
	postCards := postCardsHTML(posts)

	indexDoc := Document{Type: "page", Title: config.Title, Date: time.Now().Format(dateFormat), URL: "/"}
	indexValues := templateValues(indexDoc, config, navigation, postCards)
	indexValues["theme.css"] = themeStylesheetURL
	indexValues["content"] = applyTokens(indexTemplate, indexValues)
	indexOutput := applyTokens(layout, indexValues)
	if err := writePublic(filepath.Join(outputDir, "index.html"), indexOutput); err != nil {
		return BuildResult{}, err
	}

	files := 1
	for _, doc := range published {
		values := templateValues(doc, config, navigation, postCards)
		values["theme.css"] = themeStylesheetURL
		values["content"] = doc.HTML
		bodyTemplate := pageTemplate
		if doc.Type == "post" {
			bodyTemplate = postTemplate
		}
		body := applyTokens(bodyTemplate, values)
		values["content"] = body
		output := applyTokens(layout, values)
		outputPath := filepath.Join(outputDir, filepath.FromSlash(strings.TrimPrefix(doc.URL, "/")), "index.html")
		if doc.URL == "/" {
			outputPath = filepath.Join(outputDir, "index.html")
		}
		if err := writePublic(outputPath, output); err != nil {
			return BuildResult{}, err
		}
		files++
	}

	tagMap := map[string][]Document{}
	categoryMap := map[string][]Document{}
	yearMap := map[string][]Document{}
	for _, post := range posts {
		for _, tag := range post.Tags {
			key := strings.ToLower(strings.TrimSpace(tag))
			if key != "" {
				tagMap[key] = append(tagMap[key], post)
			}
		}
		if category := strings.TrimSpace(post.Category); category != "" {
			categoryMap[strings.ToLower(category)] = append(categoryMap[strings.ToLower(category)], post)
		}
		year := post.Date
		if len(year) >= 4 {
			year = year[:4]
		}
		if year != "" {
			yearMap[year] = append(yearMap[year], post)
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
		values["theme.css"] = themeStylesheetURL
		values["tag"] = html.EscapeString(tagName)
		values["content"] = applyTokens(tagTemplate, values)
		output := applyTokens(layout, values)
		if err := writePublic(filepath.Join(outputDir, "tag", slugify(tagName), "index.html"), output); err != nil {
			return BuildResult{}, err
		}
		files++
	}

	for category, categoryPosts := range categoryMap {
		categoryName := category
		if len(categoryPosts) > 0 && categoryPosts[0].Category != "" {
			categoryName = categoryPosts[0].Category
		}
		doc := Document{Type: "page", Title: categoryName, Date: time.Now().Format(dateFormat), URL: "/category/" + slugify(categoryName) + "/"}
		values := templateValues(doc, config, navigation, postCardsHTML(categoryPosts))
		values["theme.css"] = themeStylesheetURL
		values["category"] = html.EscapeString(categoryName)
		values["content"] = applyTokens(categoryTemplate, values)
		output := applyTokens(layout, values)
		if err := writePublic(filepath.Join(outputDir, "category", slugify(categoryName), "index.html"), output); err != nil {
			return BuildResult{}, err
		}
		files++
	}

	for year, yearPosts := range yearMap {
		doc := Document{Type: "page", Title: year, Date: year, URL: "/archive/" + year + "/"}
		values := templateValues(doc, config, navigation, postCardsHTML(yearPosts))
		values["theme.css"] = themeStylesheetURL
		values["year"] = html.EscapeString(year)
		values["content"] = applyTokens(archiveTemplate, values)
		output := applyTokens(layout, values)
		if err := writePublic(filepath.Join(outputDir, "archive", year, "index.html"), output); err != nil {
			return BuildResult{}, err
		}
		files++
	}

	if err := writePublic(filepath.Join(outputDir, "rss.xml"), renderRSS(config, posts)); err != nil {
		return BuildResult{}, err
	}
	files++
	if err := writePublic(filepath.Join(outputDir, "sitemap.xml"), renderSitemap(config, published, tagMap, categoryMap, yearMap)); err != nil {
		return BuildResult{}, err
	}
	files++

	checks, err := checkBuildOutput(outputDir)
	if err != nil {
		return BuildResult{}, fmt.Errorf("run build checks: %w", err)
	}
	if err := swapPublicDirectory(outputDir, publicDir); err != nil {
		return BuildResult{}, err
	}

	result := BuildResult{GeneratedAt: time.Now().UTC().Format(time.RFC3339), Files: files, Published: len(published), Checks: checks}
	s.lastBuild = result
	if config.Git.Enabled && config.Git.AutoCommit && strings.ToLower(config.Git.CommitOn) == "build" {
		if _, gitErr := s.gitCommitAndPush(config.Git, "Build Fileloom site", false); gitErr != nil {
			slog.Warn("git sync after build failed", "error", gitErr)
		}
	}
	return result, nil
}

func documentIsPublishable(doc Document, now time.Time) bool {
	status := strings.ToLower(strings.TrimSpace(doc.Status))
	if status == "draft" || status == "private" {
		return false
	}
	if status != "scheduled" {
		return true
	}
	publishAt, err := time.Parse(time.RFC3339, strings.TrimSpace(doc.PublishAt))
	return err == nil && !publishAt.After(now)
}

func normalizePublishAt(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed.UTC().Format(time.RFC3339), nil
	}
	parsed, err := time.ParseInLocation("2006-01-02T15:04", value, time.Local)
	if err != nil {
		return "", errors.New("publish time must be an RFC3339 or local datetime value")
	}
	return parsed.UTC().Format(time.RFC3339), nil
}

func swapPublicDirectory(staged, publicDir string) error {
	backup := publicDir + ".backup-" + fmt.Sprint(time.Now().UnixNano())
	hadPublic := false
	if _, err := os.Stat(publicDir); err == nil {
		if err := os.Rename(publicDir, backup); err != nil {
			return fmt.Errorf("stage existing public directory: %w", err)
		}
		hadPublic = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(staged, publicDir); err != nil {
		if hadPublic {
			_ = os.Rename(backup, publicDir)
		}
		return fmt.Errorf("activate built site: %w", err)
	}
	if hadPublic {
		_ = os.RemoveAll(backup)
	}
	return nil
}

func checkBuildOutput(root string) ([]CheckIssue, error) {
	var issues []CheckIssue
	err := filepath.WalkDir(root, func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".html") {
			return nil
		}
		data, err := os.ReadFile(filePath)
		if err != nil {
			return err
		}
		document, err := htmlnode.Parse(bytes.NewReader(data))
		if err != nil {
			return fmt.Errorf("parse %s: %w", filePath, err)
		}
		rel, err := filepath.Rel(root, filePath)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		checkHTMLAccessibility(document, rel, &issues)
		checkHTMLLinks(root, rel, document, &issues)
		return nil
	})
	return issues, err
}

func checkHTMLAccessibility(document *htmlnode.Node, rel string, issues *[]CheckIssue) {
	var htmlElement, titleElement *htmlnode.Node
	labels := map[string]bool{}
	var walk func(*htmlnode.Node)
	walk = func(node *htmlnode.Node) {
		if node.Type == htmlnode.ElementNode {
			switch strings.ToLower(node.Data) {
			case "html":
				htmlElement = node
			case "title":
				titleElement = node
			case "label":
				for _, attr := range node.Attr {
					if attr.Key == "for" && strings.TrimSpace(attr.Val) != "" {
						labels[attr.Val] = true
					}
				}
			case "img":
				if attrValue(node, "alt") == "" {
					*issues = append(*issues, CheckIssue{Severity: "warning", Code: "image-alt", Path: rel, Message: "image is missing an alt attribute"})
				}
			case "input", "select", "textarea":
				typeValue := strings.ToLower(attrValue(node, "type"))
				if node.Data == "input" && (typeValue == "hidden" || typeValue == "submit" || typeValue == "button" || typeValue == "reset") {
					break
				}
				id := attrValue(node, "id")
				name := attrValue(node, "name")
				if (id == "" || !labels[id]) && name == "" {
					*issues = append(*issues, CheckIssue{Severity: "warning", Code: "form-label", Path: rel, Message: "form control may be missing an associated label"})
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(document)
	if htmlElement == nil || attrValue(htmlElement, "lang") == "" {
		*issues = append(*issues, CheckIssue{Severity: "warning", Code: "html-lang", Path: rel, Message: "document is missing an html lang attribute"})
	}
	if titleElement == nil || strings.TrimSpace(nodeText(titleElement)) == "" {
		*issues = append(*issues, CheckIssue{Severity: "warning", Code: "document-title", Path: rel, Message: "document is missing a non-empty title"})
	}
}

func checkHTMLLinks(root, rel string, document *htmlnode.Node, issues *[]CheckIssue) {
	var walk func(*htmlnode.Node)
	walk = func(node *htmlnode.Node) {
		if node.Type == htmlnode.ElementNode {
			var target string
			switch strings.ToLower(node.Data) {
			case "a", "area", "link":
				target = attrValue(node, "href")
			case "img", "script", "source", "video", "audio":
				target = attrValue(node, "src")
			}
			if target != "" && isInternalTarget(target) {
				parsed, err := url.Parse(target)
				if err == nil && parsed.Path != "" && !publicTargetExists(root, rel, parsed.Path) {
					*issues = append(*issues, CheckIssue{Severity: "warning", Code: "broken-link", Path: rel, Message: "internal link target does not exist: " + parsed.Path})
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(document)
}

func attrValue(node *htmlnode.Node, key string) string {
	for _, attr := range node.Attr {
		if strings.EqualFold(attr.Key, key) {
			return strings.TrimSpace(attr.Val)
		}
	}
	return ""
}

func nodeText(node *htmlnode.Node) string {
	if node == nil {
		return ""
	}
	if node.Type == htmlnode.TextNode {
		return node.Data
	}
	var b strings.Builder
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		b.WriteString(nodeText(child))
	}
	return b.String()
}

func isInternalTarget(target string) bool {
	lower := strings.ToLower(strings.TrimSpace(target))
	return lower != "" && !strings.HasPrefix(lower, "#") && !strings.HasPrefix(lower, "http:") && !strings.HasPrefix(lower, "https:") && !strings.HasPrefix(lower, "mailto:") && !strings.HasPrefix(lower, "tel:") && !strings.HasPrefix(lower, "javascript:") && !strings.HasPrefix(lower, "data:")
}

func publicTargetExists(root, currentRel, target string) bool {
	parsed, err := url.Parse(target)
	if err != nil || parsed.Host != "" {
		return true
	}
	requestPath := parsed.Path
	if requestPath == "" {
		return true
	}
	var base string
	if strings.HasPrefix(requestPath, "/") {
		base = path.Clean(requestPath)
	} else {
		currentDir := path.Dir("/" + currentRel)
		base = path.Clean(path.Join(currentDir, requestPath))
	}
	base = strings.TrimPrefix(base, "/")
	candidates := []string{base, path.Join(base, "index.html"), base + ".html"}
	for _, candidate := range candidates {
		if info, err := os.Stat(filepath.Join(root, filepath.FromSlash(candidate))); err == nil && !info.IsDir() {
			return true
		}
	}
	return false
}
func (s *Server) listThemes() ([]ThemeInfo, error) {
	config, err := s.loadSiteConfig()
	if err != nil {
		return nil, err
	}
	root := filepath.Join(s.SiteDir, "themes")
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("list themes: %w", err)
	}
	var themes []ThemeInfo
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if _, err := normalizeThemeName(name); err != nil {
			continue
		}
		meta := struct {
			Title       string `json:"title"`
			Description string `json:"description"`
			Author      string `json:"author"`
			License     string `json:"license"`
		}{}
		if data, readErr := os.ReadFile(filepath.Join(root, name, "theme.json")); readErr == nil {
			_ = json.Unmarshal(data, &meta)
		}
		if meta.Title == "" {
			meta.Title = friendlyTitle(name)
		}
		themes = append(themes, ThemeInfo{
			Name: name, Title: meta.Title, Description: meta.Description,
			Author: meta.Author, License: meta.License, Active: name == config.Theme,
			HasLayout: fileExists(filepath.Join(root, name, "layout.html")),
			HasStyles: fileExists(filepath.Join(root, name, "assets", "style.css")),
		})
	}
	sort.Slice(themes, func(i, j int) bool {
		if themes[i].Active != themes[j].Active {
			return themes[i].Active
		}
		return themes[i].Title < themes[j].Title
	})
	return themes, nil
}

func normalizeThemeName(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || value == "." || value == ".." || value != filepath.Base(value) {
		return "", errors.New("invalid theme name")
	}
	if slugify(value) != value {
		return "", errors.New("theme name must be a slug")
	}
	return value, nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func (s *Server) saveSiteConfig(config SiteConfig) error {
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	path := filepath.Join(s.SiteDir, "site.json")
	tmp, err := os.CreateTemp(s.SiteDir, ".fileloom-site-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func (s *Server) activateTheme(name string) error {
	name, err := normalizeThemeName(name)
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(s.SiteDir, "themes", name)); err != nil {
		return fmt.Errorf("theme %q not found", name)
	}
	config, err := s.loadSiteConfig()
	if err != nil {
		return err
	}
	config.Theme = name
	if err := s.saveSiteConfig(config); err != nil {
		return err
	}
	_, err = s.Build()
	if err != nil {
		return err
	}
	return s.gitChangeIfConfigured("Activate theme " + name)
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
		"category":         html.EscapeString(doc.Category),
		"site.title":       html.EscapeString(config.Title),
		"site.description": html.EscapeString(config.Description),
		"site.footer":      html.EscapeString(config.Footer),
		"site.base_url":    html.EscapeString(config.BaseURL),
		"site.theme":       html.EscapeString(config.Theme),
		"site.attribution": `<span class="fileloom-attribution">Powered by <a href="https://github.com/jgbrwn/fileloom" rel="noreferrer">Fileloom</a> and <a href="https://github.com/givanz/VvvebJs" rel="noreferrer">VvvebJs</a>.</span>`,
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

func renderSitemap(config SiteConfig, docs []Document, tags map[string][]Document, categories map[string][]Document, years map[string][]Document) string {
	base := strings.TrimRight(config.BaseURL, "/")
	urls := []string{"/"}
	for _, doc := range docs {
		urls = append(urls, doc.URL)
	}
	for tag := range tags {
		urls = append(urls, "/tag/"+slugify(tag)+"/")
	}
	for category := range categories {
		urls = append(urls, "/category/"+slugify(category)+"/")
	}
	for year := range years {
		urls = append(urls, "/archive/"+slugify(year)+"/")
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
		if entry.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("symlinks are not allowed in generated assets: %s", path)
		}
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
	if strings.EqualFold(filepath.Ext(path), ".html") {
		contents = ensureAttribution(contents)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		return fmt.Errorf("write generated file %s: %w", path, err)
	}
	return nil
}

func ensureAttribution(source string) string {
	if strings.Contains(source, "class=\"fileloom-attribution\"") || strings.Contains(source, "class='fileloom-attribution'") {
		return source
	}
	attribution := `<span class="fileloom-attribution">Powered by <a href="https://github.com/jgbrwn/fileloom" rel="noreferrer">Fileloom</a> and <a href="https://github.com/givanz/VvvebJs" rel="noreferrer">VvvebJs</a>.</span>`
	if index := strings.LastIndex(strings.ToLower(source), "</body>"); index >= 0 {
		return source[:index] + `<footer class="fileloom-generated-attribution" style="display:block;margin-top:8px;font-size:.8em">` + attribution + `</footer>` + source[index:]
	}
	return source + `<footer class="fileloom-generated-attribution" style="display:block;margin-top:8px;font-size:.8em">` + attribution + `</footer>`
}
func (s *Server) Serve(addr string) error {
	s.startScheduler()
	mux := s.Handler()
	slog.Info("starting Fileloom", "addr", addr, "site", s.SiteDir)
	return http.ListenAndServe(addr, mux)
}

func (s *Server) startScheduler() {
	s.scheduler.Do(func() {
		go func() {
			s.publishDue()
			ticker := time.NewTicker(30 * time.Second)
			defer ticker.Stop()
			for range ticker.C {
				s.publishDue()
			}
		}()
	})
}

func (s *Server) publishDue() {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	docs, err := s.listDocuments()
	if err != nil {
		slog.Warn("scheduled publishing scan failed", "error", err)
		return
	}
	now := time.Now().UTC()
	changed := false
	for _, doc := range docs {
		if strings.ToLower(strings.TrimSpace(doc.Status)) != "scheduled" || strings.TrimSpace(doc.PublishAt) == "" {
			continue
		}
		publishAt, err := time.Parse(time.RFC3339, strings.TrimSpace(doc.PublishAt))
		if err != nil || publishAt.After(now) {
			continue
		}
		if _, err := s.patchDocumentSource(doc.Path, "", false, map[string]string{"status": "published", "publish_at": ""}, "", "Publish scheduled "+doc.Path); err != nil {
			slog.Warn("scheduled publish failed", "path", doc.Path, "error", err)
			continue
		}
		changed = true
	}
	if changed {
		if _, err := s.Build(); err != nil {
			slog.Warn("scheduled build failed", "error", err)
		}
	}
}

func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(s.route)
}

func setSecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
	w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
}
func (s *Server) route(w http.ResponseWriter, r *http.Request) {
	setSecurityHeaders(w)
	if r.URL.Path == "/_cms" || r.URL.Path == "/_cms/" || strings.HasPrefix(r.URL.Path, "/_cms/") {
		if !s.cmsAllowed(r) {
			http.NotFound(w, r)
			return
		}
		if isMutationMethod(r.Method) && !sameOriginRequest(r) {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path == "/_cms" {
			http.Redirect(w, r, "/_cms/", http.StatusFound)
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
	path, _, err := safeResolvedPath(s.WebDir, name)
	if err != nil {
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
	path, info, err := safeResolvedPath(root, rel)
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

func safeResolvedPath(root, rel string) (string, fs.FileInfo, error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", nil, err
	}
	rootReal, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return "", nil, err
	}
	candidate := filepath.Join(rootAbs, filepath.FromSlash(rel))
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", nil, err
	}
	relative, err := filepath.Rel(rootReal, resolved)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", nil, errors.New("resolved path escapes root")
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", nil, err
	}
	return resolved, info, nil
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
	w.Header().Add("Vary", "X-ExeDev-Email")
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
		path, info, err := safeResolvedPath(publicDir, rel)
		if err == nil && !info.IsDir() {
			setContentType(w, path)
			if doc, ok := s.publicDocument(r.URL.Path); ok && s.cmsAllowed(r) {
				data, readErr := os.ReadFile(path)
				if readErr != nil {
					http.NotFound(w, r)
					return
				}
				data = injectOwnerEditToolbar(data, doc)
				w.Header().Set("Cache-Control", "private, no-store")
				w.Header().Add("Vary", "X-ExeDev-Email")
				http.ServeContent(w, r, filepath.Base(path), info.ModTime(), bytes.NewReader(data))
				return
			}
			http.ServeFile(w, r, path)
			return
		}
	}
	http.NotFound(w, r)
}

func (s *Server) publicDocument(requestPath string) (Document, bool) {
	requested := requestPath
	if requested == "" {
		requested = "/"
	}
	if !strings.HasPrefix(requested, "/") {
		requested = "/" + requested
	}
	if !strings.HasSuffix(requested, "/") && !strings.HasSuffix(requested, ".html") {
		requested += "/"
	}
	docs, err := s.listDocuments()
	if err != nil {
		return Document{}, false
	}
	for _, doc := range docs {
		if doc.URL == requested && (doc.Type == "page" || doc.Type == "post") {
			return doc, true
		}
	}
	return Document{}, false
}

func injectOwnerEditToolbar(data []byte, doc Document) []byte {
	toolbar := `<aside data-fileloom-owner-toolbar style="position:fixed;right:16px;bottom:16px;z-index:2147483647;display:flex;align-items:center;gap:8px;padding:8px 10px;border:1px solid #d9dce5;border-radius:999px;background:#fff;color:#202633;box-shadow:0 8px 24px #0002;font:700 12px/1 system-ui,sans-serif"><span>Fileloom owner view</span><a href="/_cms/editor?path=` + url.QueryEscape(doc.Path) + `" style="color:#5038c8;text-decoration:none">Edit this page ↗</a><a href="/_cms/" style="color:#667384;text-decoration:none">CMS</a></aside>`
	lower := strings.ToLower(string(data))
	if index := strings.LastIndex(lower, "</body>"); index >= 0 {
		result := make([]byte, 0, len(data)+len(toolbar))
		result = append(result, data[:index]...)
		result = append(result, toolbar...)
		result = append(result, data[index:]...)
		return result
	}
	return append(data, []byte(toolbar)...)
}
func isMutationMethod(method string) bool {
	return method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions
}

func sameOriginRequest(r *http.Request) bool {
	fetchSite := strings.ToLower(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site")))
	if fetchSite == "cross-site" {
		return false
	}
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	return err == nil && parsed.Host != "" && strings.EqualFold(parsed.Host, r.Host)
}
func (s *Server) cmsAllowed(r *http.Request) bool {
	owner := strings.TrimSpace(s.OwnerEmail)
	if owner == "" {
		return false
	}
	email := strings.TrimSpace(r.Header.Get("X-ExeDev-Email"))
	return email != "" && strings.EqualFold(email, owner)
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
	case "/_cms/api/themes":
		s.handleThemesAPI(w, r)
	case "/_cms/api/themes/activate":
		s.handleThemeActivateAPI(w, r)
	case "/_cms/api/items/status":
		s.handleItemStatusAPI(w, r)
	case "/_cms/api/revisions":
		s.handleRevisionsAPI(w, r)
	case "/_cms/api/revisions/restore":
		s.handleRevisionRestoreAPI(w, r)
	case "/_cms/api/theme-tokens":
		s.handleThemeTokensAPI(w, r)
	case "/_cms/api/export":
		s.handleExportAPI(w, r)
	case "/_cms/api/git":
		s.handleGitAPI(w, r)
	case "/_cms/api/git/config":
		s.handleGitConfigAPI(w, r)
	case "/_cms/api/git/init":
		s.handleGitInitAPI(w, r)
	case "/_cms/api/git/commit":
		s.handleGitCommitAPI(w, r)
	case "/_cms/api/git/push":
		s.handleGitPushAPI(w, r)
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
	themes, themeErr := s.listThemes()
	if themeErr != nil {
		writeJSONError(w, http.StatusInternalServerError, themeErr.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"app":         appName,
		"site":        config,
		"pages":       pages,
		"posts":       posts,
		"media_count": mediaCount(filepath.Join(s.SiteDir, "media")),
		"last_build":  lastBuild,
		"themes":      themes,
		"git":         map[string]any{"config": config.Git, "status": s.gitStatus(config.Git)},
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
				"publish_at": doc.PublishAt,
				"tags":       doc.Tags,
				"category":   doc.Category,
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
	Type      string `json:"type"`
	Title     string `json:"title"`
	Slug      string `json:"slug"`
	Date      string `json:"date"`
	Status    string `json:"status"`
	PublishAt string `json:"publish_at"`
	Tags      string `json:"tags"`
	Excerpt   string `json:"excerpt"`
	Category  string `json:"category"`
	HTML      string `json:"html"`
}

func (s *Server) handleCreateItem(w http.ResponseWriter, r *http.Request) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
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
			Date: r.FormValue("date"), Status: r.FormValue("status"), PublishAt: r.FormValue("publish_at"), Tags: r.FormValue("tags"),
			Category: r.FormValue("category"), Excerpt: r.FormValue("excerpt"), HTML: r.FormValue("html"),
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
	if input.Status != "draft" && input.Status != "published" && input.Status != "scheduled" {
		input.Status = "draft"
	}
	publishAt, err := normalizePublishAt(input.PublishAt)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	input.PublishAt = publishAt
	if input.Status == "scheduled" && input.PublishAt == "" {
		writeJSONError(w, http.StatusBadRequest, "publish_at is required for scheduled content")
		return
	}
	if input.Status != "scheduled" {
		input.PublishAt = ""
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
		Date: input.Date, Status: input.Status, PublishAt: input.PublishAt, Tags: parseTags(input.Tags),
		Category: strings.TrimSpace(input.Category), Excerpt: input.Excerpt, HTML: input.HTML,
	}
	if err := s.writeDocument(doc); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if input.Status == "published" || input.Status == "scheduled" {
		if _, err := s.Build(); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "item saved but build failed: "+err.Error())
			return
		}
	}
	if err := s.gitChangeIfConfigured("Create " + path); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"ok": true, "item": map[string]string{
			"path": path, "editor_url": "/_cms/editor?path=" + url.QueryEscape(path),
		},
	})
}

func (s *Server) handleThemesAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	themes, err := s.listThemes()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"themes": themes})
}

func (s *Server) handleThemeActivateAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := r.ParseForm(); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	name := r.FormValue("name")
	if err := s.activateTheme(name); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "theme": name})
}

func (s *Server) handleItemStatusAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := r.ParseForm(); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	doc, err := s.loadDocument(r.FormValue("path"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	status := strings.ToLower(strings.TrimSpace(r.FormValue("status")))
	if status != "draft" && status != "published" && status != "private" && status != "scheduled" {
		writeJSONError(w, http.StatusBadRequest, "status must be draft, published, private, or scheduled")
		return
	}
	publishAt, err := normalizePublishAt(r.FormValue("publish_at"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if status == "scheduled" && publishAt == "" {
		writeJSONError(w, http.StatusBadRequest, "publish_at is required for scheduled content")
		return
	}
	if status != "scheduled" {
		publishAt = ""
	}
	updated, err := s.patchDocumentSource(doc.Path, "", false, map[string]string{"status": status, "publish_at": publishAt}, r.FormValue("base_sha256"), "Change status for "+doc.Path)
	if err != nil {
		var conflict *sourceConflictError
		if errors.As(err, &conflict) {
			writeJSONError(w, http.StatusConflict, err.Error())
			return
		}
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var build BuildResult
	build, err = s.Build()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "status saved but build failed: "+err.Error())
		return
	}
	if err := s.gitChangeIfConfigured("Change status for " + updated.Path); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "path": updated.Path, "status": updated.Status, "publish_at": updated.PublishAt, "build": build})
}
func (s *Server) handleRevisionsAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	revisions, err := s.listRevisions(r.URL.Query().Get("path"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": r.URL.Query().Get("path"), "revisions": revisions})
}

func (s *Server) handleRevisionRestoreAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := r.ParseForm(); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	doc, err := s.restoreRevision(r.FormValue("path"), r.FormValue("id"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	build, err := s.Build()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "revision restored but build failed: "+err.Error())
		return
	}
	if err := s.gitChangeIfConfigured("Restore " + doc.Path); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "path": doc.Path, "build": build, "source_sha256": fileSHA256(filepath.Join(s.SiteDir, "content", filepath.FromSlash(doc.Path)))})
}
func (s *Server) handleBuildAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
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
	if themeName := strings.TrimSpace(r.FormValue("theme")); themeName != "" || strings.HasPrefix(strings.TrimPrefix(r.FormValue("file"), "/"), "themes/") {
		s.handleThemeSaveAPI(w, r, themeName, r.FormValue("file"))
		return
	}
	path := r.FormValue("file")
	if path == "" {
		path = r.FormValue("path")
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	updated, err := s.patchDocumentSource(path, extractBodyHTML(r.FormValue("html")), true, nil, r.FormValue("base_sha256"), "Edit "+path)
	if err != nil {
		var conflict *sourceConflictError
		if errors.As(err, &conflict) {
			writeJSONError(w, http.StatusConflict, err.Error())
			return
		}
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	var build BuildResult
	if documentIsPublishable(updated, time.Now().UTC()) {
		build, err = s.Build()
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "saved but build failed: "+err.Error())
			return
		}
	}
	if err := s.gitChangeIfConfigured("Edit " + updated.Path); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "path": updated.Path, "source_sha256": fileSHA256(filepath.Join(s.SiteDir, "content", filepath.FromSlash(updated.Path))), "build": build})
}

func normalizeThemeLayoutPath(value string) (string, string, error) {
	value = strings.TrimPrefix(strings.TrimSpace(value), "/")
	value = strings.TrimPrefix(value, "themes/")
	parts := strings.Split(filepath.ToSlash(value), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] != "layout.html" {
		return "", "", errors.New("theme editor only saves a theme layout")
	}
	name, err := normalizeThemeName(parts[0])
	if err != nil {
		return "", "", err
	}
	return name, filepath.ToSlash(filepath.Join("themes", name, "layout.html")), nil
}

func stripThemePreview(source string) string {
	previewStyle := regexp.MustCompile(`(?is)<style[^>]*data-fileloom-preview[^>]*>.*?</style>\s*`)
	return previewStyle.ReplaceAllString(source, "")
}

func (s *Server) handleThemeSaveAPI(w http.ResponseWriter, r *http.Request, themeName, file string) {
	if themeName == "" {
		var err error
		themeName, file, err = normalizeThemeLayoutPath(file)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	name, err := normalizeThemeName(themeName)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if file == "" {
		file = filepath.ToSlash(filepath.Join("themes", name, "layout.html"))
	}
	_, file, err = normalizeThemeLayoutPath(file)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	contents := stripThemePreview(r.FormValue("html"))
	path := filepath.Join(s.SiteDir, filepath.FromSlash(file))
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	previous, _ := os.ReadFile(path)
	if baseSHA := strings.TrimSpace(r.FormValue("base_sha256")); baseSHA != "" && baseSHA != sourceSHA256(previous) {
		writeJSONError(w, http.StatusConflict, "theme layout changed since it was opened; reload before saving")
		return
	}
	if len(previous) > 0 {
		if err := s.recordWorkspaceRevision(file, previous, "Edit theme layout"); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := writeAtomicFile(path, []byte(contents+"\n")); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	build, err := s.Build()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "theme saved but build failed: "+err.Error())
		return
	}
	if err := s.gitChangeIfConfigured("Edit " + file); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "theme": name, "file": file, "source_sha256": sourceSHA256([]byte(contents + "\n")), "build": build})
}

var cssTokenPattern = regexp.MustCompile(`(?m)(--[A-Za-z0-9_-]+)\s*:\s*([^;}\r\n]+)`)

type cssTokenSpan struct {
	Name       string
	Value      string
	ValueStart int
	ValueEnd   int
}

func extractCSSTokens(source string) []cssTokenSpan {
	matches := cssTokenPattern.FindAllStringSubmatchIndex(source, -1)
	seen := map[string]bool{}
	var tokens []cssTokenSpan
	for _, match := range matches {
		if len(match) < 6 {
			continue
		}
		name := source[match[2]:match[3]]
		if seen[name] {
			continue
		}
		seen[name] = true
		valueStart := match[4]
		valueEnd := match[5]
		for valueStart < valueEnd && (source[valueStart] == ' ' || source[valueStart] == '\t') {
			valueStart++
		}
		for valueEnd > valueStart && (source[valueEnd-1] == ' ' || source[valueEnd-1] == '\t') {
			valueEnd--
		}
		tokens = append(tokens, cssTokenSpan{Name: name, Value: source[valueStart:valueEnd], ValueStart: valueStart, ValueEnd: valueEnd})
	}
	sort.Slice(tokens, func(i, j int) bool { return tokens[i].Name < tokens[j].Name })
	return tokens
}

func themeStylesPath(siteDir, theme string) (string, string, error) {
	name, err := normalizeThemeName(theme)
	if err != nil {
		return "", "", err
	}
	rel := filepath.ToSlash(filepath.Join("themes", name, "assets", "style.css"))
	filePath := filepath.Join(siteDir, filepath.FromSlash(rel))
	if !fileExists(filePath) {
		return "", "", errors.New("theme stylesheet not found")
	}
	return rel, filePath, nil
}

func validateCSSValue(value string) error {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 512 || strings.ContainsAny(value, "{};\r\n") || strings.Contains(strings.ToLower(value), "</style") {
		return errors.New("CSS token value is empty or contains forbidden characters")
	}
	return nil
}

func (s *Server) handleThemeTokensAPI(w http.ResponseWriter, r *http.Request) {
	theme := r.URL.Query().Get("theme")
	if r.Method == http.MethodPost {
		theme = r.FormValue("theme")
	}
	rel, filePath, err := themeStylesPath(s.SiteDir, theme)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	switch r.Method {
	case http.MethodGet:
		data, err := os.ReadFile(filePath)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		tokens := extractCSSTokens(string(data))
		result := make([]ThemeToken, 0, len(tokens))
		for _, token := range tokens {
			result = append(result, ThemeToken{Name: token.Name, Value: token.Value})
		}
		writeJSON(w, http.StatusOK, map[string]any{"theme": theme, "file": rel, "sha256": sourceSHA256(data), "tokens": result})
	case http.MethodPost:
		s.writeMu.Lock()
		defer s.writeMu.Unlock()
		var input struct {
			Theme  string            `json:"theme"`
			SHA256 string            `json:"sha256"`
			Tokens map[string]string `json:"tokens"`
		}
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
			input.Theme = r.FormValue("theme")
			input.SHA256 = r.FormValue("sha256")
			if err := json.Unmarshal([]byte(r.FormValue("tokens")), &input.Tokens); err != nil {
				writeJSONError(w, http.StatusBadRequest, "tokens must be a JSON object")
				return
			}
		}
		if input.Theme != "" && input.Theme != theme {
			writeJSONError(w, http.StatusBadRequest, "theme mismatch")
			return
		}
		data, err := os.ReadFile(filePath)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if input.SHA256 != "" && !strings.EqualFold(input.SHA256, sourceSHA256(data)) {
			writeJSONError(w, http.StatusConflict, "theme stylesheet changed since it was opened; reload before saving")
			return
		}
		spans := extractCSSTokens(string(data))
		spanByName := make(map[string]cssTokenSpan, len(spans))
		for _, span := range spans {
			spanByName[span.Name] = span
		}
		type replacement struct {
			start, end int
			value      string
		}
		var replacements []replacement
		for name, value := range input.Tokens {
			span, ok := spanByName[name]
			if !ok {
				writeJSONError(w, http.StatusBadRequest, "unknown CSS token: "+name)
				return
			}
			if err := validateCSSValue(value); err != nil {
				writeJSONError(w, http.StatusBadRequest, name+": "+err.Error())
				return
			}
			replacements = append(replacements, replacement{start: span.ValueStart, end: span.ValueEnd, value: strings.TrimSpace(value)})
		}
		if err := s.recordWorkspaceRevision(rel, data, "Edit theme tokens"); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		updated := string(data)
		sort.Slice(replacements, func(i, j int) bool { return replacements[i].start > replacements[j].start })
		for _, replacement := range replacements {
			updated = updated[:replacement.start] + replacement.value + updated[replacement.end:]
		}
		if err := writeAtomicFile(filePath, []byte(updated)); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		build, err := s.Build()
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "theme tokens saved but build failed: "+err.Error())
			return
		}
		if err := s.gitChangeIfConfigured("Edit theme tokens " + theme); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "theme": theme, "file": rel, "sha256": sourceSHA256([]byte(updated)), "build": build})
	default:
		methodNotAllowed(w)
	}
}

const (
	maxExportFiles = 10000
	maxExportBytes = 64 << 20
)

type limitedBuffer struct {
	buffer bytes.Buffer
	limit  int
}

func (w *limitedBuffer) Write(data []byte) (int, error) {
	if w.buffer.Len()+len(data) > w.limit {
		return 0, errors.New("export exceeds the size limit")
	}
	return w.buffer.Write(data)
}

func (s *Server) handleExportAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if _, err := s.Build(); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "build failed: "+err.Error())
		return
	}
	publicDir := filepath.Join(s.SiteDir, "public")
	buffer := &limitedBuffer{limit: maxExportBytes}
	archive := zip.NewWriter(buffer)
	files := 0
	err := filepath.WalkDir(publicDir, func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("refusing symlink in export: %s", filePath)
		}
		if entry.IsDir() {
			return nil
		}
		if files >= maxExportFiles {
			return errors.New("export contains too many files")
		}
		rel, err := filepath.Rel(publicDir, filePath)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		header.Name = rel
		header.Method = zip.Deflate
		writer, err := archive.CreateHeader(header)
		if err != nil {
			return err
		}
		input, err := os.Open(filePath)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(writer, input)
		closeErr := input.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		files++
		return nil
	})
	if err == nil {
		err = archive.Close()
	} else {
		_ = archive.Close()
	}
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "export failed: "+err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="fileloom-site-%s.zip"`, time.Now().UTC().Format("20060102-150405")))
	w.Header().Set("Content-Length", strconv.Itoa(buffer.buffer.Len()))
	_, _ = w.Write(buffer.buffer.Bytes())
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
	if themeName := strings.TrimSpace(r.URL.Query().Get("theme")); themeName != "" {
		s.handleThemeEditor(w, r, themeName)
		return
	}
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
			"base_sha256": fileSHA256(filepath.Join(s.SiteDir, "content", filepath.FromSlash(doc.Path))),
		},
	}
	pagesJSON, _ := json.Marshal(pages)
	pagesJSON = bytes.ReplaceAll(pagesJSON, []byte("<"), []byte(`\\u003c`))
	htmlSource := s.prepareVvvebEditor(string(source), pagesJSON, "visual editor")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, htmlSource)
}

func (s *Server) handleThemeEditor(w http.ResponseWriter, r *http.Request, themeName string) {
	name, err := normalizeThemeName(themeName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	layoutPath := filepath.Join(s.SiteDir, "themes", name, "layout.html")
	if !fileExists(layoutPath) {
		http.Error(w, "Theme layout not found", http.StatusNotFound)
		return
	}
	source, err := os.ReadFile(filepath.Join(s.WebDir, "vvvebjs", "editor.html"))
	if err != nil {
		http.Error(w, "VvvebJs editor is not installed", http.StatusNotFound)
		return
	}
	pageURL := "/_cms/editor/frame?theme=" + url.QueryEscape(name)
	pages := map[string]map[string]string{
		"current": {
			"name": "current", "file": filepath.ToSlash(filepath.Join("themes", name, "layout.html")),
			"url": pageURL, "title": friendlyTitle(name) + " theme", "theme": name,
			"base_sha256": fileSHA256(layoutPath),
		},
	}
	pagesJSON, _ := json.Marshal(pages)
	pagesJSON = bytes.ReplaceAll(pagesJSON, []byte("<"), []byte(`\\u003c`))
	htmlSource := s.prepareVvvebEditor(string(source), pagesJSON, friendlyTitle(name)+" theme")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, htmlSource)
}

func (s *Server) prepareVvvebEditor(source string, pagesJSON []byte, label string) string {
	htmlSource := source
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
	badge := `<style>#fileloom-editor-badge{position:fixed;left:16px;bottom:16px;z-index:9999;background:#7557ff;color:#fff;border-radius:999px;padding:7px 12px;font:700 11px/1 system-ui;letter-spacing:.1em;box-shadow:0 8px 20px #0002}#fileloom-editor-badge span{opacity:.7;font-weight:500;letter-spacing:0}</style><div id="fileloom-editor-badge">FILELOOM <span>` + html.EscapeString(label) + `</span></div>`
	htmlSource = strings.Replace(htmlSource, "</head>", badge+"</head>", 1)
	boot := `window.fileloomPages = ` + string(pagesJSON) + `;` + `
	window.fileloomBaseSHA = window.fileloomPages.current?.base_sha256 || "";
	const fileloomFetch = window.fetch.bind(window);
	window.fetch = function(input, init) {
		const url = typeof input === "string" ? input : input?.url || "";
		if (url.includes("/_cms/api/editor-save") && init?.body) {
			const body = new URLSearchParams(typeof init.body === "string" ? init.body : init.body.toString());
			if (window.fileloomBaseSHA) body.set("base_sha256", window.fileloomBaseSHA);
			init = {...init, body: body.toString()};
		}
		return fileloomFetch(input, init);
	};` + `
	let pages = window.fileloomPages || defaultPages;`
	htmlSource = strings.Replace(htmlSource, "let pages = defaultPages;", boot, 1)
	htmlSource = addMobileEditorShell(htmlSource)
	return htmlSource
}

func addMobileEditorShell(source string) string {
	mobileCSS := `<style>
@media (max-width:700px){
  html,body{overflow:hidden!important}
  #container{width:100vw!important;min-width:0!important}
  #container .sidebar{display:none!important}
  #container .main{width:100vw!important;margin:0!important;padding:0!important}
  #vvveb-builder{--builder-left-panel-width:0px;--builder-right-panel-width:0px;--builder-sidebar-width:0px;--builder-canvas-margin:0px;--builder-header-top-height:50px;--builder-bottom-panel-height:0px}
  #vvveb-builder #top-panel{height:50px;overflow:hidden;padding:0 5px;justify-content:flex-start;gap:4px}
  #vvveb-builder #top-panel>div:nth-child(2){display:none!important}
  #vvveb-builder #top-panel>div:first-child>.btn-group .btn:not(.menu-toggle){display:none!important}
  #vvveb-builder #top-panel>div:first-child>.btn-group .menu-toggle{display:inline-flex!important}
  #vvveb-builder #top-panel>div:last-child{margin-left:auto!important}
  #vvveb-builder #top-panel>div:last-child>.btn-group>div:first-child{display:none!important}
  #vvveb-builder #top-panel .save-btn{display:inline-flex!important}
  #vvveb-builder #top-panel .save-btn .button-text{font-size:11px!important}
  #vvveb-builder #left-panel,#vvveb-builder #right-panel{top:50px;bottom:44px;width:min(88vw,330px);max-width:330px;height:auto;z-index:2001;box-shadow:0 12px 40px #0003;display:none!important}
  #vvveb-builder.fileloom-mobile-left #left-panel{display:block!important;left:0}
  #vvveb-builder.fileloom-mobile-right #right-panel{display:block!important;right:0}
  #vvveb-builder #canvas{top:50px;bottom:44px;left:0;right:0;width:100vw!important;height:calc(100vh - 94px)!important;margin:0!important}
  #vvveb-builder #bottom-panel{display:none!important}
  #fileloom-mobile-scrim{display:none;position:fixed;inset:50px 0 44px;z-index:2000;background:rgba(16,20,30,.36)}
  #fileloom-mobile-scrim.open{display:block}
  #fileloom-mobile-bar{position:fixed;left:0;right:0;bottom:0;height:44px;z-index:3000;display:flex;align-items:stretch;background:#fff;border-top:1px solid #dfe3e9;box-shadow:0 -5px 18px #0001}
  #fileloom-mobile-bar button{flex:1;border:0;border-right:1px solid #edf0f4;background:#fff;color:#4f5b6b;font:700 10px/1 system-ui;letter-spacing:.02em}
  #fileloom-mobile-bar button:active,#fileloom-mobile-bar button.active{color:#5038c8;background:#f0edff}
  #fileloom-mobile-bar button span{display:block;font-size:16px;line-height:18px;margin-bottom:2px}
  #fileloom-editor-badge{bottom:52px!important;left:8px!important;font-size:9px!important;padding:6px 9px!important}
}
</style>`
	mobileUI := `<div id="fileloom-mobile-scrim"></div><div id="fileloom-mobile-bar" aria-label="Mobile editor controls"><button data-mobile-action="pages"><span>☷</span>Pages</button><button data-mobile-action="elements"><span>✚</span>Blocks</button><button data-mobile-action="style"><span>◌</span>Style</button><button data-mobile-action="preview"><span>◉</span>Preview</button><button data-mobile-action="save"><span>↥</span>Save</button></div><script>(function(){const builder=document.getElementById('vvveb-builder'),scrim=document.getElementById('fileloom-mobile-scrim'),bar=document.getElementById('fileloom-mobile-bar');if(!builder||!bar)return;function close(){builder.classList.remove('fileloom-mobile-left','fileloom-mobile-right');scrim.classList.remove('open');}function left(tab){close();builder.classList.add('fileloom-mobile-left');scrim.classList.add('open');document.querySelector(tab)?.click();}function right(tab){close();builder.classList.add('fileloom-mobile-right');scrim.classList.add('open');document.querySelector(tab)?.click();}bar.addEventListener('click',function(e){const button=e.target.closest('button');if(!button)return;const action=button.dataset.mobileAction;if(action==='pages')left('#pages-tab');if(action==='elements')left('#components-tab');if(action==='style')right('#configuration-tab');if(action==='preview'){close();document.querySelector('#preview-btn')?.click();}if(action==='save'){const save=document.querySelector('#top-panel .save-btn:not([disabled])')||document.querySelector('#top-panel .save-btn');save?.click();}});scrim.addEventListener('click',close);window.addEventListener('resize',function(){if(window.innerWidth>700)close();});})();</script>`
	return strings.Replace(source, "</body>", mobileCSS+mobileUI+"</body>", 1)
}
func (s *Server) handleEditorFrame(w http.ResponseWriter, r *http.Request) {
	if themeName := strings.TrimSpace(r.URL.Query().Get("theme")); themeName != "" {
		s.handleThemeFrame(w, r, themeName)
		return
	}
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

func (s *Server) handleThemeFrame(w http.ResponseWriter, r *http.Request, themeName string) {
	name, err := normalizeThemeName(themeName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	layout, err := os.ReadFile(filepath.Join(s.SiteDir, "themes", name, "layout.html"))
	if err != nil {
		http.Error(w, "Theme layout not found", http.StatusNotFound)
		return
	}
	css, _ := os.ReadFile(filepath.Join(s.SiteDir, "themes", name, "assets", "style.css"))
	cssText := strings.ReplaceAll(string(css), "</style>", "<\\/style>")
	frame := strings.Replace(string(layout), "</head>", `<style data-fileloom-preview>`+cssText+`</style></head>`, 1)
	if !strings.Contains(strings.ToLower(frame), "<html") {
		frame = `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><style data-fileloom-preview>` + cssText + `</style></head><body>` + frame + `</body></html>`
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, frame)
}
func (s *Server) handleMediaAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		s.handleMediaUploadAPI(w, r)
		return
	}
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
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

func (s *Server) handleMediaUploadAPI(w http.ResponseWriter, r *http.Request) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	r.Body = http.MaxBytesReader(w, r.Body, 16<<20)
	if err := r.ParseMultipartForm(16 << 20); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid upload: "+err.Error())
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "file is required")
		return
	}
	defer file.Close()
	name := filepath.Base(header.Filename)
	if name == "." || name == "" || !allowedMediaName(name) {
		writeJSONError(w, http.StatusBadRequest, "unsupported media filename")
		return
	}
	name = uniqueMediaName(filepath.Join(s.SiteDir, "media"), name)
	path := filepath.Join(s.SiteDir, "media", name)
	out, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o644)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_, copyErr := io.Copy(out, file)
	closeErr := out.Close()
	if copyErr != nil {
		writeJSONError(w, http.StatusInternalServerError, copyErr.Error())
		return
	}
	if closeErr != nil {
		writeJSONError(w, http.StatusInternalServerError, closeErr.Error())
		return
	}
	if _, err := s.Build(); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "media saved but build failed: "+err.Error())
		return
	}
	if err := s.gitChangeIfConfigured("Add media " + name); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "name": name, "path": name, "url": "/media/" + url.PathEscape(name)})
}

func allowedMediaName(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".avif", ".gif", ".jpeg", ".jpg", ".png", ".svg", ".webp", ".mp3", ".mp4", ".pdf", ".webm":
		return true
	default:
		return false
	}
}

func uniqueMediaName(root, name string) string {
	candidate := name
	base := strings.TrimSuffix(name, filepath.Ext(name))
	ext := filepath.Ext(name)
	for i := 2; fileExists(filepath.Join(root, candidate)); i++ {
		candidate = fmt.Sprintf("%s-%d%s", base, i, ext)
	}
	return candidate
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

func runGit(repo string, args ...string) (string, error) {
	commandArgs := append([]string{"-C", repo}, args...)
	command := exec.Command("git", commandArgs...)
	output, err := command.CombinedOutput()
	text := strings.TrimSpace(string(output))
	if err != nil {
		if text == "" {
			text = err.Error()
		}
		return text, fmt.Errorf("git %s: %s", strings.Join(args, " "), text)
	}
	return text, nil
}

func (s *Server) gitRepo() (string, error) {
	repo := filepath.Clean(s.SiteDir)
	gitDir := filepath.Join(repo, ".git")
	if _, err := os.Stat(gitDir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", errors.New("no Git repository at site/.git; initialize the site repository first")
		}
		return "", err
	}
	root, err := runGit(repo, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	rootAbs, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return "", err
	}
	repoAbs, err := filepath.Abs(repo)
	if err != nil {
		return "", err
	}
	if rootAbs != repoAbs {
		return "", errors.New("site Git repository resolves outside the site workspace")
	}
	return repo, nil
}

func (s *Server) gitScope(repo string) ([]string, error) {
	rel, err := filepath.Rel(repo, s.SiteDir)
	if err != nil || rel != "." {
		return nil, errors.New("site Git repository must be the site workspace")
	}
	return []string{".gitignore", "site.json", "content", "themes", "media"}, nil
}

func (s *Server) gitStatus(config GitConfig) GitStatus {
	status := GitStatus{}
	repo, err := s.gitRepo()
	if err != nil {
		status.Error = err.Error()
		return status
	}
	scope, err := s.gitScope(repo)
	if err != nil {
		status.Error = err.Error()
		return status
	}
	branch, _ := runGit(repo, "branch", "--show-current")
	remote := config.Remote
	if remote == "" {
		remote = "origin"
	}
	remoteURL, remoteErr := runGit(repo, "remote", "get-url", remote)
	if remoteErr != nil {
		remoteURL = ""
	}
	porcelain, err := runGit(repo, append([]string{"status", "--porcelain"}, append([]string{"--"}, scope...)...)...)
	if err != nil {
		status.Error = err.Error()
		return status
	}
	changes := 0
	if strings.TrimSpace(porcelain) != "" {
		changes = len(strings.Split(strings.TrimSpace(porcelain), "\n"))
	}
	status.Available = true
	status.Repo = repo
	status.Branch = branch
	status.RemoteName = remote
	status.Remote = remoteURL
	status.Changes = changes
	status.Clean = changes == 0
	return status
}

func (s *Server) gitCommitAndPush(config GitConfig, message string, push bool) (bool, error) {
	repo, err := s.gitRepo()
	if err != nil {
		return false, err
	}
	scope, err := s.gitScope(repo)
	if err != nil {
		return false, err
	}
	porcelain, err := runGit(repo, append([]string{"status", "--porcelain"}, append([]string{"--"}, scope...)...)...)
	if err != nil {
		return false, err
	}
	committed := false
	if strings.TrimSpace(porcelain) != "" {
		if _, err := runGit(repo, append([]string{"add", "--"}, scope...)...); err != nil {
			return false, err
		}
		message = strings.TrimSpace(message)
		if message == "" {
			message = "Update Fileloom site"
		}
		if len(message) > 160 {
			message = message[:160]
		}
		if _, err := runGit(repo, "commit", "-m", message); err != nil {
			return false, err
		}
		committed = true
	}
	if push || config.AutoPush {
		if err := s.gitPush(config); err != nil {
			return committed, err
		}
	}
	return committed, nil
}

func (s *Server) gitPush(config GitConfig) error {
	repo, err := s.gitRepo()
	if err != nil {
		return err
	}
	remote := config.Remote
	if remote == "" {
		remote = "origin"
	}
	branch := strings.TrimSpace(config.Branch)
	if branch == "" {
		branch, _ = runGit(repo, "branch", "--show-current")
	}
	if branch == "" {
		return errors.New("git branch is not configured")
	}
	_, err = runGit(repo, "push", remote, branch)
	return err
}

func (s *Server) gitChangeIfConfigured(message string) error {
	config, err := s.loadSiteConfig()
	if err != nil {
		return err
	}
	if !config.Git.Enabled || !config.Git.AutoCommit || strings.ToLower(config.Git.CommitOn) != "change" {
		return nil
	}
	_, err = s.gitCommitAndPush(config.Git, message, false)
	return err
}

func (s *Server) handleGitAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	config, err := s.loadSiteConfig()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"config": config.Git, "status": s.gitStatus(config.Git)})
}

func (s *Server) handleGitInitAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if _, err := os.Stat(filepath.Join(s.SiteDir, ".git")); err == nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "status": s.gitStatus(GitConfig{Remote: "origin"})})
		return
	} else if !errors.Is(err, os.ErrNotExist) {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if _, err := runGit(s.SiteDir, "init", "-b", "main"); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	config, err := s.loadSiteConfig()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "config": config.Git, "status": s.gitStatus(config.Git)})
}
func (s *Server) handleGitConfigAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := r.ParseForm(); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	config, err := s.loadSiteConfig()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	gitConfig := GitConfig{
		Enabled:    formBool(r, "enabled"),
		AutoCommit: formBool(r, "auto_commit"),
		AutoPush:   formBool(r, "auto_push"),
		CommitOn:   strings.ToLower(strings.TrimSpace(r.FormValue("commit_on"))),
		Remote:     strings.TrimSpace(r.FormValue("remote")),
		RemoteURL:  strings.TrimSpace(r.FormValue("remote_url")),
		Branch:     strings.TrimSpace(r.FormValue("branch")),
	}
	if gitConfig.CommitOn != "build" && gitConfig.CommitOn != "change" {
		writeJSONError(w, http.StatusBadRequest, "commit trigger must be build or change")
		return
	}
	if gitConfig.AutoCommit && !gitConfig.Enabled {
		writeJSONError(w, http.StatusBadRequest, "auto-commit requires Git automation")
		return
	}
	if gitConfig.AutoPush && (!gitConfig.Enabled || !gitConfig.AutoCommit) {
		writeJSONError(w, http.StatusBadRequest, "auto-push requires Git automation and auto-commit")
		return
	}
	if gitConfig.Remote == "" {
		gitConfig.Remote = "origin"
	}
	if !regexp.MustCompile(`^[A-Za-z0-9._-]+$`).MatchString(gitConfig.Remote) {
		writeJSONError(w, http.StatusBadRequest, "remote name contains invalid characters")
		return
	}
	if err := validateGitBranch(gitConfig.Branch); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateRemoteURL(gitConfig.RemoteURL); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if gitConfig.RemoteURL != "" {
		repo, err := s.gitRepo()
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "cannot configure a remote: "+err.Error())
			return
		}
		if _, remoteErr := runGit(repo, "remote", "get-url", gitConfig.Remote); remoteErr != nil {
			if _, addErr := runGit(repo, "remote", "add", gitConfig.Remote, gitConfig.RemoteURL); addErr != nil {
				writeJSONError(w, http.StatusBadRequest, addErr.Error())
				return
			}
		} else if _, setErr := runGit(repo, "remote", "set-url", gitConfig.Remote, gitConfig.RemoteURL); setErr != nil {
			writeJSONError(w, http.StatusBadRequest, setErr.Error())
			return
		}
	}
	config.Git = gitConfig
	if err := s.saveSiteConfig(config); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "config": config.Git, "status": s.gitStatus(config.Git)})
}

func validateGitBranch(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if strings.ContainsAny(value, "\r\n\t ") {
		return errors.New("branch name cannot contain whitespace")
	}
	command := exec.Command("git", "check-ref-format", "--branch", value)
	if output, err := command.CombinedOutput(); err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			message = "invalid Git branch name"
		}
		return errors.New(message)
	}
	return nil
}
func formBool(r *http.Request, key string) bool {
	value := strings.ToLower(strings.TrimSpace(r.FormValue(key)))
	return value == "1" || value == "true" || value == "on" || value == "yes"
}

func validateRemoteURL(value string) error {
	if value == "" {
		return nil
	}
	if strings.ContainsAny(value, "\r\n") {
		return errors.New("remote URL cannot contain newlines")
	}
	if strings.HasPrefix(value, "git@") && strings.Contains(value, ":") {
		return nil
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || (parsed.Host == "" && !(parsed.Scheme == "file" && parsed.Path != "")) {
		return errors.New("remote URL must be https://, ssh://, git://, file://, or git@host:path format")
	}
	if parsed.User != nil {
		return errors.New("remote URL must not contain embedded credentials")
	}
	switch parsed.Scheme {
	case "http", "https", "ssh", "git", "file":
		return nil
	default:
		return errors.New("unsupported remote URL scheme")
	}
}
func (s *Server) handleGitCommitAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := r.ParseForm(); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	config, err := s.loadSiteConfig()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	push := strings.EqualFold(r.FormValue("push"), "true")
	committed, err := s.gitCommitAndPush(config.Git, r.FormValue("message"), push)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "committed": committed, "status": s.gitStatus(config.Git)})
}

func (s *Server) handleGitPushAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	config, err := s.loadSiteConfig()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.gitPush(config.Git); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "status": s.gitStatus(config.Git)})
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Cache-Control", "no-store")
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
