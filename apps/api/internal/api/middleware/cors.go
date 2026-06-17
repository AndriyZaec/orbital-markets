package middleware

import "net/http"

// CORS allows credentialed cross-origin requests from a single trusted origin.
//
// Per the spec, when Access-Control-Allow-Credentials is true, the
// Allow-Origin header MUST be a specific origin (not `*`). We mirror the
// caller's Origin only if it matches the configured allowed origin.
//
// If allowedOrigin is empty, the middleware is a no-op (dev / same-origin
// reverse-proxy setups don't need CORS).
func CORS(allowedOrigin string) func(http.Handler) http.Handler {
	if allowedOrigin == "" {
		return func(next http.Handler) http.Handler { return next }
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin == allowedOrigin {
				w.Header().Set("Access-Control-Allow-Origin", allowedOrigin)
				w.Header().Set("Access-Control-Allow-Credentials", "true")
				w.Header().Set("Vary", "Origin")
			}
			if r.Method == http.MethodOptions {
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
				w.Header().Set("Access-Control-Max-Age", "86400")
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
