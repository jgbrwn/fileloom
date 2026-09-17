package srv

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

const youtubeNoCookieOrigin = "https://www.youtube-nocookie.com"

var youtubeVideoIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)

type fileloomYouTube struct {
	ID       string
	Title    string
	Fallback string
}

type fileloomMediaRenderMode uint8

const (
	fileloomMediaEditor fileloomMediaRenderMode = iota
	fileloomMediaPreview
	fileloomMediaPublic
)

type fileloomTagRange struct {
	start      int
	end        int
	openingEnd int
	name       string
	closing    bool
}

// normalizeYouTubeURL accepts only the small set of HTTPS URLs Fileloom can
// safely turn into an embed. It deliberately does not accept embed HTML or
// arbitrary hosts, and returns only the exact video ID used by the marker.
func normalizeYouTubeURL(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("YouTube URL is required")
	}
	if strings.IndexFunc(value, unicode.IsSpace) >= 0 || strings.ContainsAny(value, "\x00\r\n") {
		return "", errors.New("YouTube URL must be a single HTTPS URL")
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed == nil {
		return "", errors.New("YouTube URL is invalid")
	}
	if !strings.EqualFold(parsed.Scheme, "https") {
		return "", errors.New("YouTube URL must use HTTPS")
	}
	if parsed.Host == "" || parsed.User != nil || strings.Contains(parsed.Host, ":") {
		return "", errors.New("YouTube URL host is not allowed")
	}
	if parsed.Fragment != "" {
		return "", errors.New("YouTube URL fragments are not supported")
	}
	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return "", errors.New("YouTube URL query is invalid")
	}
	host := strings.ToLower(parsed.Hostname())
	var id string
	switch host {
	case "youtube.com", "www.youtube.com":
		switch parsed.Path {
		case "/watch":
			values := query["v"]
			if len(values) != 1 {
				return "", errors.New("YouTube watch URL must contain one video ID")
			}
			id = values[0]
		case "":
			return "", errors.New("YouTube URL path is not allowed")
		default:
			parts := strings.Split(strings.TrimPrefix(parsed.Path, "/"), "/")
			if len(parts) == 2 && (parts[0] == "shorts" || parts[0] == "embed") {
				id = parts[1]
			} else {
				return "", errors.New("YouTube URL form is not allowed")
			}
		}
	case "youtu.be":
		parts := strings.Split(strings.TrimPrefix(parsed.Path, "/"), "/")
		if len(parts) != 1 || parts[0] == "" {
			return "", errors.New("youtu.be URL form is not allowed")
		}
		id = parts[0]
	default:
		return "", errors.New("YouTube URL host is not allowed")
	}
	if !youtubeVideoIDPattern.MatchString(id) {
		return "", errors.New("YouTube video ID must be exactly 11 characters")
	}
	return id, nil
}

// parseYouTubeVideoURL is kept as a descriptive alias for callers and tests.
func parseYouTubeVideoURL(value string) (string, error) {
	return normalizeYouTubeURL(value)
}

func normalizeYouTubeLabel(value, fallback string) string {
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, value)
	value = strings.Join(strings.Fields(value), " ")
	if value == "" {
		value = fallback
	}
	runes := []rune(value)
	if len(runes) > 200 {
		value = string(runes[:197]) + "…"
	}
	return value
}

func youtubeMarkerData(id, title, fallback string) fileloomYouTube {
	return fileloomYouTube{
		ID:       id,
		Title:    normalizeYouTubeLabel(title, "YouTube video"),
		Fallback: normalizeYouTubeLabel(fallback, "Watch on YouTube"),
	}
}

func youtubeMarkerHTML(id, title, fallback string) string {
	data := youtubeMarkerData(id, title, fallback)
	if !youtubeVideoIDPattern.MatchString(data.ID) {
		return ""
	}
	idEscaped := html.EscapeString(data.ID)
	titleEscaped := html.EscapeString(data.Title)
	fallbackEscaped := html.EscapeString(data.Fallback)
	return `<figure class="fileloom-youtube" data-fileloom-youtube data-youtube-id="` + idEscaped + `" data-youtube-title="` + titleEscaped + `" data-youtube-fallback="` + fallbackEscaped + `"><div class="fileloom-youtube-placeholder" role="img" aria-label="` + titleEscaped + `"><span class="fileloom-youtube-play" aria-hidden="true">▶</span><span class="fileloom-youtube-copy"><strong>` + titleEscaped + `</strong><small>` + fallbackEscaped + `</small></span></div></figure>`
}

