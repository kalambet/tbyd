package api

import (
	"net/http"
	"strings"
)

func BearerAuth(token string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			auth := r.Header.Get("Authorization")
			if !strings.HasPrefix(auth, "Bearer ") || strings.TrimPrefix(auth, "Bearer ") != token {
				httpError(w, http.StatusUnauthorized, "authentication_error", "invalid or missing bearer token")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
