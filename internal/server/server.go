package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/corani/wren/internal/store"
	"github.com/corani/wren/internal/study"
)

type Server struct {
	store  *store.Store
	log    *slog.Logger
	assets fs.FS
}

func New(st *store.Store, log *slog.Logger, assets fs.FS) *Server {
	return &Server{store: st, log: log, assets: assets}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/settings", s.handle(func(r *http.Request) (any, error) {
		settings, err := s.store.Settings(r.Context())
		if err != nil {
			return nil, err
		}
		levels, err := s.store.Levels(r.Context())
		return struct {
			Settings study.Settings `json:"settings"`
			Levels   []study.Level  `json:"levels"`
		}{settings, levels}, err
	}))
	mux.HandleFunc("PUT /api/settings", s.handle(func(r *http.Request) (any, error) {
		var settings study.Settings
		if err := decode(r, &settings); err != nil {
			return nil, err
		}
		return settings, s.store.SaveSettings(r.Context(), settings)
	}))
	mux.HandleFunc("GET /api/sessions/current", s.handle(func(r *http.Request) (any, error) {
		return s.store.Current(r.Context())
	}))
	mux.HandleFunc("POST /api/sessions", s.handle(func(r *http.Request) (any, error) {
		var input struct {
			Settings   study.Settings `json:"settings"`
			MutationID string         `json:"mutationId"`
		}
		if err := decode(r, &input); err != nil {
			return nil, err
		}
		return s.store.Start(r.Context(), input.Settings, input.MutationID)
	}))
	mux.HandleFunc("GET /api/sessions/{id}", s.handle(func(r *http.Request) (any, error) {
		id, err := sessionID(r)
		if err != nil {
			return nil, err
		}
		return s.store.Session(r.Context(), id)
	}))
	mux.HandleFunc("POST /api/sessions/{id}/{action}", s.handle(func(r *http.Request) (any, error) {
		id, err := sessionID(r)
		if err != nil {
			return nil, err
		}
		var input study.Mutation
		if err := decode(r, &input); err != nil {
			return nil, err
		}
		return s.store.Act(r.Context(), id, r.PathValue("action"), input)
	}))
	mux.HandleFunc("/api/", func(w http.ResponseWriter, _ *http.Request) {
		s.respond(w, http.StatusNotFound, map[string]string{"error": "API route not found"})
	})
	assets := http.FileServerFS(s.assets)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			s.respond(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		assets.ServeHTTP(w, r)
	})
	protected := http.NewCrossOriginProtection().Handler(mux)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self'; script-src 'self'; img-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		if len(r.URL.Path) >= 5 && r.URL.Path[:5] == "/api/" {
			w.Header().Set("Cache-Control", "no-store")
		}
		protected.ServeHTTP(w, r)
	})
}

func sessionID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		return 0, fmt.Errorf("%w: invalid session ID", study.ErrInvalid)
	}
	return id, nil
}

func decode(r *http.Request, value any) error {
	if r.Header.Get("Content-Type") != "application/json" {
		return fmt.Errorf("%w: Content-Type must be application/json", study.ErrInvalid)
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, 16*1024+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return fmt.Errorf("%w: invalid JSON: %v", study.ErrInvalid, err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("%w: request must contain one JSON object", study.ErrInvalid)
	}
	return nil
}

func (s *Server) handle(fn func(*http.Request) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		result, err := fn(r)
		if err == nil {
			s.respond(w, http.StatusOK, result)
			return
		}
		status := http.StatusInternalServerError
		message := "Could not save or load your progress. Please retry."
		switch {
		case errors.Is(err, study.ErrInvalid):
			status, message = http.StatusBadRequest, err.Error()
		case errors.Is(err, study.ErrConflict):
			status, message = http.StatusConflict, err.Error()
		case errors.Is(err, study.ErrNotFound):
			status, message = http.StatusNotFound, err.Error()
		default:
			s.log.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
		}
		s.respond(w, status, map[string]string{"error": message})
	}
}

func (s *Server) respond(w http.ResponseWriter, status int, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		s.log.Error("encode response", "err", err)
		http.Error(w, "Could not encode response", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if _, err := w.Write(data); err != nil {
		s.log.Error("write response", "err", err)
	}
}
