package srv

import (
	"bytes"
	"context"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type slice6ObservedBuildContext struct {
	context.Context
	started chan struct{}
	once    sync.Once
}

func (c *slice6ObservedBuildContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.started) })
	return c.Context.Done()
}

type slice6CancelAfterDoneContext struct {
	context.Context
	done      chan struct{}
	after     int32
	calls     atomic.Int32
	cancelled sync.Once
}

func newSlice6CancelAfterDoneContext(after int32) *slice6CancelAfterDoneContext {
	return &slice6CancelAfterDoneContext{Context: context.Background(), done: make(chan struct{}), after: after}
}

func (c *slice6CancelAfterDoneContext) Done() <-chan struct{} {
	if c.calls.Add(1) >= c.after {
		c.cancelled.Do(func() { close(c.done) })
	}
	return c.done
}

func (c *slice6CancelAfterDoneContext) Err() error {
	select {
	case <-c.done:
		return context.Canceled
	default:
		return nil
	}
}

func slice6BlockAtBuildPhase(server *Server, ctx context.Context, phase string) <-chan struct{} {
	reached := make(chan struct{})
	var once sync.Once
	server.setBuildPhaseHook(func(got string) {
		if got != phase {
			return
		}
		once.Do(func() { close(reached) })
		<-ctx.Done()
	})
	return reached
}

