package api

import (
	"crypto/subtle"
	"fmt"
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

	metrics, err := s.liveMetrics(r)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "unable to load live analytics"})
		return
	}
	writeJSON(w, http.StatusOK, metrics)
}

func (s *Server) handlePublicMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Del("Access-Control-Allow-Credentials")
	w.Header().Set("Cache-Control", "public, max-age=60")
	metrics, err := s.liveMetrics(r)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "unable to load public metrics"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"total_volume": fmt.Sprintf("%.2f", metrics.Volume.AllTime.GrossVenueVolume),
	})
}

func (s *Server) liveMetrics(r *http.Request) (*analytics.LiveMetrics, error) {
	now := time.Now()
	s.metricsMu.Lock()
	if s.metricsCache != nil && now.Sub(s.metricsCachedAt) < liveMetricsCacheTTL {
		cached := s.metricsCache
		s.metricsMu.Unlock()
		return cached, nil
	}
	s.metricsMu.Unlock()

	metrics, err := analytics.LoadLiveMetrics(r.Context(), s.db, now)
	if err != nil {
		return nil, err
	}

	s.metricsMu.Lock()
	s.metricsCache = metrics
	s.metricsCachedAt = now
	s.metricsMu.Unlock()
	return metrics, nil
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
