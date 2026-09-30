package api

import (
	"crypto/subtle"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/analytics"
)

const liveMetricsCacheTTL = time.Minute
const weeklyAPRWeeks = 12

func (s *Server) handleLiveAnalytics(w http.ResponseWriter, r *http.Request) {
	if !s.analyticsTokenMatches(r) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}

	metrics, err := s.liveMetrics(r)
	if err != nil {
		s.logger.Error("live analytics: load failed", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "unable to load live analytics"})
		return
	}
	writeJSON(w, http.StatusOK, metrics)
}

func (s *Server) handleWeeklyAPR(w http.ResponseWriter, r *http.Request) {
	if !s.analyticsTokenMatches(r) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}

	database, err := requireMetricsDatabase(s.metricsDatabase())
	if err != nil {
		s.logger.Error("weekly APR: load failed", "err", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "weekly APR temporarily unavailable"})
		return
	}
	report, err := analytics.LoadWeeklyAPR(r.Context(), database, time.Now(), weeklyAPRWeeks)
	if err != nil {
		s.logger.Error("weekly APR: load failed", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "unable to load weekly APR"})
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func (s *Server) handlePublicMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Del("Access-Control-Allow-Credentials")
	w.Header().Set("Cache-Control", "public, max-age=60")
	metrics, err := s.liveMetrics(r)
	if err != nil {
		s.logPublicMetricsError(err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "unable to load public metrics"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"total_volume": fmt.Sprintf("%.2f", metrics.Volume.AllTime.GrossVenueVolume),
	})
}

func (s *Server) logPublicMetricsError(err error) {
	now := time.Now()
	s.metricsMu.Lock()
	defer s.metricsMu.Unlock()
	if now.Sub(s.publicMetricsLogAt) < liveMetricsCacheTTL {
		return
	}
	s.publicMetricsLogAt = now
	s.logger.Error("public metrics: load failed", "err", err)
}

func (s *Server) liveMetrics(r *http.Request) (*analytics.LiveMetrics, error) {
	now := time.Now()
	s.metricsMu.Lock()
	defer s.metricsMu.Unlock()
	if s.metricsCache != nil && now.Sub(s.metricsCachedAt) < liveMetricsCacheTTL {
		return s.metricsCache, nil
	}

	database, err := requireMetricsDatabase(s.metricsDatabase())
	if err != nil {
		return nil, err
	}
	metrics, err := analytics.LoadLiveMetrics(r.Context(), database, now)
	if err != nil {
		return nil, err
	}

	s.metricsCache = metrics
	s.metricsCachedAt = now
	return metrics, nil
}

func (s *Server) metricsDatabase() *sql.DB {
	if s.analyticsDBDisabled {
		return nil
	}
	return s.analyticsDB
}

func requireMetricsDatabase(database *sql.DB) (*sql.DB, error) {
	if database == nil {
		return nil, errors.New("isolated analytics database unavailable")
	}
	return database, nil
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
