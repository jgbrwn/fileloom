package srv

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	localCommentsProxyPath = "/_fileloom/artalk"
	defaultCommentsHost    = "127.0.0.1"
	defaultCommentsPort    = 23366
	defaultCommentsService = "fileloom-artalk"
)

type CommentsLocalConfig struct {
	Host    string `json:"host,omitempty"`
	Port    int    `json:"port,omitempty"`
	Service string `json:"service,omitempty"`
}

func normalizeCommentsLocalConfig(local *CommentsLocalConfig) (*CommentsLocalConfig, error) {
	if local == nil {
		return nil, nil
	}
	copy := *local
	copy.Host = strings.TrimSpace(copy.Host)
	if copy.Host == "" {
		copy.Host = defaultCommentsHost
	}
	ip := net.ParseIP(copy.Host)
	if ip == nil || !ip.IsLoopback() {
		return nil, fmt.Errorf("local Artalk host must be a loopback IP address")
	}
	if copy.Port == 0 {
		copy.Port = defaultCommentsPort
	}
	if copy.Port < 1024 || copy.Port > 65535 {
		return nil, fmt.Errorf("local Artalk port must be between 1024 and 65535")
	}
	if copy.Service == "" {
		copy.Service = defaultCommentsService
	}
	for _, r := range copy.Service {
		if !(r == '-' || r == '_' || r == '.' || r == '@' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return nil, fmt.Errorf("local Artalk service name contains invalid characters")
		}
	}
	return &copy, nil
}

func commentsBrowserServer(config CommentsConfig) string {
	if config.Local != nil {
		return localCommentsProxyPath
	}
	return config.Server
}

func localCommentsAddress(local *CommentsLocalConfig) string {
	if local == nil {
		return net.JoinHostPort(defaultCommentsHost, strconv.Itoa(defaultCommentsPort))
	}
	return net.JoinHostPort(local.Host, strconv.Itoa(local.Port))
}

func suggestedCommentsSite(config SiteConfig) string {
	if site := strings.TrimSpace(config.Comments.Site); site != "" {
		return site
	}
	name := slugify(config.Title)
	if name == "" {
		name = "site"
	}
	name = "fileloom-" + name
	if len(name) > 64 {
		name = strings.TrimRight(name[:64], "-._")
	}
	return name
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func commentsInstallCommand(config SiteConfig, local *CommentsLocalConfig) string {
	port := defaultCommentsPort
	if local != nil && local.Port != 0 {
		port = local.Port
	}
	baseURL := strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	return "sudo ./scripts/install-artalk.sh --site-url " + shellQuote(baseURL) + " --site-key " + shellQuote(suggestedCommentsSite(config)) + " --port " + strconv.Itoa(port)
}

func probeLocalArtalk(local *CommentsLocalConfig) (reachable, recognized bool, detail string) {
	address := localCommentsAddress(local)
	client := &http.Client{
		Timeout: 900 * time.Millisecond,
		Transport: &http.Transport{
			Proxy:       nil,
			DialContext: (&net.Dialer{Timeout: 900 * time.Millisecond}).DialContext,
		},
	}
	request, err := http.NewRequest(http.MethodGet, "http://"+address+"/api/v2", nil)
	if err != nil {
		return false, false, err.Error()
	}
	response, err := client.Do(request)
	if err != nil {
		return false, false, err.Error()
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	text := strings.ToLower(string(body) + " " + response.Header.Get("Server") + " " + response.Header.Get("Location"))
	recognized = strings.Contains(text, "artalk") || strings.Contains(text, "cannot get /api/v2") || strings.Contains(text, "sidebar/")
	if recognized {
		return true, true, "Artalk responded"
	}
	if response.StatusCode >= 200 && response.StatusCode < 600 {
		return true, false, "another HTTP service is using this port"
	}
	return false, false, "unexpected local service response"
}

func (s *Server) handleCommentsStatusAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	config, err := s.loadSiteConfig()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	local := config.Comments.Local
	if local == nil {
		local, err = normalizeCommentsLocalConfig(&CommentsLocalConfig{})
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	reachable, recognized, detail := probeLocalArtalk(local)
	warning := ""
	if strings.HasPrefix(strings.ToLower(config.BaseURL), "http://localhost") || strings.HasPrefix(strings.ToLower(config.BaseURL), "http://127.0.0.1") {
		warning = "Set FILELOOM_BASE_URL to the public Fileloom origin before enabling comments."
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"configured":       config.Comments.Local != nil,
		"reachable":        reachable,
		"recognized":       recognized,
		"detail":           detail,
		"host":             local.Host,
		"port":             local.Port,
		"service":          local.Service,
		"proxy_path":       localCommentsProxyPath,
		"suggested_server": localCommentsProxyPath,
		"suggested_site":   suggestedCommentsSite(config),
		"install_command":  commentsInstallCommand(config, local),
		"base_url_warning": warning,
	})
}

func (s *Server) handleLocalCommentsProxy(w http.ResponseWriter, r *http.Request) {
	config, err := s.loadSiteConfig()
	if err != nil {
		http.Error(w, "Fileloom site configuration is unavailable", http.StatusServiceUnavailable)
		return
	}
	if !config.Comments.Enabled || config.Comments.Local == nil {
		http.NotFound(w, r)
		return
	}
	suffix := strings.TrimPrefix(r.URL.Path, localCommentsProxyPath)
	if suffix == "" {
		http.NotFound(w, r)
		return
	}
	if !strings.HasPrefix(suffix, "/api/") || strings.Contains(suffix, "..") || strings.ContainsAny(suffix, "\r\n") {
		http.NotFound(w, r)
		return
	}
	address := localCommentsAddress(config.Comments.Local)
	target := &url.URL{Scheme: "http", Host: address}
	proxy := httputil.NewSingleHostReverseProxy(target)
	originalDirector := proxy.Director
	proxy.Director = func(request *http.Request) {
		request.URL.Path = suffix
		request.URL.RawPath = ""
		originalDirector(request)
		request.Host = target.Host
		request.Header.Del("X-ExeDev-Email")
		request.Header.Set("X-Forwarded-Host", r.Host)
		request.Header.Set("X-Forwarded-Proto", requestScheme(r))
		request.Header.Set("X-Forwarded-For", remoteAddress(r))
	}
	proxy.Transport = &http.Transport{
		Proxy:                 nil,
		DialContext:           (&net.Dialer{Timeout: 900 * time.Millisecond, KeepAlive: 30 * time.Second}).DialContext,
		MaxIdleConns:          8,
		MaxIdleConnsPerHost:   4,
		IdleConnTimeout:       30 * time.Second,
		ResponseHeaderTimeout: 3 * time.Second,
	}
	proxy.ErrorHandler = func(response http.ResponseWriter, request *http.Request, proxyErr error) {
		writeJSONError(response, http.StatusBadGateway, "local Artalk is unavailable")
	}
	proxy.ServeHTTP(w, r)
}

func requestScheme(r *http.Request) string {
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

func remoteAddress(r *http.Request) string {
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil && host != "" {
		return host
	}
	return "0.0.0.0"
}

func isLocalCommentsPath(value string) bool {
	return value == localCommentsProxyPath || strings.HasPrefix(value, localCommentsProxyPath+"/")
}