func TestBuildContextConcurrentCallsSerializeWithoutCorruptingOutput(t *testing.T) {
	server, err := New(filepath.Join(t.TempDir(), "site"), filepath.Join(t.TempDir(), "web"), "")
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	const builds = 4
	errs := make(chan error, builds)
	var group sync.WaitGroup
	for range builds {
		group.Add(1)
		go func() {
			defer group.Done()
			_, buildErr := server.BuildContext(context.Background())
			errs <- buildErr
		}()
	}
	group.Wait()
	close(errs)
	for buildErr := range errs {
		if buildErr != nil {
			t.Fatalf("concurrent BuildContext error: %v", buildErr)
		}
	}
	stages, err := filepath.Glob(filepath.Join(server.SiteDir, ".fileloom-build-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(stages) != 0 {
		t.Fatalf("concurrent builds left staging directories: %v", stages)
	}
}

func TestBuildContextCancellationWhileWaitingForBuildSlot(t *testing.T) {
	server, err := New(filepath.Join(t.TempDir(), "site"), filepath.Join(t.TempDir(), "web"), "")
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	gate := server.ensureBuildGate()
	gate <- struct{}{}
	defer func() { <-gate }()

	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &slice6ObservedBuildContext{Context: base, started: make(chan struct{})}
	done := make(chan error, 1)
	go func() {
		_, buildErr := server.BuildContext(ctx)
		done <- buildErr
	}()
	select {
	case <-ctx.started:
	case <-time.After(time.Second):
		t.Fatal("build did not attempt to acquire the build slot")
	}
	cancel()
	select {
	case buildErr := <-done:
		if !errors.Is(buildErr, context.Canceled) {
			t.Fatalf("BuildContext error = %v, want context.Canceled", buildErr)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled build remained blocked on the build slot")
	}
}

func TestCopyDirContextCancellationDuringChunkedCopy(t *testing.T) {
	source := filepath.Join(t.TempDir(), "source")
	destination := filepath.Join(t.TempDir(), "destination")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	data := make([]byte, buildIOChunkSize*2)
	for index := range data {
		data[index] = byte(index)
	}
	if err := os.WriteFile(filepath.Join(source, "asset.bin"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := newSlice6CancelAfterDoneContext(7)
	if err := copyDirContext(ctx, source, destination); !errors.Is(err, context.Canceled) {
		t.Fatalf("copyDirContext error = %v, want context.Canceled", err)
	}
}

func TestCheckBuildOutputContextCancellationDuringRead(t *testing.T) {
	root := t.TempDir()
	contents := "<!doctype html><html lang=\"en\"><head><title>Check</title></head><body><p>check</p></body></html>"
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := newSlice6CancelAfterDoneContext(8)
	if _, err := checkBuildOutputContext(ctx, root); !errors.Is(err, context.Canceled) {
		t.Fatalf("checkBuildOutputContext error = %v, want context.Canceled", err)
	}
}

func TestCanceledClientWaitingForBuildSlotRollsBackSource(t *testing.T) {
	siteDir := filepath.Join(t.TempDir(), "site")
	server, err := New(siteDir, filepath.Join(t.TempDir(), "web"), "owner@example.com")
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	sourcePath := filepath.Join(siteDir, "content", "pages", "about.html")
	beforeSource, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	publicPath := filepath.Join(siteDir, "public", "index.html")
	beforePublic, err := os.ReadFile(publicPath)
	if err != nil {
		t.Fatal(err)
	}

	gate := server.ensureBuildGate()
	gate <- struct{}{}
	defer func() { <-gate }()
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	observed := &slice6ObservedBuildContext{Context: base, started: make(chan struct{})}
	request := httptest.NewRequest(http.MethodPost, "/_cms/api/editor-save", strings.NewReader("file=pages%2Fabout.html&html=%3Cp%3Eslot-canceled%3C%2Fp%3E"))
	request = request.WithContext(observed)
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("X-ExeDev-Email", "owner@example.com")
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		server.Handler().ServeHTTP(response, request)
		close(done)
	}()
	select {
	case <-observed.started:
	case <-time.After(time.Second):
		t.Fatal("request did not wait for the build slot")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("canceled request did not finish while waiting for the build slot")
	}
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("canceled waiting build status = %d, want 500: %s", response.Code, response.Body)
	}
	afterSource, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(afterSource) != string(beforeSource) {
		t.Fatal("canceled waiting build did not roll source back")
	}
	afterPublic, err := os.ReadFile(publicPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(afterPublic) != string(beforePublic) {
		t.Fatal("canceled waiting build changed public output")
	}
}

func TestCanceledClientBuildRollsBackSource(t *testing.T) {
	siteDir := filepath.Join(t.TempDir(), "site")
	server, err := New(siteDir, filepath.Join(t.TempDir(), "web"), "owner@example.com")
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	sourcePath := filepath.Join(siteDir, "content", "pages", "about.html")
	beforeSource, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	publicPath := filepath.Join(siteDir, "public", "index.html")
	beforePublic, err := os.ReadFile(publicPath)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	phaseReached := slice6BlockAtBuildPhase(server, ctx, "before-swap")
	request := httptest.NewRequest(http.MethodPost, "/_cms/api/editor-save", strings.NewReader("file=pages%2Fabout.html&html=%3Cp%3Ecanceled%3C%2Fp%3E"))
	request = request.WithContext(ctx)
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("X-ExeDev-Email", "owner@example.com")
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		server.Handler().ServeHTTP(response, request)
		close(done)
	}()

	select {
	case <-phaseReached:
	case <-time.After(time.Second):
		t.Fatal("canceled client build did not reach the pre-publication phase")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("canceled client request did not finish")
	}
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("canceled client build status = %d, want 500: %s", response.Code, response.Body)
	}
	afterSource, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(afterSource) != string(beforeSource) {
		t.Fatal("canceled client build did not roll source back")
	}
	afterPublic, err := os.ReadFile(publicPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(afterPublic) != string(beforePublic) {
		t.Fatal("canceled client build changed public output")
	}
}

func TestBuildContextCancellationAfterCopyCleansStagingAndPreservesPublic(t *testing.T) {
	siteDir := filepath.Join(t.TempDir(), "site")
	server, err := New(siteDir, filepath.Join(t.TempDir(), "web"), "")
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	publicPath := filepath.Join(siteDir, "public", "index.html")
	before, err := os.ReadFile(publicPath)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	phaseReached := slice6BlockAtBuildPhase(server, ctx, "after-copy")
	done := make(chan error, 1)
	go func() {
		_, buildErr := server.BuildContext(ctx)
		done <- buildErr
	}()

	select {
	case <-phaseReached:
	case <-time.After(time.Second):
		t.Fatal("build did not reach the post-copy phase")
	}
	cancel()
	select {
	case buildErr := <-done:
		if !errors.Is(buildErr, context.Canceled) {
			t.Fatalf("BuildContext error = %v, want context.Canceled", buildErr)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled build did not finish")
	}

	after, err := os.ReadFile(publicPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("canceled build changed public output")
	}
	stages, err := filepath.Glob(filepath.Join(siteDir, ".fileloom-build-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(stages) != 0 {
		t.Fatalf("build staging directories remain: %v", stages)
	}
	backups, err := filepath.Glob(filepath.Join(siteDir, "public.backup-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 0 {
		t.Fatalf("public backups remain after canceled build: %v", backups)
	}
}

func TestCanceledThemeActivationRollsBackConfig(t *testing.T) {
	siteDir := filepath.Join(t.TempDir(), "site")
	server, err := New(siteDir, filepath.Join(t.TempDir(), "web"), "owner@example.com")
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	name := "sunset"
	assetsDir := filepath.Join(siteDir, "themes", name, "assets")
	if err := os.MkdirAll(assetsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(assetsDir, "style.css"), []byte("body { color: #f0f; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	beforeConfig, err := server.loadSiteConfig()
	if err != nil {
		t.Fatal(err)
	}
	beforePublic, err := os.ReadFile(filepath.Join(siteDir, "public", "index.html"))
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	phaseReached := slice6BlockAtBuildPhase(server, ctx, "before-swap")
	request := httptest.NewRequest(http.MethodPost, "/_cms/api/themes/activate", strings.NewReader("name="+name))
	request = request.WithContext(ctx)
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("X-ExeDev-Email", "owner@example.com")
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		server.Handler().ServeHTTP(response, request)
		close(done)
	}()
	select {
	case <-phaseReached:
	case <-time.After(time.Second):
		t.Fatal("theme activation did not reach the pre-publication phase")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("canceled theme activation did not finish")
	}
	if response.Code != http.StatusBadRequest {
		t.Fatalf("canceled theme activation status = %d, want 400: %s", response.Code, response.Body)
	}
	afterConfig, err := server.loadSiteConfig()
	if err != nil {
		t.Fatal(err)
	}
	if afterConfig.Theme != beforeConfig.Theme {
		t.Fatalf("canceled theme activation changed config theme to %q", afterConfig.Theme)
	}
	afterPublic, err := os.ReadFile(filepath.Join(siteDir, "public", "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if string(afterPublic) != string(beforePublic) {
		t.Fatal("canceled theme activation changed public output")
	}
}

func TestCanceledMediaUploadRollsBackMedia(t *testing.T) {
	siteDir := filepath.Join(t.TempDir(), "site")
	server, err := New(siteDir, filepath.Join(t.TempDir(), "web"), "owner@example.com")
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	beforePublic, err := os.ReadFile(filepath.Join(siteDir, "public", "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "canceled.mp4")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("not-a-real-video-but-a-valid-upload-payload")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	phaseReached := slice6BlockAtBuildPhase(server, ctx, "before-swap")
	request := httptest.NewRequest(http.MethodPost, "/_cms/api/media", &body)
	request = request.WithContext(ctx)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("X-ExeDev-Email", "owner@example.com")
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		server.Handler().ServeHTTP(response, request)
		close(done)
	}()
	select {
	case <-phaseReached:
	case <-time.After(time.Second):
		t.Fatal("media upload did not reach the pre-publication phase")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("canceled media upload did not finish")
	}
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("canceled media upload status = %d, want 500: %s", response.Code, response.Body)
	}
	if _, err := os.Stat(filepath.Join(siteDir, "media", "canceled.mp4")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("canceled media upload left media file, stat error = %v", err)
	}
	afterPublic, err := os.ReadFile(filepath.Join(siteDir, "public", "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if string(afterPublic) != string(beforePublic) {
		t.Fatal("canceled media upload changed public output")
	}
}
