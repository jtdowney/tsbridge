// Package web provides a monitoring web interface for tsbridge.
// It implements a read-only dashboard with service status, metrics, and logs.
package web

import (
	"context"
	"fmt"
	"html/template"
	"net/http"
	"path/filepath"
	"time"

	"github.com/jtdowney/tsbridge/internal/config"
	"github.com/jtdowney/tsbridge/internal/service"
)

// Server represents the web interface HTTP server.
type Server struct {
	addr      string
	app       Application
	server    *http.Server
	templates *template.Template
}

// Application defines the interface for accessing application data.
type Application interface {
	GetConfig() *config.Config
	GetRegistry() *service.Registry
}

// NewServer creates a new web interface server.
func NewServer(addr string, app Application) (*Server, error) {
	if addr == "" {
		return nil, fmt.Errorf("web server address cannot be empty")
	}

	s := &Server{
		addr: addr,
		app:  app,
	}

	// Load templates
	if err := s.loadTemplates(); err != nil {
		return nil, fmt.Errorf("failed to load templates: %w", err)
	}

	// Setup HTTP server
	mux := http.NewServeMux()
	s.setupRoutes(mux)

	s.server = &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	return s, nil
}

// Start starts the web server.
func (s *Server) Start() error {
	return s.server.ListenAndServe()
}

// Stop gracefully stops the web server.
func (s *Server) Stop(ctx context.Context) error {
	return s.server.Shutdown(ctx)
}

// loadTemplates loads all HTML templates from the templates directory.
func (s *Server) loadTemplates() error {
	// Parse all template files
	templatesPattern := filepath.Join("internal", "web", "templates", "*.html")

	templates := template.New("web")

	// Try to parse main templates
	if mainTemplates, err := template.ParseGlob(templatesPattern); err == nil && len(mainTemplates.Templates()) > 0 {
		for _, tmpl := range mainTemplates.Templates() {
			if tmpl.Name() != "web" { // Skip the root template
				templates = template.Must(templates.AddParseTree(tmpl.Name(), tmpl.Tree))
			}
		}
	}

	s.templates = templates
	return nil
}

// setupRoutes configures all HTTP routes for the web interface.
func (s *Server) setupRoutes(mux *http.ServeMux) {
	// Static files
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("internal/web/static"))))

	// Main dashboard
	mux.HandleFunc("/", s.handleDashboard)

	// HTML partials for HTMX
	mux.HandleFunc("/partials/services", s.handlePartialServices)

	// JSON API endpoints
	mux.HandleFunc("/api/services", s.handleAPIServices)
	mux.HandleFunc("/api/services/", s.handleAPIServiceDetail)
	mux.HandleFunc("/api/metrics/summary", s.handleAPIMetricsSummary)
}