func canonicalYouTubeMarker(value string) (string, error) {
	id, err := normalizeYouTubeURL(value)
	if err != nil {
		return "", err
	}
	return youtubeMarkerHTML(id, "YouTube video", "Watch on YouTube"), nil
}

func parseHTMLTagEnd(source string, start int) int {
	end, _ := parseHTMLTagEndContext(context.Background(), source, start)
	return end
}

func parseHTMLTagEndContext(ctx context.Context, source string, start int) (int, error) {
	quote := byte(0)
	for index := start + 1; index < len(source); index++ {
		if index&0x3fff == 0 {
			if err := mediaContextErr(ctx); err != nil {
				return -1, err
			}
		}
		character := source[index]
		if quote != 0 {
			if character == quote {
				quote = 0
			}
			continue
		}
		if character == '\'' || character == '"' {
			quote = character
			continue
		}
		if character == '>' {
			return index + 1, nil
		}
	}
	if err := mediaContextErr(ctx); err != nil {
		return -1, err
	}
	return -1, nil
}

func parseHTMLTag(source string, start int) (fileloomTagRange, bool) {
	tag, err := parseHTMLTagContext(context.Background(), source, start)
	return tag, err == nil && tag.name != ""
}

func parseHTMLTagContext(ctx context.Context, source string, start int) (fileloomTagRange, error) {
	if start < 0 || start >= len(source) || source[start] != '<' {
		return fileloomTagRange{}, nil
	}
	index := start + 1
	closing := false
	if index < len(source) && source[index] == '/' {
		closing = true
		index++
	}
	for index < len(source) && (source[index] == ' ' || source[index] == '\t' || source[index] == '\r' || source[index] == '\n') {
		if index&0x3fff == 0 {
			if err := mediaContextErr(ctx); err != nil {
				return fileloomTagRange{}, err
			}
		}
		index++
	}
	nameStart := index
	for index < len(source) {
		if index&0x3fff == 0 {
			if err := mediaContextErr(ctx); err != nil {
				return fileloomTagRange{}, err
			}
		}
		character := source[index]
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || character == ':' || character == '-' || (character >= '0' && character <= '9' && index > nameStart) {
			index++
			continue
		}
		break
	}
	if index == nameStart {
		return fileloomTagRange{}, nil
	}
	end, err := parseHTMLTagEndContext(ctx, source, start)
	if err != nil {
		return fileloomTagRange{}, err
	}
	if end < 0 {
		return fileloomTagRange{}, nil
	}
	return fileloomTagRange{start: start, end: end, openingEnd: end, name: strings.ToLower(source[nameStart:index]), closing: closing}, nil
}

func htmlTagSelfClosing(source string, tag fileloomTagRange) bool {
	inside := strings.TrimSpace(source[tag.start:tag.end])
	return strings.HasSuffix(inside, "/>")
}

func isFileloomRawHTMLTag(name string) bool {
	switch strings.ToLower(name) {
	case "script", "style", "textarea", "title", "noscript", "template":
		return true
	default:
		return false
	}
}
func parseHTMLAttributes(tag string) map[string]string {
	attributes, _ := parseHTMLAttributesContext(context.Background(), tag)
	return attributes
}

