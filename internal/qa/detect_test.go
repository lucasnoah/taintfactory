package qa

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lucasnoah/taintfactory/internal/github"
)

func TestDetectBrowserTest_ForceFlag(t *testing.T) {
	result := DetectBrowserTest(DetectOpts{
		ForceFlag: true,
	})

	if !result.BrowserTestNeeded {
		t.Error("expected browser test needed with force flag")
	}
	if len(result.Reasons) == 0 || !strings.Contains(result.Reasons[0], "browser_check") {
		t.Error("expected reason mentioning browser_check")
	}
}

func TestDetectBrowserTest_Labels(t *testing.T) {
	result := DetectBrowserTest(DetectOpts{
		Issue: &github.Issue{
			Labels: []github.Label{{Name: "frontend"}},
		},
	})

	if !result.BrowserTestNeeded {
		t.Error("expected browser test needed for frontend label")
	}
}

func TestDetectBrowserTest_Labels_CaseInsensitive(t *testing.T) {
	result := DetectBrowserTest(DetectOpts{
		Issue: &github.Issue{
			Labels: []github.Label{{Name: "UI"}},
		},
	})

	if !result.BrowserTestNeeded {
		t.Error("expected browser test needed for UI label")
	}
}

func TestDetectBrowserTest_Labels_NoMatch(t *testing.T) {
	result := DetectBrowserTest(DetectOpts{
		Issue: &github.Issue{
			Labels: []github.Label{{Name: "backend"}, {Name: "database"}},
		},
	})

	if result.BrowserTestNeeded {
		t.Error("did not expect browser test for backend labels")
	}
}

func TestDetectBrowserTest_IssueBodyKeywords(t *testing.T) {
	result := DetectBrowserTest(DetectOpts{
		Issue: &github.Issue{
			Body: "Add a login form with a modal dialog for password reset",
		},
	})

	if !result.BrowserTestNeeded {
		t.Error("expected browser test needed for issue body with form/modal/dialog")
	}

	found := false
	for _, r := range result.Reasons {
		if strings.Contains(r, "browser-related terms") {
			found = true
		}
	}
	if !found {
		t.Error("expected reason mentioning browser-related terms")
	}
}

func TestDetectBrowserTest_IssueBodyKeywords_SingleMatch(t *testing.T) {
	// Single keyword should not trigger (needs >= 2)
	result := DetectBrowserTest(DetectOpts{
		Issue: &github.Issue{
			Body: "Fix the button color",
		},
	})

	if result.BrowserTestNeeded {
		t.Error("single keyword should not trigger browser test")
	}
}

func TestDetectBrowserTest_IssueBodyKeywords_NoFalsePositive(t *testing.T) {
	// Common English words removed from keyword list should not trigger
	result := DetectBrowserTest(DetectOpts{
		Issue: &github.Issue{
			Body: "Update the coding style guide to match new layout standards",
		},
	})

	if result.BrowserTestNeeded {
		t.Error("common English words (style, layout) should not trigger browser test")
	}
}

func TestDetectBrowserTest_FileExtensions(t *testing.T) {
	result := DetectBrowserTest(DetectOpts{
		FilesChanged: []string{
			"src/components/LoginForm.tsx",
			"src/utils/auth.ts",
		},
	})

	if !result.BrowserTestNeeded {
		t.Error("expected browser test needed for .tsx files")
	}
}

func TestDetectBrowserTest_FileExtensions_TsJs(t *testing.T) {
	// .ts and .js should trigger extension match
	result := DetectBrowserTest(DetectOpts{
		FilesChanged: []string{
			"src/components/Button.ts",
		},
	})

	if !result.BrowserTestNeeded {
		t.Error("expected browser test needed for .ts files")
	}

	result2 := DetectBrowserTest(DetectOpts{
		FilesChanged: []string{
			"src/components/Button.js",
		},
	})

	if !result2.BrowserTestNeeded {
		t.Error("expected browser test needed for .js files")
	}
}

func TestDetectBrowserTest_FileDirectories(t *testing.T) {
	result := DetectBrowserTest(DetectOpts{
		FilesChanged: []string{
			"src/components/Header.ts",
			"src/api/auth.go",
		},
	})

	if !result.BrowserTestNeeded {
		t.Error("expected browser test needed for components/ directory")
	}
}

