package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// handleDashboard serves the main dashboard page.
func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	data := struct {
		Title    string
		Services []ServiceInfo
		Metrics  MetricsSummary
	}{
		Title:    "tsbridge Dashboard",
		Services: s.getServicesInfo(),
		Metrics:  s.getMetricsSummary(),
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.renderTemplate(w, "dashboard", data); err != nil {
		http.Error(w, "Failed to render dashboard", http.StatusInternalServerError)
		return
	}
}

// handlePartialServices returns the services list HTML partial for HTMX updates.
func (s *Server) handlePartialServices(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	data := struct {
		Services []ServiceInfo
	}{
		Services: s.getServicesInfo(),
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.templates.ExecuteTemplate(w, "services-list", data); err != nil {
		http.Error(w, "Failed to render services list", http.StatusInternalServerError)
		return
	}
}

// handleAPIServices returns JSON data about all services.
func (s *Server) handleAPIServices(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	services := s.getServicesInfo()

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(services); err != nil {
		http.Error(w, "Failed to encode services", http.StatusInternalServerError)
		return
	}
}

// handleAPIServiceDetail returns JSON data about a specific service.
func (s *Server) handleAPIServiceDetail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract service name from URL path
	path := strings.TrimPrefix(r.URL.Path, "/api/services/")
	serviceName := strings.TrimSuffix(path, "/")

	if serviceName == "" {
		http.Error(w, "Service name required", http.StatusBadRequest)
		return
	}

	service, err := s.getServiceInfo(serviceName)
	if err != nil {
		http.Error(w, "Service not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(service); err != nil {
		http.Error(w, "Failed to encode service", http.StatusInternalServerError)
		return
	}
}

// handleAPIMetricsSummary returns JSON metrics summary.
func (s *Server) handleAPIMetricsSummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	metrics := s.getMetricsSummary()

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(metrics); err != nil {
		http.Error(w, "Failed to encode metrics", http.StatusInternalServerError)
		return
	}
}

// renderTemplate renders a template with the given name and data.
func (s *Server) renderTemplate(w http.ResponseWriter, name string, data any) error {
	if s.templates == nil {
		return fmt.Errorf("templates not loaded")
	}

	// Currently only the dashboard template is supported
	if name == "dashboard" {
		return s.templates.ExecuteTemplate(w, "layout.html", data)
	}

	return fmt.Errorf("unknown template: %s", name)
}