func parseHTMLAttributesContext(ctx context.Context, tag string) (map[string]string, error) {
	attributes := make(map[string]string)
	if len(tag) < 2 || tag[0] != '<' {
		return attributes, nil
	}
	index := 1
	if index < len(tag) && tag[index] == '/' {
		index++
	}
	for index < len(tag) && (tag[index] == ' ' || tag[index] == '\t' || tag[index] == '\r' || tag[index] == '\n') {
		if index&0x3fff == 0 {
			if err := mediaContextErr(ctx); err != nil {
				return nil, err
			}
		}
		index++
	}
	for index < len(tag) && tag[index] != '>' && tag[index] != '/' {
		if index&0x3fff == 0 {
			if err := mediaContextErr(ctx); err != nil {
				return nil, err
			}
		}
		for index < len(tag) && (tag[index] == ' ' || tag[index] == '\t' || tag[index] == '\r' || tag[index] == '\n') {
			index++
		}
		if index >= len(tag) || tag[index] == '>' || tag[index] == '/' {
			break
		}
		nameStart := index
		for index < len(tag) && tag[index] != '=' && tag[index] != '>' && tag[index] != '/' && tag[index] != ' ' && tag[index] != '\t' && tag[index] != '\r' && tag[index] != '\n' {
			if index&0x3fff == 0 {
				if err := mediaContextErr(ctx); err != nil {
					return nil, err
				}
			}
			index++
		}
		name := strings.ToLower(tag[nameStart:index])
		if name == "" {
			index++
			continue
		}
		for index < len(tag) && (tag[index] == ' ' || tag[index] == '\t' || tag[index] == '\r' || tag[index] == '\n') {
			index++
		}
		value := ""
		if index < len(tag) && tag[index] == '=' {
			index++
			for index < len(tag) && (tag[index] == ' ' || tag[index] == '\t' || tag[index] == '\r' || tag[index] == '\n') {
				index++
			}
			if index < len(tag) && (tag[index] == '\'' || tag[index] == '"') {
				quote := tag[index]
				index++
				valueStart := index
				for index < len(tag) && tag[index] != quote {
					if index&0x3fff == 0 {
						if err := mediaContextErr(ctx); err != nil {
							return nil, err
						}
					}
					index++
				}
				value = tag[valueStart:index]
				if index < len(tag) {
					index++
				}
			} else {
				valueStart := index
				for index < len(tag) && tag[index] != '>' && tag[index] != ' ' && tag[index] != '\t' && tag[index] != '\r' && tag[index] != '\n' {
					if index&0x3fff == 0 {
						if err := mediaContextErr(ctx); err != nil {
							return nil, err
						}
					}
					index++
				}
				value = tag[valueStart:index]
			}
		}
		if _, exists := attributes[name]; !exists {
			attributes[name] = html.UnescapeString(value)
		}
	}
	if err := mediaContextErr(ctx); err != nil {
		return nil, err
	}
	return attributes, nil
}

func indexByteContext(ctx context.Context, source string, target byte, start int) (int, error) {
	for offset := start; offset < len(source); {
		if err := mediaContextErr(ctx); err != nil {
			return -1, err
		}
		end := offset + buildIOChunkSize
		if end > len(source) {
			end = len(source)
		}
		if index := strings.IndexByte(source[offset:end], target); index >= 0 {
			return offset + index, nil
		}
		offset = end
	}
	return -1, nil
}

func matchingFileloomTagEnd(source string, opening fileloomTagRange) int {
	end, _ := matchingFileloomTagEndContext(context.Background(), source, opening)
	return end
}
func matchingFileloomTagEndContext(ctx context.Context, source string, opening fileloomTagRange) (int, error) {
	if err := mediaContextErr(ctx); err != nil {
		return -1, err
	}
	if htmlTagSelfClosing(source, opening) {
		return opening.end, nil
	}
	depth := 1
	cursor := opening.end
	for cursor < len(source) {
		if err := mediaContextErr(ctx); err != nil {
			return -1, err
		}
		start, err := indexByteContext(ctx, source, '<', cursor)
		if err != nil {
			return -1, err
		}
		if start < 0 {
			return -1, nil
		}
		if strings.HasPrefix(source[start:], "<!--") {
			commentEnd := strings.Index(source[start+4:], "-->")
			cursor = len(source)
			if commentEnd >= 0 {
				cursor = start + 4 + commentEnd + 3
			}
			continue
		}
		tag, err := parseHTMLTagContext(ctx, source, start)
		if err != nil {
			return -1, err
		}
		if tag.name == "" {
			cursor = start + 1
			continue
		}
		if isFileloomRawHTMLTag(tag.name) {
			if tag.name != opening.name {
				if !tag.closing {
					var err error
					cursor, err = skipRawHTMLTagContext(ctx, source, tag)
					if err != nil {
						return -1, err
					}
				} else {
					cursor = tag.end
				}
				continue
			}
		}
		if tag.name != opening.name {
			cursor = tag.end
			continue
		}
		if tag.closing {
			depth--
			if depth == 0 {
				return tag.end, nil
			}
		} else if !htmlTagSelfClosing(source, tag) {
			depth++
		}
		cursor = tag.end
	}
	return -1, nil
}

