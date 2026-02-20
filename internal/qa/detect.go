package qa

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/lucasnoah/taintfactory/internal/github"
)

// DetectResult holds the browser detection analysis.
type DetectResult struct {
	BrowserTestNeeded  bool     `json:"browser_test_needed"`
	Reasons            []string `json:"reasons"`
	AffectedRoutes     []string `json:"affected_routes"`
	AffectedComponents []string `json:"affected_components"`
}

// DetectOpts configures browser test detection.
type DetectOpts struct {
	Issue        *github.Issue
	FilesChanged []string // list of changed file paths
	ForceFlag    bool     // stage config browser_check flag
}

// browser-related issue labels
var browserLabels = map[string]bool{
	"frontend": true,
	"ui":       true,
	"browser":  true,
	"web":      true,
	"css":      true,
	"ux":       true,
}

// browser-related file extensions (includes .ts and .js for frontend code)
var browserExtensions = map[string]bool{
	".tsx":    true,
	".jsx":    true,
	".ts":     true,
	".js":     true,
	".html":   true,
	".css":    true,
	".scss":   true,
	".less":   true,
	".vue":    true,
	".svelte": true,
}

// browserDirs matched as path segments (not substrings) via hasBrowserDirSegment
var browserDirs = []string{
	"components",
	"pages",
	"routes",
	"views",
	"layouts",
	"styles",
	"public",
	"static",
	"templates",
	"src/ui",
}

// keywords in issue body suggesting browser testing —
// higher-signal terms that are less likely to appear in non-UI contexts
var browserKeywords = regexp.MustCompile(
	`(?i)\b(button|form|modal|dialog|dropdown|` +
		`checkbox|radio|tooltip|popover|sidebar|` +
		`accordion|carousel|slider|` +
		`login|signup|dashboard|landing page|responsive|` +
		`hover|scroll|drag|animate|` +
		`browser|viewport|mobile|css)\b`,
)

// route extraction pattern: /path/like/this
var routePattern = regexp.MustCompile(`(?:^|[^a-zA-Z0-9])(\/[a-z][a-z0-9\-\/]*[a-z0-9])`)

// codeBlockPattern matches Markdown fenced code blocks
var codeBlockPattern = regexp.MustCompile("(?s)```.*?```")

// component extraction from file names (PascalCase filenames in component dirs)
var componentNamePattern = regexp.MustCompile(`([A-Z][a-zA-Z0-9]+)`)

// DetectBrowserTest analyzes whether an issue needs browser testing.
func DetectBrowserTest(opts DetectOpts) *DetectResult {
	result := &DetectResult{
		Reasons:            []string{},
		AffectedRoutes:     []string{},
		AffectedComponents: []string{},
	}

	// Check explicit force flag
	if opts.ForceFlag {
		result.BrowserTestNeeded = true
		result.Reasons = append(result.Reasons, "Stage config has browser_check: true")
	}

	// Check issue labels
	if opts.Issue != nil {
		for _, label := range opts.Issue.Labels {
			if browserLabels[strings.ToLower(label.Name)] {
				result.BrowserTestNeeded = true
				result.Reasons = append(result.Reasons, "Issue has browser-related label: "+label.Name)
				break
			}
		}

		// Check issue body keywords
		if opts.Issue.Body != "" {
			matches := browserKeywords.FindAllString(opts.Issue.Body, -1)
			if len(matches) > 0 {
				seen := make(map[string]bool)
				var unique []string
				for _, m := range matches {
					lower := strings.ToLower(m)
					if !seen[lower] {
						seen[lower] = true
						unique = append(unique, lower)
					}
				}
				if len(unique) >= 2 {
					result.BrowserTestNeeded = true
					n := len(unique)
					if n > 3 {
						n = 3
					}
					result.Reasons = append(result.Reasons, "Issue body mentions browser-related terms: "+strings.Join(unique[:n], ", "))
				}
			}
		}

		// Extract routes from issue body (strip code blocks first)
		bodyForRoutes := codeBlockPattern.ReplaceAllString(opts.Issue.Body, "")
		routeMatches := routePattern.FindAllStringSubmatch(bodyForRoutes, -1)
		seen := make(map[string]bool)
		for _, m := range routeMatches {
			route := m[1]
			if !seen[route] && !isCommonNonRoute(route) {
				seen[route] = true
				result.AffectedRoutes = append(result.AffectedRoutes, route)
			}
		}
	}

	// Check files changed
	if len(opts.FilesChanged) > 0 {
		browserFiles := analyzeBrowserFiles(opts.FilesChanged)
		if browserFiles.hasBrowserExtension {
			result.BrowserTestNeeded = true
			result.Reasons = append(result.Reasons, "Files changed include browser-related extensions: "+strings.Join(browserFiles.extensions, ", "))
		}
		if browserFiles.hasBrowserDir {
			result.BrowserTestNeeded = true
			result.Reasons = append(result.Reasons, "Files changed include browser-related directories: "+strings.Join(browserFiles.dirs, ", "))
		}
		result.AffectedComponents = append(result.AffectedComponents, browserFiles.components...)

		// Extract routes from file paths (pages/ or routes/ directories)
		for _, f := range opts.FilesChanged {
			if route := extractRouteFromPath(f); route != "" {
				result.AffectedRoutes = append(result.AffectedRoutes, route)
			}
		}
	}

	// Deduplicate routes and components
	result.AffectedRoutes = dedupe(result.AffectedRoutes)
	result.AffectedComponents = dedupe(result.AffectedComponents)

	return result
}

