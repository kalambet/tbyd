package api

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

func BearerAuth(token string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			auth := r.Header.Get("Authorization")
			const prefix = "Bearer "
			if !strings.HasPrefix(auth, prefix) || subtle.ConstantTimeCompare([]byte(auth[len(prefix):]), []byte(token)) != 1 {
				httpError(w, http.StatusUnauthorized, "authentication_error", "invalid or missing bearer token")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
