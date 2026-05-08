package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"
)

var (
	Version      = "v0.2.4"
	manifestPath = os.Getenv("MANIFEST_PATH")
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	if manifestPath == "" {
		manifestPath = "./release/phone-talk-v0.1.0/manifest.json"
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/health", healthHandler)
	mux.HandleFunc("/v1/release/latest", releaseHandler)
	mux.HandleFunc("/v1/install.sh", installScriptHandler)
	mux.HandleFunc("/v1/telemetry", telemetryHandler)

	addr := ":" + port
	log.Printf("[api] PhoneTalk API %s listening on %s", Version, addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("[api] server failed: %v", err)
	}
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":    "ok",
		"version":   Version,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})
}

func releaseHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	data, err := os.ReadFile(manifestPath)
	if err != nil {
		http.Error(w, `{"error":"manifest not found"}`, http.StatusNotFound)
		return
	}
	w.Write(data)
}

func installScriptHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/x-shellscript")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	data, err := os.ReadFile("scripts/install.sh")
	if err != nil {
		data, err = os.ReadFile("/opt/phonetalk/releases/latest/install.sh")
		if err != nil {
			http.Error(w, "install script not found", http.StatusInternalServerError)
			return
		}
	}
	w.Write(data)
}

func telemetryHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	var payload map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}

	// TODO: Store telemetry data
	log.Printf("[telemetry] received report: installId=%v", payload["installId"])

	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok": true,
	})
}
