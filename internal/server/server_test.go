package server_test

import (
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/corani/wren"
	"github.com/corani/wren/internal/server"
	"github.com/corani/wren/internal/store"
	"github.com/corani/wren/internal/study"
)

func testHandler(t *testing.T) (http.Handler, *store.Store) {
	t.Helper()
	st, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "wren.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, st.Close()) })
	_, err = st.Sync(t.Context(), fstest.MapFS{"data/test.json": {
		Data: []byte(`{"label":"HSK 1","totalTerms":2,"terms":[
			{"simplified":"one","pinyin":"yi","english":"one"},
			{"simplified":"two","pinyin":"er","english":"two"}]}`),
	}})
	require.NoError(t, err)
	assets, err := fs.Sub(wren.Files, "web")
	require.NoError(t, err)
	return server.New(st, slog.Default(), assets).Handler(), st
}

func TestHTTPRoutes(t *testing.T) {
	handler, _ := testHandler(t)
	for _, tt := range []struct {
		name   string
		method string
		path   string
		body   string
		origin string
		status int
		text   string
	}{
		{"embedded home", "GET", "/", "", "", 200, "One word at a time."},
		{"embedded css", "GET", "/css/app.css", "", "", 200, "prefers-color-scheme"},
		{"embedded javascript", "GET", "/js/app.js", "", "", 200, "CardStack"},
		{"settings", "GET", "/api/settings", "", "", 200, "available"},
		{"no session", "GET", "/api/sessions/current", "", "", 200, "null"},
		{"invalid session ID", "GET", "/api/sessions/abc", "", "", 400, "invalid session ID"},
		{"unknown session", "GET", "/api/sessions/99", "", "", 404, "not found"},
		{"unknown API", "GET", "/api/missing", "", "", 404, "API route"},
		{"wrong method", "GET", "/api/sessions", "", "", 404, "API route"},
		{"static mutation rejected", "POST", "/", `{}`, "", 405, "method not allowed"},
		{"malformed JSON", "POST", "/api/sessions", `{`, "", 400, "invalid JSON"},
		{"unknown fields", "POST", "/api/sessions", `{"typo":1}`, "", 400, "unknown field"},
		{"invalid settings", "POST", "/api/sessions", `{"settings":{},"mutationId":"invalid-settings"}`, "", 400, "batch size"},
		{"cross-origin blocked", "POST", "/api/sessions", `{}`, "https://other.example", 403, "cross-origin"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			request := httptest.NewRequest(tt.method, "http://wren.test"+tt.path, strings.NewReader(tt.body))
			request.Header.Set("Content-Type", "application/json")
			if tt.origin != "" {
				request.Header.Set("Origin", tt.origin)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			assert.Equal(t, tt.status, response.Code)
			assert.Contains(t, response.Body.String(), tt.text)
		})
	}
}

func TestHTTPMutationRecovery(t *testing.T) {
	handler, st := testHandler(t)
	for _, tt := range []struct {
		name string
		run  func(*testing.T)
	}{
		{"start session request can be retried", func(t *testing.T) {
			body := `{"settings":{"level":1,"includeLower":true,"batchSize":1,"directions":["read"],"mode":"training"},"mutationId":"start-retry-over-http"}`
			var first study.Session
			for i := range 2 {
				request := httptest.NewRequest("POST", "/api/sessions", strings.NewReader(body))
				request.Header.Set("Content-Type", "application/json")
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				require.Equal(t, 200, response.Code, response.Body.String())
				var session study.Session
				require.NoError(t, json.Unmarshal(response.Body.Bytes(), &session))
				if i == 0 {
					first = session
				} else {
					assert.Equal(t, first.ID, session.ID)
					assert.Equal(t, first.Current.ID, session.Current.ID)
				}
			}
		}},
		{"storage failure is not success-shaped", func(t *testing.T) {
			require.NoError(t, st.Close())
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest("GET", "/api/settings", nil))
			assert.Equal(t, 500, response.Code)
			assert.Contains(t, response.Body.String(), "Could not save or load")
		}},
	} {
		t.Run(tt.name, tt.run)
	}
}