func skipRawHTMLTag(source string, opening fileloomTagRange) int {
	end, _ := skipRawHTMLTagContext(context.Background(), source, opening)
	return end
}

func skipRawHTMLTagContext(ctx context.Context, source string, opening fileloomTagRange) (int, error) {
	if err := mediaContextErr(ctx); err != nil {
		return opening.end, err
	}
	lower := strings.ToLower(source)
	needle := "</" + opening.name
	cursor := opening.end
	for cursor < len(source) {
		if err := mediaContextErr(ctx); err != nil {
			return cursor, err
		}
		index := strings.Index(lower[cursor:], needle)
		if index < 0 {
			return len(source), nil
		}
		index += cursor
		end, err := parseHTMLTagEndContext(ctx, source, index)
		if err != nil {
			return cursor, err
		}
		if end < 0 {
			return len(source), nil
		}
		if end > index+len(needle) && (source[index+len(needle)] == '>' || source[index+len(needle)] == ' ' || source[index+len(needle)] == '\t' || source[index+len(needle)] == '\r' || source[index+len(needle)] == '\n') {
			return end, nil
		}
		cursor = index + len(needle)
	}
	return len(source), nil
}

func fileloomYouTubeTagRanges(source string) []fileloomTagRange {
	ranges, _ := fileloomYouTubeTagRangesContext(context.Background(), source)
	return ranges
}

func fileloomYouTubeTagRangesContext(ctx context.Context, source string) ([]fileloomTagRange, error) {
	var ranges []fileloomTagRange
	cursor := 0
	for cursor < len(source) {
		if err := mediaContextErr(ctx); err != nil {
			return nil, err
		}
		start, err := indexByteContext(ctx, source, '<', cursor)
		if err != nil {
			return nil, err
		}
		if start < 0 {
			break
		}
		if strings.HasPrefix(source[start:], "<!--") {
			commentEnd := strings.Index(source[start+4:], "-->")
			cursor = len(source)
			if commentEnd >= 0 {
				cursor = start + 4 + commentEnd + 3
			}
			continue
		}
		tag, err := parseHTMLTagContext(ctx, source, start)
		if err != nil {
			return nil, err
		}
		if tag.name == "" {
			cursor = start + 1
			continue
		}
		if isFileloomRawHTMLTag(tag.name) {
			if !tag.closing {
				var err error
				cursor, err = skipRawHTMLTagContext(ctx, source, tag)
				if err != nil {
					return nil, err
				}
			} else {
				cursor = tag.end
			}
			continue
		}
		if (tag.name == "figure" || tag.name == "div") && !tag.closing {
			attrs, attrErr := parseHTMLAttributesContext(ctx, source[tag.start:tag.end])
			if attrErr != nil {
				return nil, attrErr
			}
			if _, marked := attrs["data-fileloom-youtube"]; marked {
				end, err := matchingFileloomTagEndContext(ctx, source, tag)
				if err != nil {
					return nil, err
				}
				if end >= tag.end {
					tag.end = end
					ranges = append(ranges, tag)
					cursor = end
					continue
				}
			}
		}
		cursor = tag.end
	}
	return ranges, nil
}

func fileloomYouTubeFromOpening(source string, opening fileloomTagRange) (fileloomYouTube, bool) {
	data, ok, _ := fileloomYouTubeFromOpeningContext(context.Background(), source, opening)
	return data, ok
}

