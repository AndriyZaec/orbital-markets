package api

import (
	"crypto/subtle"
	"net/http"
	"strings"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/analytics"
)

const liveMetricsCacheTTL = time.Minute

func (s *Server) handleLiveAnalytics(w http.ResponseWriter, r *http.Request) {
	if !s.analyticsTokenMatches(r) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}

	now := time.Now()
	s.metricsMu.Lock()
	if s.metricsCache != nil && now.Sub(s.metricsCachedAt) < liveMetricsCacheTTL {
		cached := s.metricsCache
		s.metricsMu.Unlock()
		writeJSON(w, http.StatusOK, cached)
		return
	}
	s.metricsMu.Unlock()

	metrics, err := analytics.LoadLiveMetrics(r.Context(), s.db, now)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "unable to load live analytics"})
		return
	}

	s.metricsMu.Lock()
	s.metricsCache = metrics
	s.metricsCachedAt = now
	s.metricsMu.Unlock()
	writeJSON(w, http.StatusOK, metrics)
}

func (s *Server) analyticsTokenMatches(r *http.Request) bool {
	expected := strings.TrimSpace(s.analyticsAccessToken)
	if expected == "" {
		return true
	}
	actual := strings.TrimSpace(r.Header.Get("X-Analytics-Token"))
	if actual == "" {
		return false
	}
	return len(actual) == len(expected) && subtle.ConstantTimeCompare([]byte(actual), []byte(expected)) == 1
}
