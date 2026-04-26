package web

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"crystal-disk-info-mp/internal/smart"
)

type Server struct {
	collector smart.Collector
	mu        sync.Mutex
	disks     []smart.RawDisk
	lastErr   string
	loading   bool
}

type response struct {
	Disks   []smart.RawDisk `json:"disks,omitempty"`
	Error   string          `json:"error,omitempty"`
	Loading bool            `json:"loading,omitempty"`
	Themes  []string        `json:"themes,omitempty"`
}

type refreshRequest struct {
	Force bool   `json:"force"`
	ID    string `json:"id"`
	Index *int   `json:"index"`
}

func NewServer(collector smart.Collector) *Server {
	return &Server{collector: collector}
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
	s.writeState(w, false)
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
	s.Refresh(req.Force, req.ID, req.Index)
	s.writeState(w, false)
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
	s.writeJSON(w, response{Themes: s.themes()})
}

func (s *Server) Refresh(force bool, id string, index *int) {
	s.mu.Lock()
	s.loading = true
	s.mu.Unlock()

	if id == "" && index == nil {
		disks, err := s.collector.Scan(force)
		s.mu.Lock()
		s.disks = mergeByID(s.disks, disks)
		s.lastErr = ""
		if err != nil {
			s.lastErr = err.Error()
		}
		s.loading = false
		s.mu.Unlock()
		return
	}

	target, previous, ok := s.findTarget(id, index)
	if !ok {
		s.mu.Lock()
		s.lastErr = "disk not found"
		s.loading = false
		s.mu.Unlock()
		return
	}
	disk, err := s.collector.Read(target, force, previous)
	s.mu.Lock()
	s.lastErr = ""
	if err != nil {
		s.lastErr = err.Error()
	} else {
		s.replaceDisk(disk)
	}
	s.loading = false
	s.mu.Unlock()
}

func (s *Server) findTarget(id string, index *int) (int, *smart.RawDisk, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.disks {
		if (id != "" && s.disks[i].ID == id) || (index != nil && s.disks[i].Index == *index) {
			prev := s.disks[i]
			return s.disks[i].Index, &prev, true
		}
	}
	if index != nil {
		return *index, nil, true
	}
	return 0, nil, false
}

func (s *Server) replaceDisk(disk smart.RawDisk) {
	for i := range s.disks {
		if s.disks[i].ID == disk.ID || s.disks[i].Index == disk.Index {
			s.disks[i] = disk
			return
		}
	}
	s.disks = append(s.disks, disk)
}

func mergeByID(old, next []smart.RawDisk) []smart.RawDisk {
	byID := make(map[string]smart.RawDisk, len(old))
	for _, disk := range old {
		byID[disk.ID] = disk
	}
	for i := range next {
		prev, ok := byID[next[i].ID]
		if ok && next[i].SmartState == smart.SmartStateAsleep {
			if len(next[i].Raw.SmartReadData) == 0 {
				next[i].Raw.SmartReadData = prev.Raw.SmartReadData
			}
			if len(next[i].Raw.SmartReadThreshold) == 0 {
				next[i].Raw.SmartReadThreshold = prev.Raw.SmartReadThreshold
			}
			if next[i].LastSmartAt == "" {
				next[i].LastSmartAt = prev.LastSmartAt
			}
		}
	}
	return next
}

func (s *Server) writeState(w http.ResponseWriter, includeThemes bool) {
	s.mu.Lock()
	res := response{
		Disks:   append([]smart.RawDisk(nil), s.disks...),
		Error:   s.lastErr,
		Loading: s.loading,
	}
	s.mu.Unlock()
	if includeThemes {
		res.Themes = s.themes()
	}
	s.writeJSON(w, res)
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