func TestDetectBrowserTest_DirSegmentMatching(t *testing.T) {
	// Substring false positives should NOT match
	tests := []struct {
		path    string
		shouldMatch bool
		desc    string
	}{
		{"src/subcomponents/utils.ts", false, "subcomponents should not match components"},
		{"webapp/server.go", false, "webapp should not match app"},
		{"myapp/config.go", false, "myapp should not match app"},
		{"docs/man-pages/help.md", false, "man-pages should not match pages"},
		{"previews/img.png", false, "previews should not match views"},
		{"src/components/Button.tsx", true, "src/components should match"},
		{"pages/index.tsx", true, "pages should match"},
		{"views/layout.html", true, "views should match"},
	}

	for _, tc := range tests {
		dir := matchBrowserDir(tc.path)
		matched := dir != ""
		if matched != tc.shouldMatch {
			t.Errorf("%s: matchBrowserDir(%q) = %q, want match=%v", tc.desc, tc.path, dir, tc.shouldMatch)
		}
	}
}

func TestDetectBrowserTest_NoMatch(t *testing.T) {
	result := DetectBrowserTest(DetectOpts{
		Issue: &github.Issue{
			Body:   "Update database migration for user table",
			Labels: []github.Label{{Name: "backend"}},
		},
		FilesChanged: []string{
			"internal/db/migrations/001.sql",
			"internal/db/schema.go",
		},
	})

	if result.BrowserTestNeeded {
		t.Error("did not expect browser test for backend-only changes")
	}
}

func TestDetectBrowserTest_ComponentExtraction(t *testing.T) {
	result := DetectBrowserTest(DetectOpts{
		FilesChanged: []string{
			"src/components/LoginForm.tsx",
			"src/components/SessionModal.jsx",
		},
	})

	if len(result.AffectedComponents) != 2 {
		t.Fatalf("expected 2 components, got %d: %v", len(result.AffectedComponents), result.AffectedComponents)
	}

	hasLogin := false
	hasSession := false
	for _, c := range result.AffectedComponents {
		if c == "LoginForm" {
			hasLogin = true
		}
		if c == "SessionModal" {
			hasSession = true
		}
	}
	if !hasLogin || !hasSession {
		t.Errorf("expected LoginForm and SessionModal, got %v", result.AffectedComponents)
	}
}

func TestDetectBrowserTest_RouteExtraction(t *testing.T) {
	result := DetectBrowserTest(DetectOpts{
		Issue: &github.Issue{
			Body: "The login page at /auth/login should redirect to /dashboard after success",
		},
	})

	hasLogin := false
	hasDashboard := false
	for _, r := range result.AffectedRoutes {
		if r == "/auth/login" {
			hasLogin = true
		}
		if r == "/dashboard" {
			hasDashboard = true
		}
	}
	if !hasLogin || !hasDashboard {
		t.Errorf("expected /auth/login and /dashboard in routes, got %v", result.AffectedRoutes)
	}
}

func TestDetectBrowserTest_RouteExtraction_CodeBlocksStripped(t *testing.T) {
	result := DetectBrowserTest(DetectOpts{
		Issue: &github.Issue{
			Body: "See the /dashboard route.\n```\npath: /api/internal\nlog: /var/log/app\n```",
		},
	})

	for _, r := range result.AffectedRoutes {
		if r == "/api/internal" {
			t.Error("should not extract routes from code blocks")
		}
	}
	// /dashboard should still be extracted
	hasDashboard := false
	for _, r := range result.AffectedRoutes {
		if r == "/dashboard" {
			hasDashboard = true
		}
	}
	if !hasDashboard {
		t.Error("expected /dashboard from text outside code block")
	}
}

func TestDetectBrowserTest_RouteFromFilePaths(t *testing.T) {
	result := DetectBrowserTest(DetectOpts{
		FilesChanged: []string{
			"src/pages/auth/login.tsx",
			"src/pages/dashboard/index.tsx",
		},
	})

	hasLogin := false
	hasDashboard := false
	for _, r := range result.AffectedRoutes {
		if r == "/auth/login" {
			hasLogin = true
		}
		if r == "/dashboard" {
			hasDashboard = true
		}
	}
	if !hasLogin || !hasDashboard {
		t.Errorf("expected /auth/login and /dashboard from pages/ paths, got %v", result.AffectedRoutes)
	}
}

