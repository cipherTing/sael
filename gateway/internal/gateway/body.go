package gateway

import (
	"errors"
	"net/http"
)

var errSecurityUnavailable = errors.New("security store unavailable")

func (s *Server) limitBody(w http.ResponseWriter, r *http.Request) bool {
	if r.ContentLength > s.MaxBodyBytes {
		http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, s.MaxBodyBytes)
	return true
}
func writeBodyError(w http.ResponseWriter, err error) {
	var large *http.MaxBytesError
	if errors.As(err, &large) {
		http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		return
	}
	http.Error(w, "unable to read request", http.StatusBadRequest)
}
