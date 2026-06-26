package main

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func withArgs(t *testing.T, args []string) {
	t.Helper()

	orig := os.Args
	t.Cleanup(func() { os.Args = orig })
	os.Args = args
}

// --- argsWithoutServiceFlag ---

func TestArgsWithoutServiceFlag_Empty(t *testing.T) {
	withArgs(t, []string{"binary"})
	assert.Empty(t, argsWithoutServiceFlag())
}

func TestArgsWithoutServiceFlag_SpaceSeparator(t *testing.T) {
	withArgs(t, []string{"binary", "--service", "install"})
	assert.Empty(t, argsWithoutServiceFlag())
}

func TestArgsWithoutServiceFlag_EqualsSeparator(t *testing.T) {
	withArgs(t, []string{"binary", "--service=install"})
	assert.Empty(t, argsWithoutServiceFlag())
}

func TestArgsWithoutServiceFlag_ShortForm(t *testing.T) {
	withArgs(t, []string{"binary", "-service", "install"})
	assert.Empty(t, argsWithoutServiceFlag())
}

func TestArgsWithoutServiceFlag_ShortFormEquals(t *testing.T) {
	withArgs(t, []string{"binary", "-service=uninstall"})
	assert.Empty(t, argsWithoutServiceFlag())
}

func TestArgsWithoutServiceFlag_PreservesOtherFlags(t *testing.T) {
	withArgs(t, []string{
		"binary",
		"--web.listen-address=:9836",
		"--service", "install",
		"--shutdown-on-error=true",
	})

	got := argsWithoutServiceFlag()
	assert.Equal(t, []string{"--web.listen-address=:9836", "--shutdown-on-error=true"}, got)
}

func TestArgsWithoutServiceFlag_NoServiceFlag(t *testing.T) {
	withArgs(t, []string{"binary", "--web.listen-address=:9835", "--shutdown-on-error=false"})

	got := argsWithoutServiceFlag()
	assert.Equal(t, []string{"--web.listen-address=:9835", "--shutdown-on-error=false"}, got)
}

func TestArgsWithoutServiceFlag_ServiceFlagAtEnd(t *testing.T) {
	// --service as last arg with no value: should be dropped, nothing else affected
	withArgs(t, []string{"binary", "--web.listen-address=:9835", "--service"})

	got := argsWithoutServiceFlag()
	assert.Equal(t, []string{"--web.listen-address=:9835"}, got)
}

// --- RootHandler ---

func newDiscardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestRootHandlerContainsMetricsPath(t *testing.T) {
	t.Parallel()

	h := NewRootHandler(newDiscardLogger(), "/metrics", false)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "/metrics")
}

func TestRootHandlerNoPprofLinksWhenDisabled(t *testing.T) {
	t.Parallel()

	h := NewRootHandler(newDiscardLogger(), "/metrics", false)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	assert.NotContains(t, rec.Body.String(), "/debug/pprof/")
}

func TestRootHandlerHasPprofLinksWhenEnabled(t *testing.T) {
	t.Parallel()

	h := NewRootHandler(newDiscardLogger(), "/metrics", true)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	assert.Contains(t, rec.Body.String(), "/debug/pprof/")
}

func TestRootHandlerCustomMetricsPath(t *testing.T) {
	t.Parallel()

	h := NewRootHandler(newDiscardLogger(), "/custom-metrics", false)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	body := rec.Body.String()
	assert.Contains(t, body, "/custom-metrics")
	assert.NotContains(t, body, `href="/metrics"`)
}

// --- newServeMux ---

func TestNewServeMuxRootReturns200(t *testing.T) {
	t.Parallel()

	mux := newServeMux(newDiscardLogger(), "/metrics", false)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestNewServeMuxMetricsPathReturns200(t *testing.T) {
	t.Parallel()

	mux := newServeMux(newDiscardLogger(), "/metrics", false)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestNewServeMuxCustomMetricsPath(t *testing.T) {
	t.Parallel()

	mux := newServeMux(newDiscardLogger(), "/custom", false)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/custom", nil))

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestNewServeMuxPprofDisabled(t *testing.T) {
	t.Parallel()

	mux := newServeMux(newDiscardLogger(), "/metrics", false)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil))

	// The root handler catches /debug/pprof/ when pprof is disabled.
	// Verify the response is the redirect page, not actual pprof output.
	body := rec.Body.String()
	assert.Contains(t, body, "Nvidia GPU Exporter")
	assert.NotContains(t, body, "goroutine")
}

func TestNewServeMuxPprofEnabled(t *testing.T) {
	t.Parallel()

	mux := newServeMux(newDiscardLogger(), "/metrics", true)

	pprofPaths := []string{
		"/debug/pprof/",
		"/debug/pprof/goroutine",
		"/debug/pprof/heap",
	}

	for _, path := range pprofPaths {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		assert.Equal(t, http.StatusOK, rec.Code, "path %s should return 200", path)
	}
}

func TestNewServeMuxRootContainsMetricsLink(t *testing.T) {
	t.Parallel()

	mux := newServeMux(newDiscardLogger(), "/metrics", false)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	assert.True(t, strings.Contains(rec.Body.String(), "/metrics"))
}
