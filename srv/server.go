package srv

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
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
	appName                    = "Fileloom"
	dateFormat                 = "2006-01-02"
	maxEditorSize              = 12 << 20
	maxFormSize                = 2 << 20
	maxUploadSize              = 16 << 20
	maxConcurrentCMSMutations  = 2
	maxRevisionsPerPath        = 100
	maxRevisionBytes           = 128 << 20
	maxExportFiles             = 10000
	maxExportBytes             = 64 << 20
	maxExportUncompressedBytes = 256 << 20
	maxExportFileBytes         = 64 << 20
	maxBuildAssetFileBytes     = 256 << 20
	maxGitCommandDuration      = 30 * time.Second
	maxGitPushDuration         = 2 * time.Minute
	maxHTTPReadHeaderDuration  = 10 * time.Second
	maxHTTPReadDuration        = 2 * time.Minute
	maxHTTPWriteDuration       = 5 * time.Minute
	maxHTTPIdleDuration        = 2 * time.Minute
)

type Server struct {
	SiteDir         string
	WebDir          string
	OwnerEmail      string
	BaseURLOverride string
	CMSCSP          string
	PublicCSP       string

	mu            sync.RWMutex
	publicMu      sync.RWMutex
	writeMu       sync.Mutex
	lastBuild     BuildResult
	scheduler     sync.Once
	mutationSlots chan struct{}
}