type browserFileAnalysis struct {
	hasBrowserExtension bool
	hasBrowserDir       bool
	extensions          []string
	dirs                []string
	components          []string
}

func analyzeBrowserFiles(files []string) browserFileAnalysis {
	result := browserFileAnalysis{}
	seenExts := make(map[string]bool)
	seenDirs := make(map[string]bool)

	for _, f := range files {
		ext := filepath.Ext(f)
		if browserExtensions[ext] && !seenExts[ext] {
			result.hasBrowserExtension = true
			seenExts[ext] = true
			result.extensions = append(result.extensions, ext)
		}

		if dir := matchBrowserDir(f); dir != "" && !seenDirs[dir] {
			result.hasBrowserDir = true
			seenDirs[dir] = true
			result.dirs = append(result.dirs, dir)
		}

		// Extract component names from PascalCase filenames in browser files
		base := filepath.Base(f)
		nameOnly := strings.TrimSuffix(base, filepath.Ext(base))
		if componentNamePattern.MatchString(nameOnly) && isBrowserFile(f) {
			result.components = append(result.components, nameOnly)
		}
	}

	return result
}

// matchBrowserDir checks if a file path contains a browser directory as a
// proper path segment (not a substring). Returns the matched dir or "".
func matchBrowserDir(path string) string {
	parts := strings.Split(filepath.ToSlash(path), "/")
	for _, dir := range browserDirs {
		if strings.Contains(dir, "/") {
			// Multi-segment like "src/ui" — check consecutive segments
			if strings.Contains(filepath.ToSlash(path), dir+"/") || strings.HasSuffix(filepath.ToSlash(path), dir) {
				return dir
			}
		} else {
			for _, p := range parts {
				if p == dir {
					return dir
				}
			}
		}
	}
	return ""
}

func isBrowserFile(path string) bool {
	ext := filepath.Ext(path)
	return browserExtensions[ext]
}

// extractRouteFromPath extracts a route from a pages/ or routes/ file path.
// Only matches "pages" and "routes" as exact path segments.
func extractRouteFromPath(path string) string {
	parts := strings.Split(filepath.ToSlash(path), "/")
	for i, p := range parts {
		if p == "pages" || p == "routes" {
			rest := strings.Join(parts[i+1:], "/")
			// Remove extension
			if ext := filepath.Ext(rest); ext != "" {
				rest = strings.TrimSuffix(rest, ext)
			}
			// Remove index files
			rest = strings.TrimSuffix(rest, "/index")
			rest = strings.TrimSuffix(rest, "index")
			if rest == "" {
				return "/"
			}
			return "/" + rest
		}
	}
	return ""
}

// isCommonNonRoute filters out paths that look like routes but aren't.
var nonRoutePrefixes = []string{
	"/bin", "/etc", "/tmp", "/var", "/usr", "/dev", "/opt", "/home",
	"/lib", "/sbin", "/proc", "/sys", "/run", "/snap",
}

func isCommonNonRoute(path string) bool {
	for _, nr := range nonRoutePrefixes {
		if strings.HasPrefix(path, nr) {
			return true
		}
	}
	return false
}

// dedupe returns a new slice with duplicates removed, preserving order.
// Always returns a non-nil slice for consistent JSON serialization.
func dedupe(items []string) []string {
	seen := make(map[string]bool)
	result := []string{}
	for _, item := range items {
		if !seen[item] {
			seen[item] = true
			result = append(result, item)
		}
	}
	return result
}
