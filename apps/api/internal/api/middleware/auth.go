// Package middleware provides HTTP middlewares for the closed-beta API:
// inline HS256 JWT auth (cookie- or Bearer-based), request logging, and
// panic recovery. Auth failures respond 404 to avoid advertising routes.
package middleware

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

const (
	cookieName     = "__beta"
	bearerPrefix   = "Bearer "
	healthPath     = "/api/v1/health"
	expectedAlgHdr = "HS256"
)

var (
	errMalformed = errors.New("malformed token")
	errBadAlg    = errors.New("unsupported alg")
	errBadSig    = errors.New("bad signature")
	errExpired   = errors.New("expired")
)

// Auth returns middleware that verifies the __beta cookie (or Authorization:
// Bearer header) against secret using HS256. On failure it responds 404 — the
// gate is meant to be invisible to unauthorized clients. /api/v1/health is
// always allowed through for CF and Fly health checks.
//
// If secret is empty the middleware is a no-op (dev bypass). Production wiring
// must ensure a secret is set.
func Auth(secret string, logger *slog.Logger) func(http.Handler) http.Handler {
	if secret == "" {
		logger.Warn("auth middleware disabled (empty JWT_SECRET) — DEV ONLY")
		return func(next http.Handler) http.Handler { return next }
	}
	key := []byte(secret)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == healthPath {
				next.ServeHTTP(w, r)
				return
			}
			token := extractToken(r)
			if token == "" {
				http.NotFound(w, r)
				return
			}
			if err := verifyHS256(token, key); err != nil {
				http.NotFound(w, r)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func extractToken(r *http.Request) string {
	if c, err := r.Cookie(cookieName); err == nil && c.Value != "" {
		return c.Value
	}
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, bearerPrefix) {
		return strings.TrimPrefix(h, bearerPrefix)
	}
	return ""
}

func verifyHS256(token string, key []byte) error {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return errMalformed
	}

	hdr, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return errMalformed
	}
	var h struct {
		Alg string `json:"alg"`
	}
	if err := json.Unmarshal(hdr, &h); err != nil {
		return errMalformed
	}
	if h.Alg != expectedAlgHdr {
		return errBadAlg
	}

	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return errMalformed
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(parts[0] + "." + parts[1]))
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return errBadSig
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return errMalformed
	}
	var c struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &c); err != nil {
		return errMalformed
	}
	if c.Exp != 0 && time.Now().Unix() > c.Exp {
		return errExpired
	}
	return nil
}
