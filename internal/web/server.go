package web

import (
	"embed"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/lucasnoah/taintfactory/internal/config"
	"github.com/lucasnoah/taintfactory/internal/db"
	"github.com/lucasnoah/taintfactory/internal/pipeline"
	"github.com/lucasnoah/taintfactory/internal/triage"
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

	// Triage support
	triageDir      string
	triageTmpl     *template.Template
	triageListTmpl *template.Template
	triageCfgMu    sync.RWMutex
	triageCfgCache map[string]*triage.TriageConfig // keyed by repoRoot
}

// NewServer creates a Server with parsed templates.
func NewServer(store *pipeline.Store, database *db.DB, port int, triageDir string) *Server {
	return &Server{
		store:          store,
		db:             database,
		port:           port,
		triageDir:      triageDir,
		cfgCache:       make(map[string]*config.PipelineConfig),
		wtCache:        make(map[string]string),
		triageCfgCache: make(map[string]*triage.TriageConfig),
		dashboardTmpl:  mustParseTmpl("base.html", "dashboard.html"),
		pipelineTmpl:   mustParseTmpl("base.html", "pipeline.html"),
		attemptTmpl:    mustParseTmpl("base.html", "attempt.html"),
		queueTmpl:      mustParseTmpl("base.html", "queue.html"),
		configTmpl:     mustParseTmpl("base.html", "config.html"),
		triageTmpl:     mustParseTmpl("base.html", "triage.html"),
		triageListTmpl: mustParseTmpl("base.html", "triage-list.html"),
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

// configForPS returns the PipelineConfig for a pipeline state.
// When ps.ConfigPath is set (multi-project pipelines), it loads directly from
// that path, using ps.RepoDir (or filepath.Dir(ConfigPath)) as the cache key.
// Otherwise it falls back to the filesystem-walk behaviour of configFor.
func (s *Server) configForPS(ps *pipeline.PipelineState) *config.PipelineConfig {
	if ps.ConfigPath != "" {
		repoDir := ps.RepoDir
		if repoDir == "" {
			repoDir = filepath.Dir(ps.ConfigPath)
		}

		s.cfgMu.RLock()
		if cfg, ok := s.cfgCache[repoDir]; ok {
			s.cfgMu.RUnlock()
			return cfg
		}
		s.cfgMu.RUnlock()

		s.cfgMu.Lock()
		defer s.cfgMu.Unlock()
		if _, loaded := s.cfgCache[repoDir]; !loaded {
			cfg, err := config.Load(ps.ConfigPath)
			if err != nil {
				cfg = nil
			}
			s.cfgCache[repoDir] = cfg
		}
		return s.cfgCache[repoDir]
	}
	return s.configFor(ps.Worktree)
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
		cfg := s.configForPS(&ps)
		if cfg == nil {
			continue
		}
		var repoDir string
		if ps.ConfigPath != "" {
			repoDir = ps.RepoDir
			if repoDir == "" {
				repoDir = filepath.Dir(ps.ConfigPath)
			}
		} else {
			s.cfgMu.RLock()
			repoDir = s.wtCache[ps.Worktree]
			s.cfgMu.RUnlock()
		}
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

// currentProject reads the ?project= query parameter from a request.
func currentProject(r *http.Request) string {
	return r.URL.Query().Get("project")
}

// repoToNamespace converts a repo URL like "github.com/org/repo" or
// "https://github.com/org/repo" to a namespace string "org/repo".
func repoToNamespace(repo string) string {
	if repo == "" {
		return ""
	}
	repo = strings.TrimPrefix(repo, "https://")
	repo = strings.TrimPrefix(repo, "http://")
	parts := strings.SplitN(repo, "/", 2)
	if len(parts) >= 2 {
		return parts[1]
	}
	return repo
}

// namespaceFromConfigPath derives the namespace for a config file path by
// reading the cached pipeline config for that directory. Returns "" if not cached.
// Call sidebarData first to warm the cache.
func (s *Server) namespaceFromConfigPath(configPath string) string {
	if configPath == "" {
		return ""
	}
	repoDir := filepath.Dir(configPath)
	s.cfgMu.RLock()
	cfg := s.cfgCache[repoDir]
	s.cfgMu.RUnlock()
	if cfg == nil {
		return ""
	}
	return repoToNamespace(cfg.Pipeline.Repo)
}

// sidebarData returns sidebar state for all known namespaced projects.
// currentProject should be the ?project= query param value (empty = All view).
func (s *Server) sidebarData(currentProj string) SidebarData {
	if s.store == nil {
		return SidebarData{CurrentProject: currentProj}
	}
	pipelines, _ := s.store.List("")

	// Warm config cache so namespaceFromConfigPath works for queue items.
	for i := range pipelines {
		s.configForPS(&pipelines[i])
	}

	type entry struct{ active, total int }
	counts := make(map[string]*entry)
	for _, ps := range pipelines {
		if ps.Namespace == "" {
			continue
		}
		e := counts[ps.Namespace]
		if e == nil {
			e = &entry{}
			counts[ps.Namespace] = e
		}
		e.total++
		if ps.Status == "in_progress" {
			e.active++
		}
	}

	var projects []ProjectSidebarItem
	for ns, e := range counts {
		projects = append(projects, ProjectSidebarItem{
			Namespace:   ns,
			ActiveCount: e.active,
			IsSelected:  ns == currentProj,
		})
	}
	sort.Slice(projects, func(i, j int) bool {
		return projects[i].Namespace < projects[j].Namespace
	})

	return SidebarData{Projects: projects, CurrentProject: currentProj}
}

// Start registers routes and starts listening.
func (s *Server) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/":
			s.handleDashboard(w, r)
		case r.URL.Path == "/triage":
			s.handleTriageList(w, r)
		case strings.HasPrefix(r.URL.Path, "/pipeline/"):
			s.routePipeline(w, r)
		case strings.HasPrefix(r.URL.Path, "/triage/"):
			s.routeTriage(w, r)
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

func (s *Server) routeTriage(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/triage/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	// Validate slug to prevent path traversal: reject if it contains / \ or starts with .
	if len(parts) >= 1 {
		slug := parts[0]
		if strings.ContainsAny(slug, "/\\") || slug == ".." || strings.HasPrefix(slug, ".") {
			http.NotFound(w, r)
			return
		}
	}
	switch {
	case len(parts) == 2:
		s.handleTriageDetail(w, r, parts[0], parts[1])
	case len(parts) == 4 && parts[2] == "session" && parts[3] == "stream":
		s.handleTriageStream(w, r, parts[0], parts[1])
	default:
		http.NotFound(w, r)
	}
}

// triageConfigFor returns the TriageConfig for the given repo root directory.
// Results are cached. Returns nil if triage.yaml is not found.
func (s *Server) triageConfigFor(repoRoot string) *triage.TriageConfig {
	if repoRoot == "" {
		return nil
	}
	s.triageCfgMu.RLock()
	if cfg, ok := s.triageCfgCache[repoRoot]; ok {
		s.triageCfgMu.RUnlock()
		return cfg
	}
	s.triageCfgMu.RUnlock()

	s.triageCfgMu.Lock()
	defer s.triageCfgMu.Unlock()
	if _, loaded := s.triageCfgCache[repoRoot]; !loaded {
		cfg, err := triage.LoadDefault(repoRoot)
		if err != nil {
			s.triageCfgCache[repoRoot] = nil
		} else {
			s.triageCfgCache[repoRoot] = cfg
		}
	}
	return s.triageCfgCache[repoRoot]
}

// allTriageStates scans ~/.factory/triage/ for all repo slug subdirectories
// and returns every triage state found.
func (s *Server) allTriageStates() []triage.TriageState {
	if s.triageDir == "" {
		return nil
	}
	entries, err := os.ReadDir(s.triageDir)
	if err != nil {
		return nil
	}
	var states []triage.TriageState
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		slug := e.Name()
		store := triage.NewStore(filepath.Join(s.triageDir, slug))
		all, err := store.List("")
		if err != nil {
			continue
		}
		states = append(states, all...)
	}
	return states
}

// triageStoreFor returns a Store for the given repo slug.
func (s *Server) triageStoreFor(slug string) *triage.Store {
	return triage.NewStore(filepath.Join(s.triageDir, slug))
}
