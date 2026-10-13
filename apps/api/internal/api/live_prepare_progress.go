package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

const livePrepareTimeout = 15 * time.Second

// Progress is opt-in on the same prepare request, so it creates neither an
// extra request nor a polling loop. Legacy callers still receive normal JSON.
// Once streaming begins the HTTP status is 200; the result carries its own status.
type livePrepareResponse struct {
	http.ResponseWriter
	ctx           context.Context
	logger        *slog.Logger
	stream        bool
	headers       http.Header
	status        int
	started       time.Time
	stage         string
	stageTime     time.Time
	requestID     string
	opportunityID string
}

func newLivePrepareResponse(w http.ResponseWriter, r *http.Request, logger *slog.Logger) *livePrepareResponse {
	if logger == nil {
		logger = slog.Default()
	}
	return &livePrepareResponse{
		ResponseWriter: w, ctx: r.Context(), logger: logger,
		stream:  strings.Contains(r.Header.Get("Accept"), "application/x-ndjson"),
		headers: make(http.Header), status: http.StatusOK, started: time.Now(),
		requestID: uuid.NewString(),
	}
}

func (p *livePrepareResponse) Header() http.Header {
	if p.stream {
		return p.headers
	}
	return p.ResponseWriter.Header()
}

func (p *livePrepareResponse) WriteHeader(status int) { p.status = status }

func (p *livePrepareResponse) Write(body []byte) (int, error) {
	n := len(body)
	if p.status >= 400 && p.ctx.Err() == context.DeadlineExceeded {
		p.status = http.StatusGatewayTimeout
		body = []byte(`{"error":"Preparation timed out. Refresh the opportunity and try again.","code":"PREPARE_TIMEOUT","retryable":true}`)
	}
	if !p.stream {
		p.ResponseWriter.WriteHeader(p.status)
		_, err := p.ResponseWriter.Write(body)
		return n, err
	}
	err := json.NewEncoder(p.ResponseWriter).Encode(map[string]any{
		"type": "result", "status": p.status, "body": json.RawMessage(body),
	})
	_ = http.NewResponseController(p.ResponseWriter).Flush()
	return n, err
}

func (p *livePrepareResponse) Stage(stage string) {
	p.logStage()
	p.stage, p.stageTime = stage, time.Now()
	if !p.stream {
		return
	}
	p.ResponseWriter.Header().Set("Content-Type", "application/x-ndjson")
	p.ResponseWriter.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(p.ResponseWriter).Encode(map[string]string{"type": "progress", "stage": stage})
	_ = http.NewResponseController(p.ResponseWriter).Flush()
}

func (p *livePrepareResponse) logStage() {
	if p.stage != "" {
		p.logger.Info("live prepare: stage completed", "request_id", p.requestID, "opportunity_id", p.opportunityID, "stage", p.stage, "dur_ms", time.Since(p.stageTime).Milliseconds())
	}
}

func (p *livePrepareResponse) Finish() {
	p.logStage()
	p.logger.Info("live prepare: completed", "request_id", p.requestID, "opportunity_id", p.opportunityID, "status", p.status, "dur_ms", time.Since(p.started).Milliseconds())
}
