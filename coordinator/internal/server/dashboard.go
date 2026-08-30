package server

import (
	_ "embed"
	"net/http"
)

//go:embed dashboard.html
var dashboardHTML []byte

// handleDashboard serves the built-in operator console at "/".
func handleDashboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(dashboardHTML)
}