type SiteConfig struct {
	Title        string    `json:"title"`
	Description  string    `json:"description"`
	BaseURL      string    `json:"base_url"`
	Theme        string    `json:"theme"`
	EditorEngine string    `json:"editor_engine,omitempty"`
	Footer       string    `json:"footer"`
	Git          GitConfig `json:"git"`
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
var themeTokenPattern = regexp.MustCompile(`\{\{[^{}]*\}\}`)

const defaultCMSCSP = "default-src 'self'; base-uri 'self'; object-src 'none'; frame-ancestors 'self'; script-src 'self' 'unsafe-inline' 'unsafe-eval'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; font-src 'self' data:; connect-src 'self'; frame-src 'self' blob:; worker-src 'self' blob:; form-action 'self'"
const defaultPublicCSP = "default-src 'self'; base-uri 'self'; object-src 'none'; frame-ancestors 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; font-src 'self' data:; connect-src 'self'"
const defaultSiteJSON = `{
  "title": "A Fileloom site",
  "description": "An HTML-first static site made with Fileloom.",
  "base_url": "http://localhost:8000",
	"theme": "default",
  "editor_engine": "deckflow",
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
  <footer class="site-footer"><div class="shell">{{site.footer}} <span class="fileloom-attribution">Powered by <a href="https://github.com/jgbrwn/fileloom" rel="noreferrer">Fileloom</a> and <a href="https://github.com/deckflow/html-editor" rel="noreferrer">Deckflow</a>.</span></div></footer>
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

func configuredCSP(value, defaultProfile string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if strings.EqualFold(value, "1") || strings.EqualFold(value, "true") || strings.EqualFold(value, "default") {
		return defaultProfile
	}
	return value
}

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
		CMSCSP:          configuredCSP(os.Getenv("FILELOOM_CMS_CSP"), defaultCMSCSP),
		PublicCSP:       configuredCSP(os.Getenv("FILELOOM_PUBLIC_CSP"), defaultPublicCSP),
		mutationSlots:   make(chan struct{}, maxConcurrentCMSMutations),
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
	if err := rejectExistingSymlinkComponents(s.SiteDir, ""); err != nil {
		return fmt.Errorf("site root is not safe: %w", err)
	}
	for _, dir := range []string{
		filepath.Join(s.SiteDir, "content", "pages"),
		filepath.Join(s.SiteDir, "content", "posts"),
		filepath.Join(s.SiteDir, "media", "images"),
		filepath.Join(s.SiteDir, "themes", "default", "assets"),
		filepath.Join(s.SiteDir, "public"),
	} {
		rel, relErr := filepath.Rel(s.SiteDir, dir)
		if relErr != nil {
			return relErr
		}
		if err := rejectExistingSymlinkComponents(s.SiteDir, rel); err != nil {
			return fmt.Errorf("unsafe site path %s: %w", rel, err)
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
		if err := rejectSymlinkPath(s.SiteDir, rel); err != nil {
			return fmt.Errorf("unsafe site path %s: %w", rel, err)
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
	info, err := os.Lstat(path)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing symlink at %s", path)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("refusing non-regular file at %s", path)
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
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
		Title:        "A Fileloom site",
		Description:  "An HTML-first static site made with Fileloom.",
		BaseURL:      "http://localhost:8000",
		Theme:        "default",
		EditorEngine: "deckflow",
		Footer:       "Made with Fileloom.",
		Git:          GitConfig{CommitOn: "build", Remote: "origin"},
	}
	path, info, err := safeResolvedPath(s.SiteDir, "site.json")
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return config, nil
		}
		return config, err
	}
	if !info.Mode().IsRegular() {
		return config, errors.New("site.json must be a regular file")
	}
	data, err := os.ReadFile(path)
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
	normalizedTheme, err := normalizeThemeName(config.Theme)
	if err != nil {
		return config, fmt.Errorf("invalid configured theme: %w", err)
	}
	config.Theme = normalizedTheme
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
	rootInfo, err := os.Lstat(root)
	if err != nil {
		return nil, fmt.Errorf("content root is not accessible: %w", err)
	}
	if !rootInfo.IsDir() {
		return nil, errors.New("content root must be a directory")
	}
	var docs []Document
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("symlinks are not allowed in content: %s", path)
		}
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".html") {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("non-regular content file is not allowed: %s", path)
		}
		data, err := os.ReadFile(path)
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
	path, err := safeWorkspacePath(filepath.Join(s.SiteDir, "content"), rel)
	if err != nil {
		return err
	}
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

func normalizeWorkspaceSourcePath(value string) (string, error) {
	clean := strings.TrimPrefix(strings.TrimSpace(value), "/")
	if strings.HasPrefix(clean, "themes/") {
		_, normalized, err := normalizeThemeLayoutPath(clean)
		return normalized, err
	}
	return normalizeContentPath(clean)
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
	path, info, err := safeResolvedPath(filepath.Join(s.SiteDir, "content"), rel)
	if err != nil {
		return Document{}, err
	}
	if !info.Mode().IsRegular() {
		return Document{}, errors.New("content file must be regular")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Document{}, err
	}
	info, err = os.Stat(path)
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
	path, info, err := safeResolvedPath(filepath.Join(s.SiteDir, "content"), rel)
	if err != nil {
		return sourceDocument{}, err
	}
	if !info.Mode().IsRegular() {
		return sourceDocument{}, errors.New("content file must be regular")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return sourceDocument{}, err
	}
	info, err = os.Stat(path)
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
	info, err := os.Lstat(filePath)
	if err != nil || !info.Mode().IsRegular() {
		return ""
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		return ""
	}
	return sourceSHA256(data)
}

type sourceConflictError struct {
	Path        string
	ExpectedSHA string
	ActualSHA   string
}

func (e *sourceConflictError) Error() string {
	return fmt.Sprintf("%s changed since it was opened; reload before saving", e.Path)
}

type editorMetadataInput struct {
	Title     *string   `json:"title,omitempty"`
	Date      *string   `json:"date,omitempty"`
	Tags      *[]string `json:"tags,omitempty"`
	Category  *string   `json:"category,omitempty"`
	Excerpt   *string   `json:"excerpt,omitempty"`
	Status    *string   `json:"status,omitempty"`
	PublishAt *string   `json:"publish_at,omitempty"`
}

type editorSaveRequest struct {
	Resource string               `json:"resource,omitempty"`
	Path     string               `json:"path"`
	File     string               `json:"file"`
	HTML     string               `json:"html"`
	BaseSHA  string               `json:"base_sha256"`
	Engine   string               `json:"engine,omitempty"`
	Theme    string               `json:"theme,omitempty"`
	Metadata *editorMetadataInput `json:"metadata,omitempty"`
}

type editorPreviewRequest struct {
	Resource string               `json:"resource,omitempty"`
	Path     string               `json:"path"`
	Theme    string               `json:"theme,omitempty"`
	HTML     string               `json:"html"`
	Metadata *editorMetadataInput `json:"metadata,omitempty"`
}

type editorDocumentResponse struct {
	SchemaVersion int            `json:"schema_version"`
	Resource      string         `json:"resource"`
	Path          string         `json:"path"`
	Document      Document       `json:"document"`
	HTML          string         `json:"html"`
	SourceSHA256  string         `json:"source_sha256"`
	PreviewURL    string         `json:"preview_url"`
	PreviewAPI    string         `json:"preview_api"`
	PublicURL     string         `json:"public_url"`
	Theme         string         `json:"theme"`
	StylesheetCSS string         `json:"stylesheet_css,omitempty"`
	MediaAPI      string         `json:"media_api"`
	SaveAPI       string         `json:"save_api"`
	Editor        map[string]any `json:"editor"`
}

func editorTagsValue(tags []string) string {
	seen := map[string]bool{}
	clean := make([]string, 0, len(tags))
	for _, value := range tags {
		value = strings.TrimSpace(strings.Trim(value, "[]\\\"'"))
		if value == "" || seen[strings.ToLower(value)] {
			continue
		}
		seen[strings.ToLower(value)] = true
		clean = append(clean, value)
	}
	sort.Strings(clean)
	if len(clean) == 0 {
		return ""
	}
	return "[" + strings.Join(clean, ", ") + "]"
}

func editorMetadataUpdates(input *editorMetadataInput, current Document) (map[string]string, error) {
	if input == nil {
		return nil, nil
	}
	updates := map[string]string{}
	if input.Title != nil {
		title := strings.TrimSpace(*input.Title)
		if title == "" {
			return nil, errors.New("title is required")
		}
		updates["title"] = title
	}
	if input.Date != nil {
		updates["date"] = strings.TrimSpace(*input.Date)
	}
	if input.Tags != nil {
		updates["tags"] = editorTagsValue(*input.Tags)
	}
	if input.Category != nil {
		updates["category"] = strings.TrimSpace(*input.Category)
	}
	if input.Excerpt != nil {
		updates["excerpt"] = strings.TrimSpace(*input.Excerpt)
	}

	effectiveStatus := strings.ToLower(strings.TrimSpace(current.Status))
	if effectiveStatus == "" {
		effectiveStatus = "published"
	}
	effectivePublishAt := strings.TrimSpace(current.PublishAt)
	if input.Status != nil {
		effectiveStatus = strings.ToLower(strings.TrimSpace(*input.Status))
		if effectiveStatus != "draft" && effectiveStatus != "private" && effectiveStatus != "published" && effectiveStatus != "scheduled" {
			return nil, errors.New("status must be draft, private, published, or scheduled")
		}
		updates["status"] = effectiveStatus
	}
	if input.PublishAt != nil {
		publishAt, err := normalizePublishAt(*input.PublishAt)
		if err != nil {
			return nil, err
		}
		effectivePublishAt = publishAt
		updates["publish_at"] = publishAt
	}
	if input.Status != nil && effectiveStatus != "scheduled" {
		effectivePublishAt = ""
		updates["publish_at"] = ""
	}
	if effectiveStatus == "scheduled" && effectivePublishAt == "" {
		return nil, errors.New("publish_at is required for scheduled content")
	}
	if input.PublishAt != nil && effectiveStatus != "scheduled" && strings.TrimSpace(*input.PublishAt) != "" {
		return nil, errors.New("publish_at requires scheduled status")
	}
	return updates, nil
}

func applyEditorMetadata(doc Document, updates map[string]string) Document {
	if value, ok := updates["title"]; ok {
		doc.Title = strings.TrimSpace(value)
	}
	if value, ok := updates["date"]; ok {
		doc.Date = strings.TrimSpace(value)
	}
	if value, ok := updates["tags"]; ok {
		doc.Tags = parseTags(value)
	}
	if value, ok := updates["category"]; ok {
		doc.Category = strings.TrimSpace(value)
	}
	if value, ok := updates["excerpt"]; ok {
		doc.Excerpt = strings.TrimSpace(value)
	}
	if value, ok := updates["status"]; ok {
		doc.Status = strings.ToLower(strings.TrimSpace(value))
	}
	if value, ok := updates["publish_at"]; ok {
		doc.PublishAt = strings.TrimSpace(value)
	}
	doc.URL = documentURL(doc)
	return doc
}

func setETag(w http.ResponseWriter, sha string) {
	if sha = strings.TrimSpace(sha); sha != "" {
		w.Header().Set("ETag", `"`+sha+`"`)
	}
}

func requestPreconditionSHA(r *http.Request, field string) string {
	if value := strings.Trim(strings.TrimSpace(field), `"`); value != "" {
		return value
	}
	value := strings.TrimSpace(r.Header.Get("If-Match"))
	if strings.HasPrefix(value, "W/") {
		value = strings.TrimSpace(strings.TrimPrefix(value, "W/"))
	}
	if strings.Contains(value, ",") {
		return ""
	}
	return strings.Trim(value, `"`)
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
	sort.Slice(replacements, func(i, j int) bool {
		startI, _ := strconv.Atoi(replacements[i][0])
		startJ, _ := strconv.Atoi(replacements[j][0])
		return startI > startJ
	})
	for i := 0; i < len(replacements); i++ {
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
		if value == "" {
			continue
		}
		missing = append(missing, fmt.Sprintf("%s: %s\n", key, value))
	}
	if len(missing) > 0 && closingOffset >= 0 {
		sort.Strings(missing)
		addition := strings.Join(missing, "")
		insertAt := closingOffset + 1
		text = text[:insertAt] + addition + text[insertAt:]
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
	actualSHA := sourceSHA256(source.Source)
	if baseSHA != "" && !strings.EqualFold(strings.TrimSpace(baseSHA), actualSHA) {
		return Document{}, &sourceConflictError{Path: source.Document.Path, ExpectedSHA: strings.TrimSpace(baseSHA), ActualSHA: actualSHA}
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
	filePath, err := safeWorkspacePath(filepath.Join(s.SiteDir, "content"), path)
	if err != nil {
		return Document{}, err
	}
	if err := writeAtomicFile(filePath, patched); err != nil {
		return Document{}, fmt.Errorf("write %s: %w", path, err)
	}
	return s.loadDocument(path)
}

func (s *Server) writeContentSource(rel string, source []byte) error {
	rel, err := normalizeContentPath(rel)
	if err != nil {
		return err
	}
	path, err := safeWorkspacePath(filepath.Join(s.SiteDir, "content"), rel)
	if err != nil {
		return err
	}
	return writeAtomicFile(path, source)
}

func (s *Server) removeContentSource(rel string) error {
	rel, err := normalizeContentPath(rel)
	if err != nil {
		return err
	}
	path, err := safeWorkspacePath(filepath.Join(s.SiteDir, "content"), rel)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func revisionDirectory(siteDir, rel string) string {
	return filepath.Join(siteDir, ".fileloom", "revisions", filepath.FromSlash(rel))
}

func (s *Server) recordRevision(rel, reason string) error {
	rel, err := normalizeContentPath(rel)
	if err != nil {
		return err
	}
	filePath, _, err := safeResolvedPath(filepath.Join(s.SiteDir, "content"), rel)
	if err != nil {
		return err
	}
	source, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}
	return s.recordWorkspaceRevision(rel, source, reason)
}

func (s *Server) recordWorkspaceRevision(rel string, source []byte, reason string) error {
	revisionPath, err := safeRelativePath(rel)
	if err != nil {
		return errors.New("invalid revision path")
	}
	rel = revisionPath
	created := time.Now().UTC()
	id := created.Format("20060102T150405.000000000Z") + "-" + sourceSHA256(source)[:12]
	dir := filepath.Join(s.SiteDir, ".fileloom", "revisions", filepath.FromSlash(rel))
	if err := rejectExistingSymlinkComponents(s.SiteDir, filepath.ToSlash(filepath.Join(".fileloom", "revisions", rel))); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	revision := Revision{ID: id, Path: rel, CreatedAt: created.Format(time.RFC3339Nano), Reason: strings.TrimSpace(reason), SHA256: sourceSHA256(source), Size: int64(len(source))}
	if err := writeAtomicFile(filepath.Join(dir, id+".html"), source); err != nil {
		return err
	}
	metadata, err := json.MarshalIndent(revision, "", "  ")
	if err != nil {
		_ = os.Remove(filepath.Join(dir, id+".html"))
		return err
	}
	if err := writeAtomicFile(filepath.Join(dir, id+".json"), append(metadata, '\n')); err != nil {
		_ = os.Remove(filepath.Join(dir, id+".html"))
		return err
	}
	return s.pruneRevisions()
}

func (s *Server) pruneRevisions() error {
	root := filepath.Join(s.SiteDir, ".fileloom", "revisions")
	if err := rejectExistingSymlinkComponents(s.SiteDir, ".fileloom/revisions"); err != nil {
		return err
	}
	if _, err := os.Stat(root); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	type revisionFile struct {
		path      string
		id        string
		createdAt string
		size      int64
	}
	var files []revisionFile
	err := filepath.WalkDir(root, func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("symlink in revisions: %s", filePath)
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			return nil
		}
		jsonInfo, err := entry.Info()
		if err != nil {
			return err
		}
		if !jsonInfo.Mode().IsRegular() {
			return fmt.Errorf("non-regular revision metadata is not allowed: %s", filePath)
		}
		data, err := os.ReadFile(filePath)
		if err != nil {
			return err
		}
		var revision Revision
		if err := json.Unmarshal(data, &revision); err != nil || revision.ID == "" {
			return nil
		}
		htmlPath := strings.TrimSuffix(filePath, ".json") + ".html"
		htmlInfo, err := os.Lstat(htmlPath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		if !htmlInfo.Mode().IsRegular() {
			return fmt.Errorf("non-regular revision source is not allowed: %s", htmlPath)
		}
		files = append(files, revisionFile{path: filePath, id: revision.ID, createdAt: revision.CreatedAt, size: htmlInfo.Size() + int64(len(data))})
		return nil
	})
	if err != nil {
		return err
	}
	byPath := map[string][]revisionFile{}
	for _, file := range files {
		data, readErr := os.ReadFile(file.path)
		if readErr != nil {
			continue
		}
		var revision Revision
		if json.Unmarshal(data, &revision) == nil {
			byPath[revision.Path] = append(byPath[revision.Path], file)
		}
	}
	remove := map[string]bool{}
	for _, group := range byPath {
		sort.Slice(group, func(i, j int) bool { return group[i].createdAt > group[j].createdAt })
		for _, file := range group[maxInt(0, minInt(len(group), maxRevisionsPerPath)):] {
			remove[file.path] = true
		}
	}
	kept := files[:0]
	for _, file := range files {
		if remove[file.path] {
			_ = os.Remove(file.path)
			_ = os.Remove(strings.TrimSuffix(file.path, ".json") + ".html")
			continue
		}
		kept = append(kept, file)
	}
	total := int64(0)
	for _, file := range kept {
		total += file.size
	}
	sort.Slice(kept, func(i, j int) bool { return kept[i].createdAt < kept[j].createdAt })
	for _, file := range kept {
		if total <= maxRevisionBytes || len(kept) == 1 {
			break
		}
		total -= file.size
		_ = os.Remove(file.path)
		_ = os.Remove(strings.TrimSuffix(file.path, ".json") + ".html")
	}
	return nil
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (s *Server) listRevisions(rel string) ([]Revision, error) {
	rel, err := normalizeWorkspaceSourcePath(rel)
	if err != nil {
		return nil, err
	}
	dir := revisionDirectory(s.SiteDir, rel)
	if err := rejectExistingSymlinkComponents(s.SiteDir, filepath.ToSlash(filepath.Join(".fileloom", "revisions", rel))); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []Revision{}, nil
		}
		return nil, err
	}
	if err := rejectSymlinkPath(filepath.Join(s.SiteDir, ".fileloom", "revisions"), rel); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []Revision{}, nil
		}
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []Revision{}, nil
		}
		return nil, err
	}
	var revisions []Revision
	for _, entry := range entries {
		if entry.Type()&fs.ModeSymlink != 0 {
			return nil, errors.New("symlinks are not allowed in revisions")
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			return nil, errors.New("non-regular revision metadata is not allowed")
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
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
	rel, err := normalizeWorkspaceSourcePath(rel)
	if err != nil {
		return Document{}, err
	}
	if !regexp.MustCompile(`^[A-Za-z0-9_.-]+$`).MatchString(id) {
		return Document{}, errors.New("invalid revision id")
	}
	dir := revisionDirectory(s.SiteDir, rel)
	if err := rejectExistingSymlinkComponents(s.SiteDir, filepath.ToSlash(filepath.Join(".fileloom", "revisions", rel))); err != nil {
		return Document{}, err
	}
	if err := rejectSymlinkPath(filepath.Join(s.SiteDir, ".fileloom", "revisions"), rel); err != nil {
		return Document{}, err
	}
	metadataPath, metadataInfo, err := safeResolvedPath(dir, id+".json")
	if err != nil {
		return Document{}, err
	}
	if !metadataInfo.Mode().IsRegular() {
		return Document{}, errors.New("revision metadata must be regular")
	}
	metadata, err := os.ReadFile(metadataPath)
	if err != nil {
		return Document{}, err
	}
	var revision Revision
	if err := json.Unmarshal(metadata, &revision); err != nil {
		return Document{}, err
	}
	sourcePath, sourceInfo, err := safeResolvedPath(dir, id+".html")
	if err != nil {
		return Document{}, err
	}
	if !sourceInfo.Mode().IsRegular() {
		return Document{}, errors.New("revision source must be regular")
	}
	source, err := os.ReadFile(sourcePath)
	if err != nil {
		return Document{}, err
	}
	if sourceSHA256(source) != revision.SHA256 {
		return Document{}, errors.New("revision checksum mismatch")
	}
	if strings.HasPrefix(rel, "themes/") {
		if err := s.recordWorkspaceRevision(rel, source, "Before restore "+id); err != nil {
			return Document{}, err
		}
		filePath, err := safeWorkspacePath(s.SiteDir, rel)
		if err != nil {
			return Document{}, err
		}
		if err := writeAtomicFile(filePath, source); err != nil {
			return Document{}, err
		}
		parts := strings.Split(rel, "/")
		return Document{Path: rel, Type: "theme", Title: friendlyTitle(parts[1]), HTML: string(source)}, nil
	}
	if err := s.recordRevision(rel, "Before restore "+id); err != nil {
		return Document{}, err
	}
	filePath, err := safeWorkspacePath(filepath.Join(s.SiteDir, "content"), rel)
	if err != nil {
		return Document{}, err
	}
	if err := writeAtomicFile(filePath, source); err != nil {
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
	themeDir, themeInfo, err := safeResolvedPath(filepath.Join(s.SiteDir, "themes"), config.Theme)
	if err != nil || !themeInfo.IsDir() {
		if err != nil {
			return BuildResult{}, fmt.Errorf("theme %q is not available: %w", config.Theme, err)
		}
		return BuildResult{}, fmt.Errorf("theme %q is not a directory", config.Theme)
	}
	themeStylesheetURL := fmt.Sprintf("/theme/style.css?v=%d", time.Now().UnixNano())
	if err := copyDir(filepath.Join(themeDir, "assets"), filepath.Join(outputDir, "theme")); err != nil {
		return BuildResult{}, fmt.Errorf("copy theme assets: %w", err)
	}
	if err := copyDir(filepath.Join(s.SiteDir, "media"), filepath.Join(outputDir, "media")); err != nil {
		return BuildResult{}, fmt.Errorf("copy media: %w", err)
	}
	for _, asset := range []string{"fileloom-code.css", "fileloom-code.js"} {
		data, assetErr := readWebAsset(s.WebDir, asset)
		if errors.Is(assetErr, os.ErrNotExist) {
			continue
		}
		if assetErr != nil {
			return BuildResult{}, fmt.Errorf("read Fileloom code asset %s: %w", asset, assetErr)
		}
		if err := writePublic(filepath.Join(outputDir, "theme", asset), string(data)); err != nil {
			return BuildResult{}, err
		}
	}

	layout, err := readThemeTemplateSafe(themeDir, "layout.html", defaultLayoutTemplate)
	if err != nil {
		return BuildResult{}, fmt.Errorf("read theme layout: %w", err)
	}
	indexTemplate, err := readThemeTemplateSafe(themeDir, "index.html", defaultIndexTemplate)
	if err != nil {
		return BuildResult{}, fmt.Errorf("read theme index: %w", err)
	}
	postTemplate, err := readThemeTemplateSafe(themeDir, "post.html", defaultPostTemplate)
	if err != nil {
		return BuildResult{}, fmt.Errorf("read theme post: %w", err)
	}
	pageTemplate, err := readThemeTemplateSafe(themeDir, "page.html", defaultPageTemplate)
	if err != nil {
		return BuildResult{}, fmt.Errorf("read theme page: %w", err)
	}
	tagTemplate, err := readThemeTemplateSafe(themeDir, "tag.html", defaultTagTemplate)
	if err != nil {
		return BuildResult{}, fmt.Errorf("read theme tag: %w", err)
	}
	categoryTemplate, err := readThemeTemplateSafe(themeDir, "category.html", defaultCategoryTemplate)
	if err != nil {
		return BuildResult{}, fmt.Errorf("read theme category: %w", err)
	}
	archiveTemplate, err := readThemeTemplateSafe(themeDir, "archive.html", defaultArchiveTemplate)
	if err != nil {
		return BuildResult{}, fmt.Errorf("read theme archive: %w", err)
	}
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
	s.publicMu.Lock()
	err = swapPublicDirectory(outputDir, publicDir)
	s.publicMu.Unlock()
	if err != nil {
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
	rootInfo, err := os.Lstat(root)
	if err != nil {
		return nil, fmt.Errorf("themes root is not accessible: %w", err)
	}
	if !rootInfo.IsDir() {
		return nil, errors.New("themes root must be a directory")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("list themes: %w", err)
	}
	var themes []ThemeInfo
	for _, entry := range entries {
		if !entry.IsDir() || entry.Type()&fs.ModeSymlink != 0 {
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
		if themePath, themeInfo, themeErr := safeResolvedPath(root, filepath.ToSlash(filepath.Join(name, "theme.json"))); themeErr == nil && themeInfo.Mode().IsRegular() {
			if data, readErr := os.ReadFile(themePath); readErr == nil {
				_ = json.Unmarshal(data, &meta)
			}
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

func (s *Server) loadThemeLayout(themeName string) (string, string, []byte, []byte, error) {
	name, err := normalizeThemeName(themeName)
	if err != nil {
		return "", "", nil, nil, err
	}
	layoutRel := filepath.ToSlash(filepath.Join("themes", name, "layout.html"))
	layoutPath, info, err := safeResolvedPath(s.SiteDir, layoutRel)
	if err != nil || !info.Mode().IsRegular() {
		return "", "", nil, nil, errors.New("theme layout not found")
	}
	layout, err := os.ReadFile(layoutPath)
	if err != nil {
		return "", "", nil, nil, err
	}
	css := []byte{}
	if _, cssPath, cssErr := themeStylesPath(s.SiteDir, name); cssErr == nil {
		css, _ = os.ReadFile(cssPath)
	}
	return name, layoutRel, layout, css, nil
}

func normalizeThemeName(value string) (string, error) {
	if value == "" || value == "." || value == ".." || value != filepath.Base(value) {
		return "", errors.New("invalid theme name")
	}
	if slugify(value) != value {
		return "", errors.New("theme name must be a slug")
	}
	return value, nil
}

func fileExists(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular()
}

func (s *Server) saveSiteConfig(config SiteConfig) error {
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	path, err := safeWorkspacePath(s.SiteDir, "site.json")
	if err != nil {
		return err
	}
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
	if themeDir, info, err := safeResolvedPath(filepath.Join(s.SiteDir, "themes"), name); err != nil || !info.IsDir() {
		if err != nil {
			return fmt.Errorf("theme %q not found: %w", name, err)
		}
		return fmt.Errorf("theme %q is not a directory", name)
	} else {
		_ = themeDir
	}
	config, err := s.loadSiteConfig()
	if err != nil {
		return err
	}
	originalConfig := config
	config.Theme = name
	if err := s.saveSiteConfig(config); err != nil {
		return err
	}
	_, err = s.Build()
	if err != nil {
		rollbackErr := s.saveSiteConfig(originalConfig)
		if rollbackErr != nil {
			return fmt.Errorf("theme build failed: %v; config rollback failed: %w", err, rollbackErr)
		}
		return fmt.Errorf("theme activation rolled back because build failed: %w", err)
	}
	return s.gitChangeIfConfigured("Activate theme " + name)
}
func readWebAsset(webDir, name string) ([]byte, error) {
	path, info, err := safeResolvedPath(webDir, name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("web asset must be a regular file")
	}
	return os.ReadFile(path)
}
func readThemeTemplate(themeDir, name, fallback string) string {
	contents, err := readThemeTemplateSafe(themeDir, name, fallback)
	if err != nil {
		return fallback
	}
	return contents
}

func readThemeTemplateSafe(themeDir, name, fallback string) (string, error) {
	path, info, err := safeResolvedPath(themeDir, name)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fallback, nil
		}
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("theme template must be a regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
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
		"site.attribution": `<span class="fileloom-attribution">Powered by <a href="https://github.com/jgbrwn/fileloom" rel="noreferrer">Fileloom</a> and <a href="https://github.com/deckflow/html-editor" rel="noreferrer">Deckflow</a>.</span>`,
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

func (s *Server) renderEditorPreview(doc Document) (string, error) {
	config, err := s.loadSiteConfig()
	if err != nil {
		return "", err
	}
	docs, err := s.listDocuments()
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	var pages, posts []Document
	for _, candidate := range docs {
		if !documentIsPublishable(candidate, now) {
			continue
		}
		if candidate.Type == "post" {
			posts = append(posts, candidate)
		} else if candidate.Path != "pages/index.html" {
			pages = append(pages, candidate)
		}
	}
	sort.SliceStable(posts, func(i, j int) bool { return posts[i].Date > posts[j].Date })
	sort.SliceStable(pages, func(i, j int) bool { return pages[i].Path < pages[j].Path })
	themeDir, themeInfo, err := safeResolvedPath(filepath.Join(s.SiteDir, "themes"), config.Theme)
	if err != nil {
		return "", fmt.Errorf("theme %q is not available: %w", config.Theme, err)
	}
	if !themeInfo.IsDir() {
		return "", fmt.Errorf("theme %q is not a directory", config.Theme)
	}
	layout, err := readThemeTemplateSafe(themeDir, "layout.html", defaultLayoutTemplate)
	if err != nil {
		return "", fmt.Errorf("read theme layout: %w", err)
	}
	pageTemplate, err := readThemeTemplateSafe(themeDir, "page.html", defaultPageTemplate)
	if err != nil {
		return "", fmt.Errorf("read theme page: %w", err)
	}
	postTemplate, err := readThemeTemplateSafe(themeDir, "post.html", defaultPostTemplate)
	if err != nil {
		return "", fmt.Errorf("read theme post: %w", err)
	}
	navigation := navigationHTML(pages)
	postCards := postCardsHTML(posts)
	values := templateValues(doc, config, navigation, postCards)
	values["theme.css"] = "/theme/style.css"
	bodyTemplate := pageTemplate
	if doc.Type == "post" {
		bodyTemplate = postTemplate
	}
	values["content"] = doc.HTML
	values["content"] = applyTokens(bodyTemplate, values)
	output := applyTokens(layout, values)
	if fileExists(filepath.Join(s.WebDir, "fileloom-code.css")) && fileExists(filepath.Join(s.WebDir, "fileloom-code.js")) {
		output = ensureCodeAssets(output)
	}
	return ensureAttribution(output), nil
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
	rootInfo, err := os.Lstat(source)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("symlinks are not allowed in generated assets: %s", source)
	}
	if !rootInfo.IsDir() {
		return fmt.Errorf("asset root is not a directory: %s", source)
	}
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("symlinks are not allowed in generated assets: %s", path)
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if rel != "." && privateGeneratedPath(filepath.ToSlash(rel)) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(destination, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("non-regular asset is not allowed: %s", path)
		}
		if info.Size() > maxBuildAssetFileBytes {
			return fmt.Errorf("asset exceeds the per-file size limit: %s", path)
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			_ = in.Close()
			return err
		}
		out, err := os.Create(target)
		if err != nil {
			_ = in.Close()
			return err
		}
		_, copyErr := io.Copy(out, in)
		closeErr := out.Close()
		inputCloseErr := in.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		return inputCloseErr
	})
}

func privateGeneratedPath(rel string) bool {
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		if part == ".git" || part == ".fileloom" || part == ".env" || strings.HasPrefix(part, ".env.") || strings.HasPrefix(part, ".fileloom-") {
			return true
		}
	}
	return false
}

func codeAssetsAvailable(path string) bool {
	directory := filepath.Dir(path)
	for range 5 {
		if fileExists(filepath.Join(directory, "theme", "fileloom-code.css")) && fileExists(filepath.Join(directory, "theme", "fileloom-code.js")) {
			return true
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			break
		}
		directory = parent
	}
	return false
}
func ensureCodeAssets(source string) string {
	if !strings.Contains(source, "/theme/fileloom-code.css") {
		link := `<link rel="stylesheet" href="/theme/fileloom-code.css">`
		if index := strings.Index(strings.ToLower(source), "</head>"); index >= 0 {
			source = source[:index] + link + source[index:]
		} else {
			source = link + source
		}
	}
	if !strings.Contains(source, "/theme/fileloom-code.js") {
		script := `<script src="/theme/fileloom-code.js" defer></script>`
		if index := strings.Index(strings.ToLower(source), "</body>"); index >= 0 {
			source = source[:index] + script + source[index:]
		} else {
			source += script
		}
	}
	return source
}

func writePublic(path, contents string) error {
	if strings.EqualFold(filepath.Ext(path), ".html") {
		if codeAssetsAvailable(path) {
			contents = ensureCodeAssets(contents)
		}
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
	attribution := `<span class="fileloom-attribution">Powered by <a href="https://github.com/jgbrwn/fileloom" rel="noreferrer">Fileloom</a> and <a href="https://github.com/deckflow/html-editor" rel="noreferrer">Deckflow</a>.</span>`
	if index := strings.LastIndex(strings.ToLower(source), "</body>"); index >= 0 {
		return source[:index] + `<footer class="fileloom-generated-attribution" style="display:block;margin-top:8px;font-size:.8em">` + attribution + `</footer>` + source[index:]
	}
	return source + `<footer class="fileloom-generated-attribution" style="display:block;margin-top:8px;font-size:.8em">` + attribution + `</footer>`
}
func (s *Server) Serve(addr string) error {
	s.startScheduler()
	mux := s.Handler()
	slog.Info("starting Fileloom", "addr", addr, "site", s.SiteDir)
	server := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: maxHTTPReadHeaderDuration,
		ReadTimeout:       maxHTTPReadDuration,
		WriteTimeout:      maxHTTPWriteDuration,
		IdleTimeout:       maxHTTPIdleDuration,
		MaxHeaderBytes:    1 << 20,
	}
	return server.ListenAndServe()
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
	type scheduledChange struct {
		path   string
		source []byte
	}
	var changes []scheduledChange
	for _, doc := range docs {
		if strings.ToLower(strings.TrimSpace(doc.Status)) != "scheduled" || strings.TrimSpace(doc.PublishAt) == "" {
			continue
		}
		publishAt, err := time.Parse(time.RFC3339, strings.TrimSpace(doc.PublishAt))
		if err != nil || publishAt.After(now) {
			continue
		}
		source, err := s.loadSourceDocument(doc.Path)
		if err != nil {
			slog.Warn("scheduled publish source read failed", "path", doc.Path, "error", err)
			continue
		}
		if _, err := s.patchDocumentSource(doc.Path, "", false, map[string]string{"status": "published", "publish_at": ""}, "", "Publish scheduled "+doc.Path); err != nil {
			slog.Warn("scheduled publish failed", "path", doc.Path, "error", err)
			continue
		}
		changes = append(changes, scheduledChange{path: doc.Path, source: source.Source})
	}
	if len(changes) == 0 {
		return
	}
	if _, err := s.Build(); err != nil {
		slog.Warn("scheduled build failed; restoring scheduled sources", "error", err)
		for index := len(changes) - 1; index >= 0; index-- {
			change := changes[index]
			path, pathErr := safeWorkspacePath(filepath.Join(s.SiteDir, "content"), change.path)
			if pathErr != nil {
				slog.Warn("scheduled source restore path failed", "path", change.path, "error", pathErr)
				continue
			}
			if writeErr := writeAtomicFile(path, change.source); writeErr != nil {
				slog.Warn("scheduled source restore failed", "path", change.path, "error", writeErr)
			}
		}
		return
	}
	slog.Info("scheduled publishing completed", "published", len(changes))
}

func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(s.route)
}

type statusResponseWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusResponseWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusResponseWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(data)
}

func (w *statusResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func (s *Server) acquireMutationSlot() bool {
	if s.mutationSlots == nil {
		return true
	}
	select {
	case s.mutationSlots <- struct{}{}:
		return true
	default:
		return false
	}
}

func (s *Server) releaseMutationSlot() {
	if s.mutationSlots == nil {
		return
	}
	<-s.mutationSlots
}

func auditValue(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' || r == '\t' {
			return ' '
		}
		return r
	}, value)
	if len(value) > 200 {
		return value[:200] + "…"
	}
	return value
}

