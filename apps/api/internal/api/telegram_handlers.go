package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/telegrambot"
)

const maxTelegramAccountLength = 128

type TelegramLinker interface {
	CreateLinkIntent(context.Context, string, string) (string, time.Time, error)
}

func (s *Server) handleTelegramLinkIntent(w http.ResponseWriter, r *http.Request) {
	var request struct {
		AccountPacifica    string `json:"account_pacifica"`
		AccountHyperliquid string `json:"account_hyperliquid"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	request.AccountPacifica = strings.TrimSpace(request.AccountPacifica)
	request.AccountHyperliquid = strings.TrimSpace(request.AccountHyperliquid)
	if request.AccountPacifica == "" || request.AccountHyperliquid == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "both venue accounts are required"})
		return
	}
	if len(request.AccountPacifica) > maxTelegramAccountLength || len(request.AccountHyperliquid) > maxTelegramAccountLength {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid venue account"})
		return
	}

	linkURL, expiresAt, err := s.telegramLinks.CreateLinkIntent(
		r.Context(), request.AccountPacifica, request.AccountHyperliquid,
	)
	if err != nil {
		if errors.Is(err, telegrambot.ErrLinkRateLimited) {
			w.Header().Set("Retry-After", "1")
			writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many Telegram link requests"})
			return
		}
		s.logger.Error("create Telegram link intent", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create Telegram link"})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"url":        linkURL,
		"expires_at": expiresAt.UTC().Format(time.RFC3339),
	})
}