func TestDetectBrowserTest_NoSpuriousAppRoutes(t *testing.T) {
	// app/ should NOT be used for route extraction (finding #3)
	result := DetectBrowserTest(DetectOpts{
		FilesChanged: []string{
			"app/internal/services/email.go",
		},
	})

	for _, r := range result.AffectedRoutes {
		if r == "/internal/services/email" {
			t.Error("app/ paths should not extract bogus routes")
		}
	}
}

func TestDetectBrowserTest_MultipleSources(t *testing.T) {
	result := DetectBrowserTest(DetectOpts{
		Issue: &github.Issue{
			Body:   "Add login form with modal dialog",
			Labels: []github.Label{{Name: "frontend"}},
		},
		FilesChanged: []string{
			"src/components/LoginForm.tsx",
		},
		ForceFlag: true,
	})

	if !result.BrowserTestNeeded {
		t.Error("expected browser test needed")
	}
	// Should have multiple reasons
	if len(result.Reasons) < 3 {
		t.Errorf("expected at least 3 reasons, got %d: %v", len(result.Reasons), result.Reasons)
	}
}

func TestDetectBrowserTest_NilIssue(t *testing.T) {
	result := DetectBrowserTest(DetectOpts{
		Issue:        nil,
		FilesChanged: []string{"README.md"},
	})

	if result.BrowserTestNeeded {
		t.Error("did not expect browser test with nil issue and README change")
	}
}

func TestDetectBrowserTest_EmptyOpts(t *testing.T) {
	result := DetectBrowserTest(DetectOpts{})

	if result.BrowserTestNeeded {
		t.Error("did not expect browser test with empty opts")
	}
	if result.Reasons == nil {
		t.Error("expected non-nil reasons slice")
	}
}

func TestDetectBrowserTest_DedupeReturnsNonNil(t *testing.T) {
	result := DetectBrowserTest(DetectOpts{})

	// Verify JSON serialization produces [] not null
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(data)
	if strings.Contains(s, `"affected_routes":null`) {
		t.Error("affected_routes should be [] not null in JSON")
	}
	if strings.Contains(s, `"affected_components":null`) {
		t.Error("affected_components should be [] not null in JSON")
	}
}

func TestExtractRouteFromPath(t *testing.T) {
	tests := []struct {
		path     string
		expected string
	}{
		{"src/pages/auth/login.tsx", "/auth/login"},
		{"src/pages/dashboard/index.tsx", "/dashboard"},
		{"src/pages/index.tsx", "/"},
		{"src/routes/api/users.ts", "/api/users"},
		{"internal/db/schema.go", ""},
		// app/ no longer extracts routes
		{"app/settings/profile.tsx", ""},
		// Substring false positives
		{"docs/man-pages/help.md", ""},
	}

	for _, tc := range tests {
		got := extractRouteFromPath(tc.path)
		if got != tc.expected {
			t.Errorf("extractRouteFromPath(%q) = %q, want %q", tc.path, got, tc.expected)
		}
	}
}

func TestDedupe(t *testing.T) {
	items := []string{"a", "b", "a", "c", "b"}
	result := dedupe(items)
	if len(result) != 3 {
		t.Errorf("expected 3 items, got %d: %v", len(result), result)
	}
}

func TestDedupe_NilInput(t *testing.T) {
	result := dedupe(nil)
	if result == nil {
		t.Error("dedupe(nil) should return non-nil slice")
	}
	if len(result) != 0 {
		t.Errorf("expected 0 items, got %d", len(result))
	}
}

func TestIsCommonNonRoute(t *testing.T) {
	if !isCommonNonRoute("/bin/sh") {
		t.Error("expected /bin/sh to be a non-route")
	}
	if !isCommonNonRoute("/lib/something") {
		t.Error("expected /lib/something to be a non-route")
	}
	if isCommonNonRoute("/auth/login") {
		t.Error("did not expect /auth/login to be a non-route")
	}
}
