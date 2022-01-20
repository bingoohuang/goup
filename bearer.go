package goup

import (
	"crypto/rand"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
)

const bearerPrefix = "Bearer "

// Bearer returns a Handler that authenticates via Bearer Auth. Writes a http.StatusUnauthorized
// if authentication fails.
func Bearer(token string, handle http.HandlerFunc) http.HandlerFunc {
	if token == "" {
		return handle
	}

	return func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get(Authorization)
		if SecureCompare(auth, bearerPrefix+token) {
			handle(w, r)
		} else {
			bearerUnauthorized(w)
		}
	}
}

// SecureCompare performs a constant time compare of two strings to limit timing attacks.
func SecureCompare(given string, actual string) bool {
	givenSha := sha512.Sum512([]byte(given))
	actualSha := sha512.Sum512([]byte(actual))

	return subtle.ConstantTimeCompare(givenSha[:], actualSha[:]) == 1
}

func bearerUnauthorized(res http.ResponseWriter) {
	http.Error(res, "Not Authorized", http.StatusUnauthorized)
}

// BearerTokenGenerate generates a random bearer token.
func BearerTokenGenerate() string {
	b := make([]byte, 15)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