func (s *Server) auditMutation(r *http.Request, status int) {
	actor := "anonymous"
	values := r.Header.Values("X-ExeDev-Email")
	if len(values) == 1 && strings.TrimSpace(values[0]) != "" {
		actor = auditValue(values[0])
	} else if len(values) > 1 {
		actor = "multiple-identity-headers"
	}
	result := "success"
	if status >= 400 && status < 500 {
		result = "rejected"
	} else if status >= 500 {
		result = "failure"
	}
	level := slog.LevelInfo
	if result == "failure" {
		level = slog.LevelWarn
	}
	slog.LogAttrs(context.Background(), level, "cms mutation", slog.String("action", r.Method), slog.String("path", auditValue(r.URL.Path)), slog.String("actor", actor), slog.Int("status", status), slog.String("result", result))
}

func (s *Server) applySecurityHeaders(w http.ResponseWriter, r *http.Request) {
	setSecurityHeaders(w)
	if strings.HasPrefix(r.URL.Path, "/_cms") {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		w.Header().Add("Vary", "X-ExeDev-Email")
		if s.CMSCSP != "" {
			w.Header().Set("Content-Security-Policy", s.CMSCSP)
		}
		return
	}
	if s.PublicCSP != "" {
		w.Header().Set("Content-Security-Policy", s.PublicCSP)
	}
}

func setSecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
	w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
}

func setSVGSecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Security-Policy", "default-src 'none'; img-src 'self' data:; style-src 'unsafe-inline'")
	w.Header().Set("Content-Disposition", "inline")
}

func (s *Server) route(w http.ResponseWriter, r *http.Request) {
	s.applySecurityHeaders(w, r)
	if r.URL.Path == "/_cms" || r.URL.Path == "/_cms/" || strings.HasPrefix(r.URL.Path, "/_cms/") {
		if !s.cmsAllowed(r) {
			http.NotFound(w, r)
			return
		}
		if isMutationMethod(r.Method) && !s.sameOriginRequest(r) {
			http.NotFound(w, r)
			return
		}
		if isMutationMethod(r.Method) {
			if !s.acquireMutationSlot() {
				w.Header().Set("Retry-After", "1")
				http.Error(w, "too many CMS mutations in progress", http.StatusTooManyRequests)
				return
			}
			defer s.releaseMutationSlot()
			recorded := &statusResponseWriter{ResponseWriter: w}
			s.routeCMS(recorded, r)
			status := recorded.status
			if status == 0 {
				status = http.StatusOK
			}
			s.auditMutation(r, status)
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
	case strings.HasPrefix(r.URL.Path, "/_cms/media/"):
		s.serveFile(w, r, filepath.Join(s.SiteDir, "media"), "/_cms/media/")
	case strings.HasPrefix(r.URL.Path, "/_cms/api/"):
		s.handleAPI(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) serveWebFile(w http.ResponseWriter, r *http.Request, name string) {
	path, info, err := safeResolvedPath(s.WebDir, name)
	if err != nil || !info.Mode().IsRegular() {
		http.Error(w, "Fileloom web asset not found", http.StatusNotFound)
		return
	}
	setContentType(w, path)
	if strings.EqualFold(filepath.Ext(path), ".svg") {
		setSVGSecurityHeaders(w)
	}
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
	if privateGeneratedPath(rel) {
		http.NotFound(w, r)
		return
	}
	path, info, err := safeResolvedPath(root, rel)
	if err != nil || info.IsDir() || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	setContentType(w, path)
	if strings.EqualFold(filepath.Ext(path), ".svg") {
		setSVGSecurityHeaders(w)
	}
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

func rejectSymlinkPath(root, rel string) error {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	if err := rejectExistingSymlinkComponents(filepath.Dir(rootAbs), filepath.Base(rootAbs)); err != nil {
		return err
	}
	rootInfo, err := os.Lstat(rootAbs)
	if err != nil {
		return err
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 {
		return errors.New("root must not be a symlink")
	}
	clean, err := safeRelativePath(rel)
	if err != nil && rel != "" {
		return err
	}
	if clean == "." {
		clean = ""
	}
	current := rootAbs
	parts := []string{}
	if clean != "" {
		parts = strings.Split(filepath.FromSlash(clean), string(filepath.Separator))
	}
	for index, part := range parts {
		current = filepath.Join(current, part)
		info, statErr := os.Lstat(current)
		if statErr != nil {
			if errors.Is(statErr, os.ErrNotExist) && index == len(parts)-1 {
				return nil
			}
			return statErr
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlinks are not allowed: %s", current)
		}
	}
	return nil
}

func rejectExistingSymlinkComponents(root, rel string) error {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	rootInfo, err := os.Lstat(rootAbs)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 {
		return errors.New("root must not be a symlink")
	}
	clean, err := safeRelativePath(rel)
	if err != nil && rel != "" {
		return err
	}
	if clean == "." {
		clean = ""
	}
	current := rootAbs
	if clean == "" {
		return nil
	}
	for _, part := range strings.Split(filepath.FromSlash(clean), string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, statErr := os.Lstat(current)
		if errors.Is(statErr, os.ErrNotExist) {
			return nil
		}
		if statErr != nil {
			return statErr
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlinks are not allowed: %s", current)
		}
	}
	return nil
}
func safeWorkspacePath(root, rel string) (string, error) {
	clean, err := safeRelativePath(rel)
	if err != nil {
		return "", err
	}
	if err := rejectExistingSymlinkComponents(root, clean); err != nil {
		return "", err
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	return filepath.Join(rootAbs, filepath.FromSlash(clean)), nil
}

func safeResolvedPath(root, rel string) (string, fs.FileInfo, error) {
	clean, err := safeRelativePath(rel)
	if err != nil {
		return "", nil, err
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", nil, err
	}
	if err := rejectSymlinkPath(rootAbs, clean); err != nil {
		return "", nil, err
	}
	candidate := filepath.Join(rootAbs, filepath.FromSlash(clean))
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", nil, err
	}
	rootReal, err := filepath.EvalSymlinks(rootAbs)
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
	ownerView := s.cmsAllowed(r)
	var ownerDocument Document
	var hasOwnerDocument bool
	if ownerView {
		ownerDocument, hasOwnerDocument = s.publicDocument(r.URL.Path)
	}
	requested := strings.TrimPrefix(r.URL.Path, "/")
	candidates := []string{}
	if requested == "" {
		candidates = append(candidates, "index.html")
	} else if strings.HasSuffix(requested, "/") {
		candidates = append(candidates, requested+"index.html")
	} else {
		candidates = append(candidates, requested, requested+"/index.html", requested+".html")
	}
	publicDir := filepath.Join(s.SiteDir, "public")
	for _, candidate := range candidates {
		rel, err := safeRelativePath(candidate)
		if err != nil || privateGeneratedPath(rel) {
			continue
		}
		s.publicMu.RLock()
		path, info, err := safeResolvedPath(publicDir, rel)
		if err != nil || info.IsDir() || !info.Mode().IsRegular() {
			s.publicMu.RUnlock()
			continue
		}
		file, openErr := os.Open(path)
		s.publicMu.RUnlock()
		if openErr != nil {
			http.NotFound(w, r)
			return
		}
		setContentType(w, path)
		if strings.EqualFold(filepath.Ext(path), ".svg") {
			setSVGSecurityHeaders(w)
		}
		if ownerView && hasOwnerDocument {
			data, readErr := io.ReadAll(file)
			_ = file.Close()
			if readErr != nil {
				http.NotFound(w, r)
				return
			}
			data = injectOwnerEditToolbar(data, ownerDocument)
			w.Header().Set("Cache-Control", "private, no-store")
			w.Header().Add("Vary", "X-ExeDev-Email")
			http.ServeContent(w, r, filepath.Base(path), info.ModTime(), bytes.NewReader(data))
			return
		}
		http.ServeContent(w, r, filepath.Base(path), info.ModTime(), file)
		_ = file.Close()
		return
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

func (s *Server) sameOriginRequest(r *http.Request) bool {
	fetchSite := strings.ToLower(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site")))
	if fetchSite == "cross-site" {
		return false
	}
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	parsedOrigin := normalizedOrigin(origin)
	if parsedOrigin == "" {
		return false
	}
	allowed := map[string]bool{requestOrigin(r): true}
	if config, err := s.loadSiteConfig(); err == nil {
		if configured := normalizedOrigin(config.BaseURL); configured != "" {
			allowed[configured] = true
		}
	}
	return allowed[parsedOrigin]
}

func sameOriginRequest(r *http.Request) bool {
	fetchSite := strings.ToLower(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site")))
	if fetchSite == "cross-site" {
		return false
	}
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	return origin == "" || normalizedOrigin(origin) == requestOrigin(r)
}

func requestOrigin(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return normalizedOrigin(scheme + "://" + r.Host)
}

func normalizedOrigin(value string) string {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.Path != "" && parsed.Path != "/" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return ""
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return ""
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "" {
		return ""
	}
	port := parsed.Port()
	if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
		port = ""
	}
	if port != "" {
		host += ":" + port
	}
	return scheme + "://" + host
}

func (s *Server) cmsAllowed(r *http.Request) bool {
	owner := strings.TrimSpace(s.OwnerEmail)
	if owner == "" {
		return false
	}
	values := r.Header.Values("X-ExeDev-Email")
	if len(values) != 1 || strings.ContainsAny(values[0], "\r\n,") {
		return false
	}
	email := strings.TrimSpace(values[0])
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
	case "/_cms/api/editor":
		s.handleEditorAPI(w, r)
	case "/_cms/api/editor-save":
		s.handleEditorSaveAPI(w, r)
	case "/_cms/api/editor-preview":
		s.handleEditorPreviewAPI(w, r)
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

func normalizeEditorEngine(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" || value == "deckflow" {
		return "deckflow", nil
	}
	return "", errors.New("unsupported editor engine")
}

func (s *Server) editorEngineAvailable(engine string) bool {
	return engine == "deckflow" && fileExists(filepath.Join(s.WebDir, "editor-dist", "index.html"))
}

func (s *Server) resolveEditorEngine(r *http.Request, config SiteConfig) (string, error) {
	value := strings.TrimSpace(r.URL.Query().Get("engine"))
	if value == "" {
		value = config.EditorEngine
	}
	return normalizeEditorEngine(value)
}

func (s *Server) resolveThemeEditorEngine(r *http.Request, config SiteConfig) (string, error) {
	if strings.TrimSpace(r.URL.Query().Get("engine")) == "" {
		return "deckflow", nil
	}
	return s.resolveEditorEngine(r, config)
}

func (s *Server) handleEditorAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowedAllow(w, "GET, HEAD")
		return
	}
	if themeName := strings.TrimSpace(r.URL.Query().Get("theme")); themeName != "" {
		s.handleThemeEditorAPI(w, r, themeName)
		return
	}
	pathValue := r.URL.Query().Get("path")
	source, err := s.loadSourceDocument(pathValue)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	config, err := s.loadSiteConfig()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	engine, err := s.resolveEditorEngine(r, config)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	sha := sourceSHA256(source.Source)
	setETag(w, sha)
	response := editorDocumentResponse{
		SchemaVersion: 1,
		Resource:      "content",
		Path:          source.Document.Path,
		Document:      source.Document,
		HTML:          source.Document.HTML,
		SourceSHA256:  sha,
		PreviewURL:    "/_cms/editor/frame?path=" + url.QueryEscape(source.Document.Path),
		PreviewAPI:    "/_cms/api/editor-preview",
		PublicURL:     source.Document.URL,
		Theme:         config.Theme,
		MediaAPI:      "/_cms/api/media",
		SaveAPI:       "/_cms/api/editor-save",
		Editor: map[string]any{
			"selected": engine,
			"engines": []map[string]any{
				{"name": "deckflow", "available": s.editorEngineAvailable("deckflow")},
			},
		},
	}
	if _, cssPath, cssErr := themeStylesPath(s.SiteDir, config.Theme); cssErr == nil {
		if css, readErr := os.ReadFile(cssPath); readErr == nil {
			response.StylesheetCSS = string(css)
		}
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) handleThemeEditorAPI(w http.ResponseWriter, r *http.Request, themeName string) {
	name, layoutPath, layout, css, err := s.loadThemeLayout(themeName)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	config, err := s.loadSiteConfig()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	engine, err := s.resolveThemeEditorEngine(r, config)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if engine != "deckflow" {
		writeJSONError(w, http.StatusBadRequest, "Deckflow is required for theme-layout editing")
		return
	}
	sha := sourceSHA256(layout)
	setETag(w, sha)
	doc := Document{Path: layoutPath, Type: "theme", Title: friendlyTitle(name), HTML: string(layout)}
	response := editorDocumentResponse{
		SchemaVersion: 1,
		Resource:      "theme-layout",
		Path:          layoutPath,
		Document:      doc,
		HTML:          string(layout),
		SourceSHA256:  sha,
		PreviewURL:    "/_cms/editor/frame?theme=" + url.QueryEscape(name),
		PreviewAPI:    "/_cms/api/editor-preview",
		Theme:         name,
		StylesheetCSS: string(css),
		MediaAPI:      "/_cms/api/media",
		SaveAPI:       "/_cms/api/editor-save",
		Editor: map[string]any{
			"selected": engine,
			"engines": []map[string]any{
				{"name": "deckflow", "available": s.editorEngineAvailable("deckflow")},
			},
		},
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) renderThemeLayoutPreview(themeName, layout string) (string, error) {
	name, _, _, css, err := s.loadThemeLayout(themeName)
	if err != nil {
		return "", err
	}
	config, err := s.loadSiteConfig()
	if err != nil {
		return "", err
	}
	docs, err := s.listDocuments()
	if err != nil {
		return "", err
	}
	var pages, posts []Document
	for _, doc := range docs {
		if !documentIsPublishable(doc, time.Now().UTC()) {
			continue
		}
		if doc.Type == "post" {
			posts = append(posts, doc)
		} else if doc.Path != "pages/index.html" {
			pages = append(pages, doc)
		}
	}
	sort.SliceStable(pages, func(i, j int) bool { return pages[i].Path < pages[j].Path })
	sort.SliceStable(posts, func(i, j int) bool { return posts[i].Date > posts[j].Date })
	sample := Document{Type: "page", Title: friendlyTitle(name) + " theme", Slug: "theme-preview", Excerpt: "Theme layout preview", HTML: `<section class="fileloom-theme-preview"><h1>Theme layout preview</h1><p>This content is rendered in memory while editing the theme layout.</p></section>`, URL: "/"}
	values := templateValues(sample, config, navigationHTML(pages), postCardsHTML(posts))
	values["theme.css"] = "/theme/style.css"
	values["content"] = sample.HTML
	output := applyTokens(layout, values)
	if !strings.Contains(strings.ToLower(output), "<html") {
		output = `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"></head><body>` + output + `</body></html>`
	}
	cssText := strings.ReplaceAll(string(css), "</style>", "<\\/style>")
	if strings.Contains(strings.ToLower(output), "</head>") {
		output = strings.Replace(output, "</head>", `<style data-fileloom-preview>`+cssText+`</style></head>`, 1)
	} else {
		output = `<style data-fileloom-preview>` + cssText + `</style>` + output
	}
	return output, nil
}
func (s *Server) handleEditorPreviewAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if !rejectOversizedDeclaredBody(w, r, maxEditorSize) {
		return
	}
	limitRequestBody(w, r, maxEditorSize)
	var input editorPreviewRequest
	if err := decodeJSONBody(r, &input); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if input.Resource == "theme-layout" || strings.HasPrefix(strings.TrimPrefix(input.Path, "/"), "themes/") {
		themeName := input.Theme
		var err error
		if themeName == "" {
			themeName, _, err = normalizeThemeLayoutPath(input.Path)
			if err != nil {
				writeJSONError(w, http.StatusBadRequest, err.Error())
				return
			}
		}
		layout := input.HTML
		if strings.TrimSpace(layout) == "" {
			_, _, source, _, loadErr := s.loadThemeLayout(themeName)
			if loadErr != nil {
				writeJSONError(w, http.StatusBadRequest, loadErr.Error())
				return
			}
			layout = string(source)
		}
		preview, err := s.renderThemeLayoutPreview(themeName, layout)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, preview)
		return
	}
	doc, err := s.loadDocument(input.Path)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	updates, err := editorMetadataUpdates(input.Metadata, doc)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	doc = applyEditorMetadata(doc, updates)
	if strings.TrimSpace(input.HTML) != "" {
		doc.HTML = extractBodyHTML(input.HTML)
	}
	preview, err := s.renderEditorPreview(doc)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, preview)
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
	limitRequestBody(w, r, maxEditorSize)
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var input createItemInput
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		if err := decodeJSONBody(r, &input); err != nil {
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
			rollbackErr := s.removeContentSource(path)
			message := "item creation rolled back because build failed: " + err.Error()
			if rollbackErr != nil {
				message += "; rollback failed: " + rollbackErr.Error()
			}
			writeJSONError(w, http.StatusInternalServerError, message)
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
	limitRequestBody(w, r, maxFormSize)
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
	limitRequestBody(w, r, maxFormSize)
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
	original, err := s.loadSourceDocument(doc.Path)
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
		rollbackErr := s.writeContentSource(doc.Path, original.Source)
		message := "status change rolled back because build failed: " + err.Error()
		if rollbackErr != nil {
			message += "; rollback failed: " + rollbackErr.Error()
		}
		writeJSONError(w, http.StatusInternalServerError, message)
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
	limitRequestBody(w, r, maxFormSize)
	if err := r.ParseForm(); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.HasPrefix(strings.TrimPrefix(r.FormValue("path"), "/"), "themes/") {
		s.handleThemeRevisionRestoreAPI(w, r)
		return
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	targetPath := r.FormValue("path")
	original, err := s.loadSourceDocument(targetPath)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	baseSHA := requestPreconditionSHA(r, r.FormValue("base_sha256"))
	actualSHA := sourceSHA256(original.Source)
	if baseSHA != "" && !strings.EqualFold(baseSHA, actualSHA) {
		writeEditorConflict(w, &sourceConflictError{Path: original.Document.Path, ExpectedSHA: baseSHA, ActualSHA: actualSHA})
		return
	}
	doc, err := s.restoreRevision(targetPath, r.FormValue("id"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	build, err := s.Build()
	if err != nil {
		rollbackErr := s.writeContentSource(doc.Path, original.Source)
		message := "revision restore rolled back because build failed: " + err.Error()
		if rollbackErr != nil {
			message += "; rollback failed: " + rollbackErr.Error()
		}
		writeJSONError(w, http.StatusInternalServerError, message)
		return
	}
	if err := s.gitChangeIfConfigured("Restore " + doc.Path); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "path": doc.Path, "build": build, "source_sha256": fileSHA256(filepath.Join(s.SiteDir, "content", filepath.FromSlash(doc.Path)))})
}

func (s *Server) handleThemeRevisionRestoreAPI(w http.ResponseWriter, r *http.Request) {
	limitRequestBody(w, r, maxFormSize)
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := r.ParseForm(); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	rel, err := normalizeWorkspaceSourcePath(r.FormValue("path"))
	if err != nil || !strings.HasPrefix(rel, "themes/") {
		writeJSONError(w, http.StatusBadRequest, "theme layout path is required")
		return
	}
	path, info, err := safeResolvedPath(s.SiteDir, rel)
	if err != nil || !info.Mode().IsRegular() {
		writeJSONError(w, http.StatusBadRequest, "theme layout not found")
		return
	}
	current, err := os.ReadFile(path)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	baseSHA := requestPreconditionSHA(r, r.FormValue("base_sha256"))
	actualSHA := sourceSHA256(current)
	if baseSHA != "" && !strings.EqualFold(baseSHA, actualSHA) {
		writeEditorConflict(w, &sourceConflictError{Path: rel, ExpectedSHA: baseSHA, ActualSHA: actualSHA})
		return
	}
	doc, err := s.restoreRevision(rel, r.FormValue("id"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	build, err := s.Build()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "theme revision restore failed: "+err.Error())
		return
	}
	if err := s.gitChangeIfConfigured("Restore " + doc.Path); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	sha := fileSHA256(path)
	setETag(w, sha)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "resource": "theme-layout", "path": doc.Path, "source_sha256": sha, "build": build})
}

func (s *Server) handleBuildAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if !rejectOversizedDeclaredBody(w, r, maxFormSize) {
		return
	}
	limitRequestBody(w, r, maxFormSize)
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	result, err := s.Build()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "build": result})
}

func writeEditorConflict(w http.ResponseWriter, conflict *sourceConflictError) {
	setETag(w, conflict.ActualSHA)
	reloadURL := "/_cms/api/editor?path=" + url.QueryEscape(conflict.Path)
	if strings.HasPrefix(conflict.Path, "themes/") {
		if name, _, err := normalizeThemeLayoutPath(conflict.Path); err == nil {
			reloadURL = "/_cms/api/editor?theme=" + url.QueryEscape(name) + "&engine=deckflow"
		}
	}
	writeJSON(w, http.StatusConflict, map[string]any{
		"ok":              false,
		"code":            "source_conflict",
		"error":           conflict.Error(),
		"path":            conflict.Path,
		"expected_sha256": conflict.ExpectedSHA,
		"current_sha256":  conflict.ActualSHA,
		"reload_url":      reloadURL,
	})
}

func (s *Server) handleEditorSaveAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if !rejectOversizedDeclaredBody(w, r, maxEditorSize) {
		return
	}
	limitRequestBody(w, r, maxEditorSize)
	isJSON := strings.HasPrefix(strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type"))), "application/json")
	var input editorSaveRequest
	if isJSON {
		if err := decodeJSONBody(r, &input); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
	} else {
		if err := r.ParseForm(); err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		input = editorSaveRequest{
			Path: r.FormValue("path"), File: r.FormValue("file"), HTML: r.FormValue("html"),
			BaseSHA: r.FormValue("base_sha256"), Theme: r.FormValue("theme"), Engine: r.FormValue("engine"),
		}
	}
	if input.Path == "" {
		input.Path = input.File
	}
	if isJSON && (input.Resource == "theme-layout" || strings.HasPrefix(strings.TrimPrefix(input.Path, "/"), "themes/")) {
		s.handleThemeSaveJSONAPI(w, r, input)
		return
	}
	if !isJSON && (strings.TrimSpace(input.Theme) != "" || strings.HasPrefix(strings.TrimPrefix(input.File, "/"), "themes/")) {
		s.handleThemeSaveAPI(w, r, input.Theme, input.File)
		return
	}
	baseSHA := requestPreconditionSHA(r, input.BaseSHA)
	if isJSON && baseSHA == "" {
		writeJSON(w, http.StatusPreconditionRequired, map[string]any{
			"ok":    false,
			"code":  "precondition_required",
			"error": "base_sha256 or If-Match is required for JSON editor saves",
		})
		return
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	original, err := s.loadSourceDocument(input.Path)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	metadata, err := editorMetadataUpdates(input.Metadata, original.Document)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	updated, err := s.patchDocumentSource(input.Path, extractBodyHTML(input.HTML), true, metadata, baseSHA, "Edit "+input.Path)
	if err != nil {
		var conflict *sourceConflictError
		if errors.As(err, &conflict) {
			writeEditorConflict(w, conflict)
			return
		}
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	var build BuildResult
	if documentIsPublishable(original.Document, time.Now().UTC()) || documentIsPublishable(updated, time.Now().UTC()) {
		build, err = s.Build()
		if err != nil {
			rollbackErr := s.writeContentSource(updated.Path, original.Source)
			message := "edit rolled back because build failed: " + err.Error()
			if rollbackErr != nil {
				message += "; rollback failed: " + rollbackErr.Error()
			}
			writeJSONError(w, http.StatusInternalServerError, message)
			return
		}
	}
	if err := s.gitChangeIfConfigured("Edit " + updated.Path); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	sha := fileSHA256(filepath.Join(s.SiteDir, "content", filepath.FromSlash(updated.Path)))
	setETag(w, sha)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "path": updated.Path, "source_sha256": sha, "document": updated, "build": build})
}

func themeTemplatePlaceholders(source string) []string {
	values := themeTokenPattern.FindAllString(source, -1)
	sort.Strings(values)
	return values
}

func sameThemeTemplatePlaceholders(before, after string) bool {
	left := themeTemplatePlaceholders(before)
	right := themeTemplatePlaceholders(after)
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
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

func (s *Server) handleThemeSaveJSONAPI(w http.ResponseWriter, r *http.Request, input editorSaveRequest) {
	name, file, err := normalizeThemeLayoutPath(input.Path)
	if strings.TrimSpace(input.Theme) != "" {
		name, err = normalizeThemeName(input.Theme)
		file = filepath.ToSlash(filepath.Join("themes", name, "layout.html"))
	}
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	baseSHA := requestPreconditionSHA(r, input.BaseSHA)
	if baseSHA == "" {
		writeJSON(w, http.StatusPreconditionRequired, map[string]any{
			"ok":    false,
			"code":  "precondition_required",
			"error": "base_sha256 or If-Match is required for JSON theme saves",
		})
		return
	}
	path, info, err := safeResolvedPath(s.SiteDir, file)
	if err != nil || !info.Mode().IsRegular() {
		writeJSONError(w, http.StatusBadRequest, "theme layout not found")
		return
	}
	previous, err := os.ReadFile(path)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	actualSHA := sourceSHA256(previous)
	if !strings.EqualFold(baseSHA, actualSHA) {
		writeEditorConflict(w, &sourceConflictError{Path: file, ExpectedSHA: baseSHA, ActualSHA: actualSHA})
		return
	}
	if !sameThemeTemplatePlaceholders(string(previous), input.HTML) {
		writeJSONError(w, http.StatusBadRequest, "theme template placeholders cannot be added, removed, or changed")
		return
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.recordWorkspaceRevision(file, previous, "Edit theme layout"); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := writeAtomicFile(path, []byte(input.HTML)); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	build, err := s.Build()
	if err != nil {
		rollbackErr := writeAtomicFile(path, previous)
		message := "theme edit rolled back because build failed: " + err.Error()
		if rollbackErr != nil {
			message += "; rollback failed: " + rollbackErr.Error()
		}
		writeJSONError(w, http.StatusInternalServerError, message)
		return
	}
	if err := s.gitChangeIfConfigured("Edit " + file); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	sha := fileSHA256(path)
	setETag(w, sha)
	doc := Document{Path: file, Type: "theme", Title: friendlyTitle(name), HTML: input.HTML}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "resource": "theme-layout", "theme": name, "path": file,
		"source_sha256": sha, "html": input.HTML, "document": doc, "build": build,
	})
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
	path, err := safeWorkspacePath(s.SiteDir, file)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var previous []byte
	hadPrevious := false
	if info, statErr := os.Lstat(path); statErr == nil {
		if !info.Mode().IsRegular() {
			writeJSONError(w, http.StatusBadRequest, "theme layout must be a regular file")
			return
		}
		previous, err = os.ReadFile(path)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		hadPrevious = true
	} else if !errors.Is(statErr, os.ErrNotExist) {
		writeJSONError(w, http.StatusInternalServerError, statErr.Error())
		return
	}
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
		var rollbackErr error
		if hadPrevious {
			rollbackErr = writeAtomicFile(path, previous)
		} else {
			rollbackErr = os.Remove(path)
			if errors.Is(rollbackErr, os.ErrNotExist) {
				rollbackErr = nil
			}
		}
		message := "theme edit rolled back because build failed: " + err.Error()
		if rollbackErr != nil {
			message += "; rollback failed: " + rollbackErr.Error()
		}
		writeJSONError(w, http.StatusInternalServerError, message)
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
	filePath, info, err := safeResolvedPath(siteDir, rel)
	if err != nil || !info.Mode().IsRegular() {
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
	if r.Method == http.MethodPost {
		limitRequestBody(w, r, maxEditorSize)
	}
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
			if err := decodeJSONBody(r, &input); err != nil {
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
			rollbackErr := writeAtomicFile(filePath, data)
			message := "theme token edit rolled back because build failed: " + err.Error()
			if rollbackErr != nil {
				message += "; rollback failed: " + rollbackErr.Error()
			}
			writeJSONError(w, http.StatusInternalServerError, message)
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
	if r.Method != http.MethodPost {
		methodNotAllowedAllow(w, http.MethodPost)
		return
	}
	if !rejectOversizedDeclaredBody(w, r, maxFormSize) {
		return
	}
	limitRequestBody(w, r, maxFormSize)
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
	var uncompressedBytes int64
	s.publicMu.RLock()
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
		rel, err := filepath.Rel(publicDir, filePath)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if privateGeneratedPath(rel) {
			return nil
		}
		if files >= maxExportFiles {
			return errors.New("export contains too many files")
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("refusing non-regular file in export: %s", filePath)
		}
		if info.Size() > maxExportFileBytes || uncompressedBytes > maxExportUncompressedBytes-info.Size() {
			return errors.New("export exceeds the uncompressed size limit")
		}
		uncompressedBytes += info.Size()
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
		_, copyErr := io.Copy(writer, io.LimitReader(input, maxExportFileBytes+1))
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
	s.publicMu.RUnlock()
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
	themeMode := strings.TrimSpace(r.URL.Query().Get("theme")) != ""
	if !themeMode && strings.TrimSpace(r.URL.Query().Get("path")) == "" {
		http.Redirect(w, r, "/_cms/", http.StatusFound)
		return
	}
	config, err := s.loadSiteConfig()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var engine string
	if themeMode {
		engine, err = s.resolveThemeEditorEngine(r, config)
	} else {
		engine, err = s.resolveEditorEngine(r, config)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !s.editorEngineAvailable(engine) {
		http.Error(w, "Deckflow editor is not installed", http.StatusNotImplemented)
		return
	}
	s.serveWebFile(w, r, filepath.ToSlash(filepath.Join("editor-dist", "index.html")))
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
	_, cssPath, err := themeStylesPath(s.SiteDir, config.Theme)
	if err != nil {
		http.Error(w, "Theme stylesheet not found", http.StatusNotFound)
		return
	}
	css, _ := os.ReadFile(cssPath)
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
	layoutPath, layoutInfo, err := safeResolvedPath(s.SiteDir, filepath.ToSlash(filepath.Join("themes", name, "layout.html")))
	if err != nil || !layoutInfo.Mode().IsRegular() {
		http.Error(w, "Theme layout not found", http.StatusNotFound)
		return
	}
	cssPath, cssInfo, cssErr := safeResolvedPath(s.SiteDir, filepath.ToSlash(filepath.Join("themes", name, "assets", "style.css")))
	if cssErr != nil || !cssInfo.Mode().IsRegular() {
		cssPath = ""
	}
	layout, err := os.ReadFile(layoutPath)
	if err != nil {
		http.Error(w, "Theme layout not found", http.StatusNotFound)
		return
	}
	var css []byte
	if cssPath != "" {
		css, _ = os.ReadFile(cssPath)
	}
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
	limitRequestBody(w, r, maxUploadSize)
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := r.ParseMultipartForm(maxUploadSize); err != nil {
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
	root := filepath.Join(s.SiteDir, "media")
	if err := rejectSymlinkPath(root, ""); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "media root is not safe: "+err.Error())
		return
	}
	mediaRelative, err := normalizeMediaDirectory(r.FormValue("mediaPath"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if mediaRelative != "" {
		if err := rejectSymlinkPath(root, mediaRelative); err != nil {
			writeJSONError(w, http.StatusBadRequest, "media directory is not safe: "+err.Error())
			return
		}
	}
	mediaDir := filepath.Join(root, filepath.FromSlash(mediaRelative))
	if err := os.MkdirAll(mediaDir, 0o755); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	name = uniqueMediaName(mediaDir, name)
	path := filepath.Join(mediaDir, name)
	uploadTempDir := filepath.Join(s.SiteDir, ".fileloom", "uploads")
	if err := rejectExistingSymlinkComponents(s.SiteDir, ".fileloom/uploads"); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "upload staging path is not safe: "+err.Error())
		return
	}
	if err := os.MkdirAll(uploadTempDir, 0o755); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	temp, err := os.CreateTemp(uploadTempDir, ".fileloom-upload-*.tmp")
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if _, err := io.Copy(temp, file); err != nil {
		_ = temp.Close()
		writeJSONError(w, http.StatusBadRequest, "upload failed: "+err.Error())
		return
	}
	if err := temp.Close(); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if _, err := io.Copy(io.Discard, r.Body); err != nil {
		writeJSONError(w, http.StatusRequestEntityTooLarge, "upload exceeds the size limit")
		return
	}
	if strings.EqualFold(filepath.Ext(name), ".svg") {
		data, err := os.ReadFile(tempName)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		sanitized, err := sanitizeSVG(data)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "unsafe SVG: "+err.Error())
			return
		}
		if err := writeAtomicFile(tempName, sanitized); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if err := os.Rename(tempName, path); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if _, err := s.Build(); err != nil {
		rollbackErr := os.Remove(path)
		if errors.Is(rollbackErr, os.ErrNotExist) {
			rollbackErr = nil
		}
		message := "media upload rolled back because build failed: " + err.Error()
		if rollbackErr != nil {
			message += "; rollback failed: " + rollbackErr.Error()
		}
		writeJSONError(w, http.StatusInternalServerError, message)
		return
	}
	if err := s.gitChangeIfConfigured("Add media " + name); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "name": name, "path": filepath.ToSlash(filepath.Join(mediaRelative, name)), "url": mediaURLPath(mediaRelative, name)})
}

const svgNamespace = "http://www.w3.org/2000/svg"

var allowedSVGElements = map[string]string{
	"svg":            "svg",
	"g":              "g",
	"defs":           "defs",
	"path":           "path",
	"rect":           "rect",
	"circle":         "circle",
	"ellipse":        "ellipse",
	"line":           "line",
	"polyline":       "polyline",
	"polygon":        "polygon",
	"lineargradient": "linearGradient",
	"radialgradient": "radialGradient",
	"stop":           "stop",
	"clippath":       "clipPath",
	"mask":           "mask",
	"pattern":        "pattern",
	"use":            "use",
	"text":           "text",
	"tspan":          "tspan",
	"title":          "title",
	"desc":           "desc",
}

var allowedSVGAttributes = map[string]bool{
	"xmlns": true, "viewbox": true, "width": true, "height": true, "x": true, "y": true,
	"x1": true, "x2": true, "y1": true, "y2": true, "cx": true, "cy": true, "r": true,
	"rx": true, "ry": true, "d": true, "points": true, "fill": true, "stroke": true,
	"stroke-width": true, "stroke-linecap": true, "stroke-linejoin": true, "stroke-miterlimit": true,
	"fill-rule": true, "clip-rule": true, "fill-opacity": true, "stroke-opacity": true,
	"opacity": true, "transform": true, "preserveaspectratio": true, "gradientunits": true,
	"gradienttransform": true, "offset": true, "stop-color": true, "stop-opacity": true,
	"patternunits": true, "patterncontentunits": true, "patterntransform": true,
	"clippathunits": true, "maskunits": true, "maskcontentunits": true, "id": true,
	"class": true, "href": true,
	"font-family": true, "font-size": true, "font-weight": true, "text-anchor": true, "dominant-baseline": true,
}

func sanitizeSVG(data []byte) ([]byte, error) {
	data = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
	decoder := xml.NewDecoder(bytes.NewReader(data))
	decoder.Strict = true
	var output bytes.Buffer
	encoder := xml.NewEncoder(&output)
	stack := []string{}
	rootSeen := false
	skipDepth := 0
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, errors.New("SVG is not well-formed XML")
		}
		switch token := token.(type) {
		case xml.StartElement:
			name := strings.ToLower(token.Name.Local)
			if token.Name.Space != "" && token.Name.Space != svgNamespace {
				if !rootSeen {
					return nil, errors.New("SVG uses an unsupported namespace")
				}
				skipDepth = 1
				continue
			}
			if skipDepth > 0 {
				skipDepth++
				continue
			}
			if !rootSeen {
				if name != "svg" {
					return nil, errors.New("SVG root element is required")
				}
				rootSeen = true
			} else if len(stack) == 0 {
				return nil, errors.New("SVG contains content after its root element")
			}
			canonical, allowed := allowedSVGElements[name]
			if !allowed || (name == "svg" && len(stack) > 0) {
				skipDepth = 1
				continue
			}
			attrs := sanitizeSVGAttributes(token.Attr, name)
			if name == "svg" {
				attrs = append([]xml.Attr{{Name: xml.Name{Local: "xmlns"}, Value: svgNamespace}}, attrs...)
			}
			if err := encoder.EncodeToken(xml.StartElement{Name: xml.Name{Local: canonical}, Attr: attrs}); err != nil {
				return nil, err
			}
			stack = append(stack, canonical)
		case xml.EndElement:
			if skipDepth > 0 {
				skipDepth--
				continue
			}
			if len(stack) == 0 {
				return nil, errors.New("SVG has an unexpected closing element")
			}
			last := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if err := encoder.EncodeToken(xml.EndElement{Name: xml.Name{Local: last}}); err != nil {
				return nil, err
			}
		case xml.CharData:
			if skipDepth > 0 {
				continue
			}
			if len(stack) > 0 {
				if err := encoder.EncodeToken(token); err != nil {
					return nil, err
				}
			} else if strings.TrimSpace(string(token)) != "" {
				return nil, errors.New("SVG contains text outside its root element")
			}
		case xml.Directive:
			return nil, errors.New("SVG directives are not allowed")
		case xml.ProcInst:
			// Drop XML declarations and processing instructions.
		case xml.Comment:
			// Drop comments so hidden markup cannot be carried into the upload.
		}
	}
	if !rootSeen || len(stack) != 0 || skipDepth != 0 {
		return nil, errors.New("SVG root element is required")
	}
	if err := encoder.Flush(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func sanitizeSVGAttributes(attributes []xml.Attr, element string) []xml.Attr {
	result := make([]xml.Attr, 0, len(attributes))
	seen := map[string]bool{}
	for _, attribute := range attributes {
		name := strings.ToLower(attribute.Name.Local)
		if attribute.Name.Space != "" && name != "href" {
			continue
		}
		if name == "xlink:href" {
			name = "href"
		}
		if name == "xmlns" {
			continue
		}
		if name == "onload" || strings.HasPrefix(name, "on") || !allowedSVGAttributes[name] || seen[name] {
			continue
		}
		value := strings.TrimSpace(attribute.Value)
		if !safeSVGAttributeValue(name, value) {
			continue
		}
		seen[name] = true
		canonical := name
		switch name {
		case "viewbox":
			canonical = "viewBox"
		case "preserveaspectratio":
			canonical = "preserveAspectRatio"
		case "gradientunits":
			canonical = "gradientUnits"
		case "gradienttransform":
			canonical = "gradientTransform"
		case "patternunits":
			canonical = "patternUnits"
		case "patterncontentunits":
			canonical = "patternContentUnits"
		case "patterntransform":
			canonical = "patternTransform"
		case "clippathunits":
			canonical = "clipPathUnits"
		case "maskunits":
			canonical = "maskUnits"
		case "maskcontentunits":
			canonical = "maskContentUnits"
		case "stop-color":
			canonical = "stop-color"
		case "stop-opacity":
			canonical = "stop-opacity"
		}
		result = append(result, xml.Attr{Name: xml.Name{Local: canonical}, Value: value})
	}
	return result
}

func asciiLower(value string) string {
	data := []byte(value)
	for index, char := range data {
		if char >= 'A' && char <= 'Z' {
			data[index] = char + ('a' - 'A')
		}
	}
	return string(data)
}

func safeSVGAttributeValue(name, value string) bool {
	if strings.ContainsAny(value, "\x00\r\n\\") {
		return false
	}
	lower := asciiLower(value)
	for _, blocked := range []string{"javascript:", "vbscript:", "expression(", "@import", "-moz-binding", "behavior:", "<script", "</script"} {
		if strings.Contains(lower, blocked) {
			return false
		}
	}
	if name == "href" && !strings.HasPrefix(value, "#") {
		return false
	}
	for cursor := 0; ; {
		index := strings.Index(lower[cursor:], "url(")
		if index < 0 {
			break
		}
		index += cursor
		end := strings.IndexByte(lower[index+4:], ')')
		if end < 0 {
			return false
		}
		inside := strings.TrimSpace(value[index+4 : index+4+end])
		if !strings.HasPrefix(inside, "#") || strings.ContainsAny(inside, "'\"<>\\/") {
			return false
		}
		cursor = index + 5 + end
	}
	return true
}
func normalizeMediaDirectory(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || value == "/media" || value == "media" || value == "/media/" || value == "media/" {
		return "", nil
	}
	value = strings.TrimPrefix(value, "/")
	if !strings.HasPrefix(value, "media/") {
		return "", errors.New("media directory must be under /media")
	}
	value = strings.TrimPrefix(value, "media/")
	clean, err := safeRelativePath(value)
	if err != nil || privateGeneratedPath(clean) {
		return "", errors.New("invalid media directory")
	}
	return clean, nil
}

func mediaURLPath(directory, name string) string {
	parts := []string{"/media"}
	if directory != "" {
		for _, part := range strings.Split(filepath.ToSlash(directory), "/") {
			parts = append(parts, url.PathEscape(part))
		}
	}
	parts = append(parts, url.PathEscape(name))
	return strings.Join(parts, "/")
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
	rootInfo, err := os.Lstat(root)
	if err != nil {
		return nil, err
	}
	if !rootInfo.IsDir() {
		return nil, errors.New("media root must be a directory")
	}
	var files []map[string]any
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("symlinks are not allowed in media: %s", path)
		}
		if entry.IsDir() || path == root {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("non-regular media file is not allowed: %s", path)
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		fileDirectory := filepath.ToSlash(filepath.Dir(filepath.FromSlash(rel)))
		if fileDirectory == "." {
			fileDirectory = ""
		}
		files = append(files, map[string]any{
			"name": filepath.Base(rel), "type": "file", "path": rel,
			"url": mediaURLPath(fileDirectory, filepath.Base(rel)), "size": info.Size(),
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
	return runGitWithTimeout(maxGitCommandDuration, repo, args...)
}

func runGitWithTimeout(timeout time.Duration, repo string, args ...string) (string, error) {
	commandArgs := append([]string{"-C", repo}, args...)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	command := exec.CommandContext(ctx, "git", commandArgs...)
	command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_NOSYSTEM=1")
	output, err := command.CombinedOutput()
	text := strings.TrimSpace(string(output))
	if ctx.Err() != nil {
		return text, fmt.Errorf("git %s timed out after %s", strings.Join(args, " "), timeout)
	}
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
	gitInfo, err := os.Lstat(gitDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", errors.New("no Git repository at site/.git; initialize the site repository first")
		}
		return "", err
	}
	if gitInfo.Mode()&os.ModeSymlink != 0 || !gitInfo.IsDir() {
		return "", errors.New("site/.git must be a real directory inside the site workspace")
	}
	if err := rejectExistingSymlinkComponents(repo, ".git"); err != nil {
		return "", err
	}
	root, err := runGit(repo, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	rootAbs, err := filepath.EvalSymlinks(filepath.Clean(root))
	if err != nil {
		return "", err
	}
	repoAbs, err := filepath.EvalSymlinks(repo)
	if err != nil {
		return "", err
	}
	if rootAbs != repoAbs {
		return "", errors.New("site Git repository resolves outside the site workspace")
	}
	return repo, nil
}

func validGitRemoteName(value string) bool {
	return value != "" && value != "." && value != ".." && !strings.HasPrefix(value, "-") && !strings.Contains(value, "..") && regexp.MustCompile(`^[A-Za-z0-9._-]+$`).MatchString(value)
}

func gitRemoteURLs(repo, remote string, push bool) ([]string, error) {
	args := []string{"remote", "get-url"}
	if push {
		args = append(args, "--push")
	}
	args = append(args, "--all", remote)
	output, err := runGit(repo, args...)
	if err != nil {
		return nil, err
	}
	var urls []string
	for _, line := range strings.Split(output, "\n") {
		if value := strings.TrimSpace(line); value != "" {
			urls = append(urls, value)
		}
	}
	if len(urls) == 0 {
		return nil, errors.New("Git remote has no URL")
	}
	return urls, nil
}

func (s *Server) gitRemoteURL(repo, remote string, unattended bool) (string, error) {
	if remote == "" {
		remote = "origin"
	}
	if !validGitRemoteName(remote) {
		return "", errors.New("remote name contains invalid characters")
	}
	fetchURLs, err := gitRemoteURLs(repo, remote, false)
	if err != nil {
		return "", err
	}
	for _, remoteURL := range fetchURLs {
		if err := validateRemoteURLForAutomation(remoteURL, false); err != nil {
			return "", err
		}
	}
	if unattended {
		pushURLs, err := gitRemoteURLs(repo, remote, true)
		if err != nil {
			return "", err
		}
		for _, remoteURL := range pushURLs {
			if err := validateRemoteURLForAutomation(remoteURL, true); err != nil {
				return "", err
			}
		}
	}
	return fetchURLs[0], nil
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
	remoteURL, remoteErr := s.gitRemoteURL(repo, remote, false)
	if remoteErr != nil && !strings.Contains(remoteErr.Error(), "No such remote") {
		status.Error = remoteErr.Error()
		return status
	}
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
	if push || config.AutoPush {
		if _, err := s.gitRemoteURL(repo, config.Remote, config.AutoPush); err != nil {
			return false, err
		}
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
		if err := s.gitPushWithPolicy(config, config.AutoPush); err != nil {
			return committed, err
		}
	}
	return committed, nil
}

func (s *Server) gitPush(config GitConfig) error {
	return s.gitPushWithPolicy(config, config.AutoPush)
}

func (s *Server) gitPushWithPolicy(config GitConfig, unattended bool) error {
	repo, err := s.gitRepo()
	if err != nil {
		return err
	}
	remote := config.Remote
	if remote == "" {
		remote = "origin"
	}
	if !validGitRemoteName(remote) {
		return errors.New("remote name contains invalid characters")
	}
	if _, err := s.gitRemoteURL(repo, remote, unattended); err != nil {
		return err
	}
	branch := strings.TrimSpace(config.Branch)
	if branch == "" {
		branch, _ = runGit(repo, "branch", "--show-current")
	}
	if branch == "" {
		return errors.New("git branch is not configured")
	}
	if err := validateGitBranch(branch); err != nil {
		return err
	}
	_, err = runGitWithTimeout(maxGitPushDuration, repo, "push", remote, branch)
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
	if !rejectOversizedDeclaredBody(w, r, maxFormSize) {
		return
	}
	limitRequestBody(w, r, maxFormSize)
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	gitInfo, statErr := os.Lstat(filepath.Join(s.SiteDir, ".git"))
	if statErr == nil {
		if gitInfo.Mode()&os.ModeSymlink != 0 || !gitInfo.IsDir() {
			writeJSONError(w, http.StatusBadRequest, "site/.git must be a real directory")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "status": s.gitStatus(GitConfig{Remote: "origin"})})
		return
	} else if !errors.Is(statErr, os.ErrNotExist) {
		writeJSONError(w, http.StatusInternalServerError, statErr.Error())
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
	limitRequestBody(w, r, maxFormSize)
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
	if !validGitRemoteName(gitConfig.Remote) {
		writeJSONError(w, http.StatusBadRequest, "remote name contains invalid characters")
		return
	}
	if err := validateGitBranch(gitConfig.Branch); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateRemoteURLForAutomation(gitConfig.RemoteURL, gitConfig.AutoPush); err != nil {
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
	if gitConfig.AutoPush {
		repo, err := s.gitRepo()
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "cannot enable auto-push: "+err.Error())
			return
		}
		if _, err := s.gitRemoteURL(repo, gitConfig.Remote, true); err != nil {
			writeJSONError(w, http.StatusBadRequest, "cannot enable auto-push: "+err.Error())
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

var gitSCPRemotePattern = regexp.MustCompile(`^[A-Za-z0-9._-]+@[A-Za-z0-9.-]+:[^\s?#]+$`)

func validateRemoteURL(value string) error {
	return validateRemoteURLForAutomation(value, false)
}

func validateRemoteURLForAutomation(value string, unattended bool) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if strings.ContainsAny(value, "\x00\r\n\t ") {
		return errors.New("remote URL contains forbidden whitespace or control characters")
	}
	if gitSCPRemotePattern.MatchString(value) {
		return nil
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || (parsed.Host == "" && !(strings.EqualFold(parsed.Scheme, "file") && parsed.Path != "")) {
		return errors.New("remote URL must be https://, ssh://, git://, file://, or git@host:path format")
	}
	scheme := strings.ToLower(parsed.Scheme)
	if parsed.User != nil {
		_, hasPassword := parsed.User.Password()
		if scheme != "ssh" || hasPassword {
			return errors.New("remote URL must not contain embedded credentials")
		}
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Opaque != "" {
		return errors.New("remote URL must not contain credentials, queries, or fragments")
	}
	switch scheme {
	case "http", "https", "ssh", "git", "file":
	default:
		return errors.New("unsupported remote URL scheme")
	}
	if unattended && (scheme == "http" || scheme == "git" || scheme == "file") {
		return errors.New("automatic pushes require an HTTPS or SSH remote")
	}
	return nil
}
func (s *Server) handleGitCommitAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	limitRequestBody(w, r, maxFormSize)
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
	if !rejectOversizedDeclaredBody(w, r, maxFormSize) {
		return
	}
	limitRequestBody(w, r, maxFormSize)
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
func decodeJSONBody(r *http.Request, destination any) error {
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return errors.New("request contains trailing JSON")
		}
		return err
	}
	return nil
}

func rejectOversizedDeclaredBody(w http.ResponseWriter, r *http.Request, limit int64) bool {
	if r.ContentLength > limit {
		writeJSONError(w, http.StatusRequestEntityTooLarge, "request body exceeds the size limit")
		return false
	}
	return true
}
func limitRequestBody(w http.ResponseWriter, r *http.Request, limit int64) {
	if r.Body != nil {
		r.Body = http.MaxBytesReader(w, r.Body, limit)
	}
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
	methodNotAllowedAllow(w, "GET, HEAD, POST")
}

func methodNotAllowedAllow(w http.ResponseWriter, allow string) {
	w.Header().Set("Allow", allow)
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}
