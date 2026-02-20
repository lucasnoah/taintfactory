package qa

import (
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

func TestExtractRouteFromPath(t *testing.T) {
	tests := []struct {
		path     string
		expected string
	}{
		{"src/pages/auth/login.tsx", "/auth/login"},
		{"src/pages/dashboard/index.tsx", "/dashboard"},
		{"src/pages/index.tsx", "/"},
		{"src/routes/api/users.ts", "/api/users"},
		{"src/app/settings/profile.tsx", "/settings/profile"},
		{"internal/db/schema.go", ""},
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

func TestIsCommonNonRoute(t *testing.T) {
	if !isCommonNonRoute("/bin/sh") {
		t.Error("expected /bin/sh to be a non-route")
	}
	if isCommonNonRoute("/auth/login") {
		t.Error("did not expect /auth/login to be a non-route")
	}
}
