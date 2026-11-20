package main

import "net/http"

// Unwrap lets http.ResponseController reach the writer statusRecorder wraps.
// requestLogger wraps every response in one, and without it a handler that
// moves its write deadline, as an import confirm does, finds the move not
// supported and keeps WRITE_TIMEOUT.
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }
