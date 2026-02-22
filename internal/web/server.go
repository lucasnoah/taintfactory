package web

import (
	"embed"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/lucasnoah/taintfactory/internal/config"
	"github.com/lucasnoah/taintfactory/internal/db"
	"github.com/lucasnoah/taintfactory/internal/pipeline"
)

//go:embed templates
var templateFS embed.FS

var funcMap = template.FuncMap{
	"badgeClass": func(status string) string {
		return "badge badge-" + strings.ReplaceAll(status, "_", "-")
	},
	"dotClass": func(color string) string {
		return "dot dot-" + color
	},
	"segClass": func(status string) string {
		return "seg seg-" + status
	},
	"passClass": func(passed bool) string {
		if passed {
			return "result-pass"
		}
		return "result-fail"
	},
	"relTime": relTime,
}

// Server is the read-only web UI server.
type Server struct {
	store *pipeline.Store
	db    *db.DB
	port  int

	// cfgCache maps repo root dir -> loaded config (nil if pipeline.yaml not found there).
	// wtCache maps worktree path -> repo root dir (empty string = not found).
	cfgMu    sync.RWMutex
	cfgCache map[string]*config.PipelineConfig
	wtCache  map[string]string

	dashboardTmpl *template.Template
	pipelineTmpl  *template.Template
	attemptTmpl   *template.Template
	queueTmpl     *template.Template
	configTmpl    *template.Template
}

// NewServer creates a Server with parsed templates.
func NewServer(store *pipeline.Store, database *db.DB, port int) *Server {
	return &Server{
		store:         store,
		db:            database,
		port:          port,
		cfgCache:      make(map[string]*config.PipelineConfig),
		wtCache:       make(map[string]string),
		dashboardTmpl: mustParseTmpl("base.html", "dashboard.html"),
		pipelineTmpl:  mustParseTmpl("base.html", "pipeline.html"),
		attemptTmpl:   mustParseTmpl("base.html", "attempt.html"),
		queueTmpl:     mustParseTmpl("base.html", "queue.html"),
		configTmpl:    mustParseTmpl("base.html", "config.html"),
	}
}

func mustParseTmpl(names ...string) *template.Template {
	patterns := make([]string, len(names))
	for i, n := range names {
		patterns[i] = "templates/" + n
	}
	return template.Must(template.New("").Funcs(funcMap).ParseFS(templateFS, patterns...))
}

// findRepoRoot walks up from path until it finds a directory containing
// pipeline.yaml, returning that directory. Returns "" if not found.
func findRepoRoot(path string) string {
	dir := path
	for {
		if _, err := os.Stat(filepath.Join(dir, "pipeline.yaml")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// configFor returns the PipelineConfig for the given worktree path,
// discovering it by walking up to find pipeline.yaml. Results are cached.
// Returns nil if no pipeline.yaml is found.
func (s *Server) configFor(worktree string) *config.PipelineConfig {
	if worktree == "" {
		return nil
	}

	s.cfgMu.RLock()
	if repoDir, seen := s.wtCache[worktree]; seen {
		cfg := s.cfgCache[repoDir]
		s.cfgMu.RUnlock()
		return cfg
	}
	s.cfgMu.RUnlock()

	repoDir := findRepoRoot(worktree)

	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()

	s.wtCache[worktree] = repoDir
	if repoDir == "" {
		return nil
	}

	if _, loaded := s.cfgCache[repoDir]; !loaded {
		cfg, err := config.Load(filepath.Join(repoDir, "pipeline.yaml"))
		if err != nil {
			cfg = nil
		}
		s.cfgCache[repoDir] = cfg
	}
	return s.cfgCache[repoDir]
}

// allRepoConfigs returns all distinct (repoDir, config) pairs discovered
// from the current set of pipelines.
func (s *Server) allRepoConfigs() []repoConfig {
	pipelines, _ := s.store.List("")

	seen := make(map[string]bool)
	var result []repoConfig
	for _, ps := range pipelines {
		cfg := s.configFor(ps.Worktree)
		if cfg == nil {
			continue
		}
		s.cfgMu.RLock()
		repoDir := s.wtCache[ps.Worktree]
		s.cfgMu.RUnlock()
		if repoDir == "" || seen[repoDir] {
			continue
		}
		seen[repoDir] = true
		result = append(result, repoConfig{Dir: repoDir, Cfg: cfg})
	}
	return result
}

type repoConfig struct {
	Dir string
	Cfg *config.PipelineConfig
}

// Start registers routes and starts listening.
func (s *Server) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/":
			s.handleDashboard(w, r)
		case strings.HasPrefix(r.URL.Path, "/pipeline/"):
			s.routePipeline(w, r)
		default:
			http.NotFound(w, r)
		}
	})
	mux.HandleFunc("/queue", s.handleQueue)
	mux.HandleFunc("/config", s.handleConfig)

	addr := fmt.Sprintf(":%d", s.port)
	log.Printf("TaintFactory UI: http://localhost%s", addr)
	return http.ListenAndServe(addr, mux)
}

func (s *Server) routePipeline(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/pipeline/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	switch {
	case len(parts) == 1:
		s.handlePipelineDetail(w, r, parts[0])
	case len(parts) == 3 && parts[1] == "session" && parts[2] == "stream":
		s.handleSessionStream(w, r, parts[0])
	case len(parts) == 5 && parts[1] == "stage" && parts[3] == "attempt":
		s.handleAttemptDetail(w, r, parts[0], parts[2], parts[4])
	case len(parts) == 6 && parts[1] == "stage" && parts[3] == "attempt" && parts[5] == "log":
		s.handleAttemptLog(w, r, parts[0], parts[2], parts[4])
	default:
		http.NotFound(w, r)
	}
}
