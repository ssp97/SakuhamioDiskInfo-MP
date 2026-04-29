package web

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"crystal-disk-info-mp/internal/db"
	"crystal-disk-info-mp/internal/smart"
)

type Server struct {
	monitor  *Monitor
	database *db.DB
}

type response struct {
	Disks       []smart.RawDisk `json:"disks,omitempty"`
	Error       string          `json:"error,omitempty"`
	Loading     bool            `json:"loading,omitempty"`
	LastUpdated string          `json:"lastUpdated,omitempty"`
	Themes      []string        `json:"themes,omitempty"`
}

type refreshRequest struct {
	Force bool   `json:"force"`
	ID    string `json:"id"`
	Index *int   `json:"index"`
}

func NewServer(monitor *Monitor, database *db.DB) *Server {
	return &Server{monitor: monitor, database: database}
}

var staticRoot string

func SetupStaticDir(path string) {
	if path != "" {
		staticRoot = path
		return
	}
	exe, err := os.Executable()
	if err != nil {
		log.Fatalf("get executable path: %v", err)
	}
	staticRoot = filepath.Join(filepath.Dir(exe), "static")
}

func staticHandle(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if path == "/" {
		path = "/index.html"
	}
	localPath := filepath.Join(staticRoot, path)
	_, err := os.Stat(localPath)
	if err == nil {
		http.ServeFile(w, r, localPath)
		return
	}
	http.ServeFileFS(w, r, StaticFiles, "static"+path)
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", staticHandle)
	mux.HandleFunc("/api/disks", s.apiDisks)
	mux.HandleFunc("/api/refresh", s.apiRefresh)
	mux.HandleFunc("/api/themes", s.apiThemes)
	mux.HandleFunc("/api/temperature/history", s.apiTemperatureHistory)
	mux.HandleFunc("/api/temperature/current", s.apiTemperatureCurrent)
	return withCORS(mux)
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) apiDisks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	disks := s.monitor.GetDisks()
	lastUpdate := s.monitor.GetLastUpdate()
	res := response{
		Disks:       disks,
		LastUpdated: lastUpdate.Format("2006-01-02T15:04:05Z07:00"),
	}
	s.writeJSON(w, res)
}

func (s *Server) apiRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	req, err := parseRefreshRequest(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.monitor.ForceRefresh(req.Force, req.ID, req.Index)
	disks := s.monitor.GetDisks()
	res := response{
		Disks:       disks,
		LastUpdated: s.monitor.GetLastUpdate().Format("2006-01-02T15:04:05Z07:00"),
	}
	s.writeJSON(w, res)
}

func (s *Server) apiTemperatureHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	q := r.URL.Query()
	diskID := strings.TrimSpace(q.Get("disk"))
	if diskID == "" {
		http.Error(w, "missing disk parameter", http.StatusBadRequest)
		return
	}
	rangeParam := q.Get("range")
	if rangeParam == "" {
		rangeParam = "24h"
	}
	limitStr := strings.TrimSpace(q.Get("limit"))
	limit := 0
	if limitStr != "" {
		limit, _ = strconv.Atoi(limitStr)
	}

	records, err := s.database.QueryTemperatureHistory(db.HistoryQuery{
		DiskID: diskID,
		Range:  rangeParam,
		Limit:  limit,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	type recordJSON struct {
		RecordedAt  string  `json:"recordedAt"`
		MaxTemp     float64 `json:"maxTemp"`
		AvgTemp     float64 `json:"avgTemp"`
		MinTemp     float64 `json:"minTemp"`
		SampleCount int     `json:"sampleCount"`
	}

	jsonRecords := make([]recordJSON, 0, len(records))
	for _, rec := range records {
		jsonRecords = append(jsonRecords, recordJSON{
			RecordedAt:  rec.RecordedAt.Format("2006-01-02T15:04:05Z07:00"),
			MaxTemp:     rec.MaxTemp,
			AvgTemp:     rec.AvgTemp,
			MinTemp:     rec.MinTemp,
			SampleCount: rec.Samples,
		})
	}

	s.writeJSON(w, map[string]any{
		"diskId":  diskID,
		"records": jsonRecords,
	})
}

func (s *Server) apiTemperatureCurrent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	temps := s.monitor.CurrentTemps()
	s.writeJSON(w, map[string]any{
		"disks": temps,
	})
}

func parseRefreshRequest(r *http.Request) (refreshRequest, error) {
	q := r.URL.Query()
	req := refreshRequest{Force: parseBool(q.Get("force"))}
	if id := strings.TrimSpace(q.Get("id")); id != "" {
		req.ID = id
	}
	if indexText := strings.TrimSpace(q.Get("index")); indexText != "" {
		index, err := strconv.Atoi(indexText)
		if err != nil {
			return req, err
		}
		req.Index = &index
	}
	if r.Body != nil && strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		defer r.Body.Close()
		var body refreshRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			return req, err
		}
		if body.Force {
			req.Force = true
		}
		if body.ID != "" {
			req.ID = body.ID
		}
		if body.Index != nil {
			req.Index = body.Index
		}
	}
	return req, nil
}

func parseBool(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return value == "1" || value == "true" || value == "yes"
}

func (s *Server) apiThemes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	themeFile := filepath.Join(staticRoot, "themes", "themes.json")
	data, err := os.ReadFile(themeFile)
	if err == nil {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Write(data)
		return
	}
	data, err = StaticFiles.ReadFile("static/themes/themes.json")
	if err == nil {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Write(data)
		return
	}
	s.writeJSON(w, map[string]any{
		"Default": "Plain",
		"Themes":  map[string]any{},
	})
}

func (s *Server) writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) themes() []string {
	themePath := staticRoot + "/themes"
	entries, err := os.ReadDir(themePath)
	if err != nil {
		return nil
	}
	themes := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		themes = append(themes, name)
	}
	return themes
}