func fileloomYouTubeFromOpeningContext(ctx context.Context, source string, opening fileloomTagRange) (fileloomYouTube, bool, error) {
	attrs, err := parseHTMLAttributesContext(ctx, source[opening.start:opening.openingEnd])
	if err != nil {
		return fileloomYouTube{}, false, err
	}
	id := strings.TrimSpace(attrs["data-youtube-id"])
	if !youtubeVideoIDPattern.MatchString(id) {
		return fileloomYouTube{}, false, nil
	}
	if strings.EqualFold(attrs["data-fileloom-rendered"], "public") {
		return fileloomYouTube{}, false, nil
	}
	return youtubeMarkerData(id, attrs["data-youtube-title"], attrs["data-youtube-fallback"]), true, nil
}

func fileloomMediaContainsYouTube(source string) bool {
	contains, _ := fileloomMediaContainsYouTubeContext(context.Background(), source)
	return contains
}

func fileloomMediaContainsYouTubeContext(ctx context.Context, source string) (bool, error) {
	ranges, err := fileloomYouTubeTagRangesContext(ctx, source)
	if err != nil {
		return false, err
	}
	for _, opening := range ranges {
		if err := mediaContextErr(ctx); err != nil {
			return false, err
		}
		_, ok, err := fileloomYouTubeFromOpeningContext(ctx, source, opening)
		if err != nil {
			return false, err
		}
		if ok {
			return true, nil
		}
		attrs, err := parseHTMLAttributesContext(ctx, source[opening.start:opening.openingEnd])
		if err != nil {
			return false, err
		}
		if youtubeVideoIDPattern.MatchString(strings.TrimSpace(attrs["data-youtube-id"])) {
			return true, nil
		}
	}
	return false, nil
}

func containsFoldedContext(ctx context.Context, source, needle string) (bool, error) {
	if needle == "" {
		return true, nil
	}
	offset := 0
	overlap := len(needle) - 1
	foldedNeedle := strings.ToLower(needle)
	for offset < len(source) {
		if err := mediaContextErr(ctx); err != nil {
			return false, err
		}
		end := offset + buildIOChunkSize
		if end > len(source) {
			end = len(source)
		}
		if strings.Contains(strings.ToLower(source[offset:end]), foldedNeedle) {
			return true, nil
		}
		if end == len(source) {
			break
		}
		next := end - overlap
		if next <= offset {
			next = end
		}
		offset = next
	}
	return false, nil
}

func fileloomMediaContainsLocalVideo(source string) bool {
	contains, _ := fileloomMediaContainsLocalVideoContext(context.Background(), source)
	return contains
}
func fileloomMediaContainsLocalVideoContext(ctx context.Context, source string) (bool, error) {
	if err := mediaContextErr(ctx); err != nil {
		return false, err
	}
	hasData, err := containsFoldedContext(ctx, source, "data-fileloom-video")
	if err != nil {
		return false, err
	}
	hasClass, err := containsFoldedContext(ctx, source, "fileloom-video")
	if err != nil {
		return false, err
	}
	return hasData || hasClass, nil
}

func fileloomYouTubePublicHTML(data fileloomYouTube) string {
	idEscaped := html.EscapeString(data.ID)
	titleEscaped := html.EscapeString(data.Title)
	fallbackEscaped := html.EscapeString(data.Fallback)
	watchURL := html.EscapeString("https://www.youtube.com/watch?v=" + data.ID)
	ariaLabel := html.EscapeString("Load YouTube video: " + data.Title)
	return `<figure class="fileloom-youtube fileloom-youtube-public" data-fileloom-youtube data-fileloom-rendered="public" data-youtube-id="` + idEscaped + `" data-youtube-title="` + titleEscaped + `" data-youtube-fallback="` + fallbackEscaped + `" data-youtube-origin="` + youtubeNoCookieOrigin + `"><button type="button" class="fileloom-youtube-load" data-fileloom-youtube-load aria-label="` + ariaLabel + `"><span class="fileloom-youtube-placeholder"><span class="fileloom-youtube-play" aria-hidden="true">▶</span><span class="fileloom-youtube-copy"><strong>` + titleEscaped + `</strong><small>` + fallbackEscaped + `</small></span></span></button><a class="fileloom-youtube-fallback" data-fileloom-youtube-fallback href="` + watchURL + `">` + fallbackEscaped + `</a></figure>`
}

