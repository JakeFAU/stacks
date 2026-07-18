package httpapi

import (
	"encoding/json"
	"net/http"
)

const contentTypeJSON = "application/json"

type healthResponse struct {
	Status string `json:"status"`
}

// NewHandler returns the public HTTP API.
func NewHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", health)
	return mux
}

func health(response http.ResponseWriter, _ *http.Request) {
	response.Header().Set("Content-Type", contentTypeJSON)
	response.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(response).Encode(healthResponse{Status: "ok"})
}
