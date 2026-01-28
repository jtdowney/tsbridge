package web

import (
	"fmt"
	"sort"
	"time"

	"github.com/jtdowney/tsbridge/internal/config"
)

// ServiceInfo represents service information for the web interface.
type ServiceInfo struct {
	Name            string    `json:"name"`
	Status          string    `json:"status"` // "running", "stopped", "error"
	Backend         string    `json:"backend"`
	ListenerAddr    string    `json:"listener_addr"`
	TLSMode         string    `json:"tls_mode"`
	WhoisEnabled    bool      `json:"whois_enabled"`
	RequestCount    int64     `json:"request_count"`
	ErrorCount      int64     `json:"error_count"`
	AvgResponseTime float64   `json:"avg_response_time_ms"`
	LastActivity    time.Time `json:"last_activity"`
	Tags            []string  `json:"tags"`
}

// MetricsSummary provides an overview of system metrics.
type MetricsSummary struct {
	TotalServices     int       `json:"total_services"`
	ActiveServices    int       `json:"active_services"`
	TotalRequests     int64     `json:"total_requests"`
	TotalErrors       int64     `json:"total_errors"`
	ErrorRate         float64   `json:"error_rate"`
	AvgResponseTime   float64   `json:"avg_response_time_ms"`
	RequestsPerSecond float64   `json:"requests_per_second"`
	ActiveConnections int64     `json:"active_connections"`
	UptimeSeconds     int64     `json:"uptime_seconds"`
	LastUpdated       time.Time `json:"last_updated"`
}

// getServicesInfo retrieves information about all configured services.
func (s *Server) getServicesInfo() []ServiceInfo {
	var services []ServiceInfo

	if s.app == nil || s.app.GetConfig() == nil {
		return services
	}

	// Iterate through all configured services
	for _, svcCfg := range s.app.GetConfig().Services {
		service, err := s.getServiceInfo(svcCfg.Name)
		if err != nil {
			// Create a service info with error status
			service = &ServiceInfo{
				Name:    svcCfg.Name,
				Status:  "error",
				Backend: svcCfg.BackendAddr,
				TLSMode: svcCfg.TLSMode,
				Tags:    svcCfg.Tags,
			}
		}
		services = append(services, *service)
	}

	// Sort services alphabetically by name
	sort.Slice(services, func(i, j int) bool {
		return services[i].Name < services[j].Name
	})

	return services
}

// getServiceInfo retrieves detailed information about a specific service.
func (s *Server) getServiceInfo(name string) (*ServiceInfo, error) {
	if s.app == nil {
		return nil, fmt.Errorf("app not available")
	}

	// Find service configuration
	var svcConfig *config.Service
	appCfg := s.app.GetConfig()
	if appCfg == nil {
		return nil, fmt.Errorf("configuration not available")
	}
	for _, svcCfg := range appCfg.Services {
		if svcCfg.Name == name {
			svcConfig = &svcCfg
			break
		}
	}

	if svcConfig == nil {
		return nil, fmt.Errorf("service not found: %s", name)
	}

	// Get service metrics from the metrics collector
	var requestCount, errorCount int64
	var avgResponseTime float64

	registry := s.app.GetRegistry()
	if registry != nil {
		if collector := registry.GetMetricsCollector(); collector != nil {
			metrics := collector.GetServiceMetrics(name)
			requestCount = metrics.TotalRequests
			errorCount = metrics.TotalErrors
			avgResponseTime = metrics.AvgResponseTime
		}
	}

	// Determine service status - for now we'll assume all configured services are running
	// In the future, we can check if the service is actually running by trying to access it
	status := "running"

	// Try to get actual listener address (placeholder for now)
	listenerAddr := fmt.Sprintf("%s.%s", name, "ts.net") // Tailscale hostname format

	return &ServiceInfo{
		Name:            name,
		Status:          status,
		Backend:         svcConfig.BackendAddr,
		ListenerAddr:    listenerAddr,
		TLSMode:         svcConfig.TLSMode,
		WhoisEnabled:    svcConfig.WhoisEnabled != nil && *svcConfig.WhoisEnabled,
		RequestCount:    requestCount,
		ErrorCount:      errorCount,
		AvgResponseTime: avgResponseTime,
		LastActivity:    time.Now(), // placeholder
		Tags:            svcConfig.Tags,
	}, nil
}

// getMetricsSummary retrieves overall system metrics.
func (s *Server) getMetricsSummary() MetricsSummary {
	services := s.getServicesInfo()

	totalServices := len(services)
	activeServices := 0
	var totalRequests, totalErrors int64
	var totalResponseTime float64
	var servicesWithResponseTime int

	for _, service := range services {
		if service.Status == "running" {
			activeServices++
		}
		totalRequests += service.RequestCount
		totalErrors += service.ErrorCount
		if service.AvgResponseTime > 0 {
			totalResponseTime += service.AvgResponseTime
			servicesWithResponseTime++
		}
	}

	errorRate := 0.0
	if totalRequests > 0 {
		errorRate = float64(totalErrors) / float64(totalRequests) * 100
	}

	// Calculate overall average response time
	avgResponseTime := 0.0
	if servicesWithResponseTime > 0 {
		avgResponseTime = totalResponseTime / float64(servicesWithResponseTime)
	}

	// These are placeholders - would need additional tracking for accurate values
	requestsPerSecond := 0.0      // Would need rate tracking
	activeConnections := int64(0) // Would need connection tracking
	uptimeSeconds := int64(0)     // Would need startup time tracking

	return MetricsSummary{
		TotalServices:     totalServices,
		ActiveServices:    activeServices,
		TotalRequests:     totalRequests,
		TotalErrors:       totalErrors,
		ErrorRate:         errorRate,
		AvgResponseTime:   avgResponseTime,
		RequestsPerSecond: requestsPerSecond,
		ActiveConnections: activeConnections,
		UptimeSeconds:     uptimeSeconds,
		LastUpdated:       time.Now(),
	}
}