func fileloomYouTubePreviewHTML(data fileloomYouTube) string {
	titleEscaped := html.EscapeString(data.Title)
	fallbackEscaped := html.EscapeString(data.Fallback + " · Preview is inert")
	return `<figure class="fileloom-youtube fileloom-youtube-preview" data-fileloom-youtube data-fileloom-rendered="preview" data-youtube-id="` + html.EscapeString(data.ID) + `" data-youtube-title="` + titleEscaped + `" data-youtube-fallback="` + html.EscapeString(data.Fallback) + `"><div class="fileloom-youtube-placeholder" data-fileloom-youtube-inert="true" role="img" aria-label="` + titleEscaped + `"><span class="fileloom-youtube-play" aria-hidden="true">▶</span><span class="fileloom-youtube-copy"><strong>` + titleEscaped + `</strong><small>` + fallbackEscaped + `</small></span></div></figure>`
}

func renderFileloomMedia(source string, mode fileloomMediaRenderMode) string {
	rendered, _ := renderFileloomMediaContext(context.Background(), source, mode)
	return rendered
}

func renderFileloomMediaContext(ctx context.Context, source string, mode fileloomMediaRenderMode) (string, error) {
	ranges, err := fileloomYouTubeTagRangesContext(ctx, source)
	if err != nil {
		return "", err
	}
	for index := len(ranges) - 1; index >= 0; index-- {
		if err := mediaContextErr(ctx); err != nil {
			return "", err
		}
		opening := ranges[index]
		data, ok, err := fileloomYouTubeFromOpeningContext(ctx, source, opening)
		if err != nil {
			return "", err
		}
		if !ok {
			continue
		}
		var replacement string
		switch mode {
		case fileloomMediaPublic:
			replacement = fileloomYouTubePublicHTML(data)
		case fileloomMediaPreview:
			replacement = fileloomYouTubePreviewHTML(data)
		default:
			continue
		}
		source = source[:opening.start] + replacement + source[opening.end:]
	}
	if err := mediaContextErr(ctx); err != nil {
		return "", err
	}
	return source, nil
}

func mediaContextErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

func ensureFileloomMediaStyles(source string, assetPath string) string {
	if !strings.Contains(source, assetPath) {
		link := `<link rel="stylesheet" href="` + assetPath + `">`
		lower := strings.ToLower(source)
		if index := strings.Index(lower, "</head>"); index >= 0 {
			source = source[:index] + link + source[index:]
		} else {
			source = link + source
		}
	}
	return source
}

func ensureFileloomMediaAssets(source string) string {
	source = ensureFileloomMediaStyles(source, "/theme/fileloom-media.css")
	if !strings.Contains(source, "/theme/fileloom-media.js") {
		script := `<script src="/theme/fileloom-media.js" defer></script>`
		if index := strings.Index(strings.ToLower(source), "</body>"); index >= 0 {
			source = source[:index] + script + source[index:]
		} else {
			source += script
		}
	}
	return source
}

func (s *Server) publicRequestHasYouTube(requestPath string) bool {
	s.publicMu.RLock()
	defer s.publicMu.RUnlock()
	requested := strings.TrimPrefix(requestPath, "/")
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
		if err != nil || privateGeneratedPath(rel) || !strings.EqualFold(filepath.Ext(rel), ".html") {
			continue
		}
		resolved, info, err := safeResolvedPath(publicDir, rel)
		if err != nil || info.IsDir() || !info.Mode().IsRegular() {
			continue
		}
		data, err := os.ReadFile(resolved)
		if err == nil && fileloomMediaContainsYouTube(string(data)) {
			return true
		}
	}
	return false
}

// youtubeMarkerFromURL is a descriptive alias for editor-facing callers and tests.
func youtubeMarkerFromURL(value string) (string, error) {
	marker, err := canonicalYouTubeMarker(value)
	if err != nil {
		return "", fmt.Errorf("invalid YouTube URL: %w", err)
	}
	return marker, nil
}
