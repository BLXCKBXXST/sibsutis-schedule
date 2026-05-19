package web

import (
	"context"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/BLXCKBXXST/sibsutis-schedule/internal/config"
	"github.com/BLXCKBXXST/sibsutis-schedule/internal/schedule"
	"github.com/BLXCKBXXST/sibsutis-schedule/internal/store"
)

// Server — HTTP-сервер расписания SibSUTI.
type Server struct {
	cfg    *config.Config
	svc    *schedule.Service
	store  *store.Store
	render *renderer
}

// New собирает Server. Шаблоны парсятся один раз при старте — на каждый
// запрос render лишь подставляет данные.
func New(cfg *config.Config, svc *schedule.Service, st *store.Store) (*Server, error) {
	r, err := newRenderer()
	if err != nil {
		return nil, err
	}
	return &Server{cfg: cfg, svc: svc, store: st, render: r}, nil
}

// Routes собирает маршруты и оборачивает их middleware.
// Используются паттерны Go 1.22+ (`GET /...`, `{q}`, `{$}`).
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleHome)
	mux.HandleFunc("POST /search", s.handleSearch)
	mux.HandleFunc("GET /schedule/{type}/{q}", s.handleSchedule)
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.Handle("GET /static/", http.StripPrefix("/static/", staticHandler()))
	return s.withMiddleware(mux)
}

// withMiddleware — логирование запроса и recover на случай panic'а в хэндлере.
func (s *Server) withMiddleware(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("panic in %s %s: %v", r.Method, r.URL.Path, rec)
				http.Error(w, "internal error", http.StatusInternalServerError)
			}
		}()
		start := time.Now()
		h.ServeHTTP(w, r)
		log.Printf("%s %s — %s", r.Method, r.URL.Path, time.Since(start))
	})
}

// ListenAndServe запускает сервер; останавливается на ctx.Done() с graceful shutdown.
func (s *Server) ListenAndServe(ctx context.Context) error {
	srv := &http.Server{
		Addr:              s.cfg.WebListenAddr,
		Handler:           s.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
	}

	done := make(chan error, 1)
	go func() {
		log.Printf("web: listening on %s", s.cfg.WebListenAddr)
		done <- srv.ListenAndServe()
	}()

	select {
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		log.Print("web: shutting down")
		return srv.Shutdown(shutdownCtx)
	}
}
