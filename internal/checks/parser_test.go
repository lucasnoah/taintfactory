package checks

import (
	"encoding/json"
	"testing"
)

func TestESLintParser_Success(t *testing.T) {
	input := `[{"filePath":"src/index.ts","messages":[]}]`
	p := &ESLintParser{}
	r := p.Parse(input, "", 0)
	if !r.Passed {
		t.Error("expected passed=true")
	}
	if r.Summary != "0 errors, 0 warnings, 0 fixable" {
		t.Errorf("unexpected summary: %q", r.Summary)
	}
}

func TestESLintParser_Errors(t *testing.T) {
	input := `[{
		"filePath": "src/auth.ts",
		"messages": [
			{"ruleId": "no-unused-vars", "severity": 2, "message": "x is unused", "line": 42, "column": 5},
			{"ruleId": "semi", "severity": 1, "message": "Missing semicolon", "line": 10, "column": 20, "fix": {"range": [100, 100], "text": ";"}}
		]
	}]`
	p := &ESLintParser{}
	r := p.Parse(input, "", 1)
	if r.Passed {
		t.Error("expected passed=false")
	}
	if r.Summary != "1 errors, 1 warnings, 1 fixable" {
		t.Errorf("unexpected summary: %q", r.Summary)
	}

	result := r.Findings.(eslintResult)
	if result.Errors != 1 {
		t.Errorf("expected 1 error, got %d", result.Errors)
	}
	if result.Warnings != 1 {
		t.Errorf("expected 1 warning, got %d", result.Warnings)
	}
	if len(result.Findings) != 2 {
		t.Fatalf("expected 2 findings, got %d", len(result.Findings))
	}
	if result.Findings[0].Rule != "no-unused-vars" {
		t.Errorf("expected rule=no-unused-vars, got %q", result.Findings[0].Rule)
	}
	if result.Findings[0].Severity != "error" {
		t.Errorf("expected severity=error, got %q", result.Findings[0].Severity)
	}
}

func TestESLintParser_InvalidJSON(t *testing.T) {
	p := &ESLintParser{}
	r := p.Parse("not json", "", 1)
	if r.Passed {
		t.Error("expected passed=false for exit code 1")
	}
}

func TestPrettierParser_AllFormatted(t *testing.T) {
	p := &PrettierParser{}
	r := p.Parse("Checking formatting...\nAll matched files use Prettier code style!", "", 0)
	if !r.Passed {
		t.Error("expected passed=true")
	}
	if r.Summary != "all files formatted" {
		t.Errorf("unexpected summary: %q", r.Summary)
	}
}

func TestPrettierParser_FilesNeedFormatting(t *testing.T) {
	input := `Checking formatting...
[warn] src/auth.ts
[warn] src/index.ts
[warn] Code style issues found in the above file(s). Forgot to run Prettier?`
	p := &PrettierParser{}
	r := p.Parse(input, "", 1)
	if r.Passed {
		t.Error("expected passed=false")
	}
	result := r.Findings.(prettierResult)
	if result.Count != 2 {
		t.Errorf("expected 2 files, got %d", result.Count)
	}
	if result.FilesNeedingFormat[0] != "src/auth.ts" {
		t.Errorf("expected src/auth.ts, got %q", result.FilesNeedingFormat[0])
	}
}

func TestTypeScriptParser_NoErrors(t *testing.T) {
	p := &TypeScriptParser{}
	r := p.Parse("", "", 0)
	if !r.Passed {
		t.Error("expected passed=true")
	}
	if r.Summary != "no errors" {
		t.Errorf("unexpected summary: %q", r.Summary)
	}
}

func TestTypeScriptParser_Errors(t *testing.T) {
	input := `src/auth.ts(42,5): error TS2345: Argument of type 'string' is not assignable to parameter of type 'number'.
src/index.ts(10,3): error TS2304: Cannot find name 'foo'.`
	p := &TypeScriptParser{}
	r := p.Parse(input, "", 1)
	if r.Passed {
		t.Error("expected passed=false")
	}

	result := r.Findings.(tsResult)
	if result.Errors != 2 {
		t.Errorf("expected 2 errors, got %d", result.Errors)
	}
	if len(result.Findings) != 2 {
		t.Fatalf("expected 2 findings, got %d", len(result.Findings))
	}
	f := result.Findings[0]
	if f.File != "src/auth.ts" {
		t.Errorf("expected file=src/auth.ts, got %q", f.File)
	}
	if f.Line != 42 {
		t.Errorf("expected line=42, got %d", f.Line)
	}
	if f.Code != "TS2345" {
		t.Errorf("expected code=TS2345, got %q", f.Code)
	}
}

func TestVitestParser_AllPass(t *testing.T) {
	input := `{
		"numTotalTests": 10,
		"numPassedTests": 10,
		"numFailedTests": 0,
		"numPendingTests": 0,
		"testResults": []
	}`
	p := &VitestParser{}
	r := p.Parse(input, "", 0)
	if !r.Passed {
		t.Error("expected passed=true")
	}
	if r.Summary != "10 passed, 0 failed, 0 skipped out of 10" {
		t.Errorf("unexpected summary: %q", r.Summary)
	}
}

func TestVitestParser_Failures(t *testing.T) {
	input := `{
		"numTotalTests": 5,
		"numPassedTests": 3,
		"numFailedTests": 2,
		"numPendingTests": 0,
		"testResults": [{
			"name": "auth.test.ts",
			"status": "failed",
			"assertionResults": [
				{"fullName": "should reject expired tokens", "status": "failed", "failureMessages": ["Expected 401, received 200"]},
				{"fullName": "should accept valid tokens", "status": "passed", "failureMessages": []}
			]
		}]
	}`
	p := &VitestParser{}
	r := p.Parse(input, "", 1)
	if r.Passed {
		t.Error("expected passed=false")
	}

	result := r.Findings.(vitestResult)
	if result.Failed != 2 {
		t.Errorf("expected 2 failed, got %d", result.Failed)
	}
	if len(result.Failures) != 1 {
		t.Fatalf("expected 1 failure detail, got %d", len(result.Failures))
	}
	if result.Failures[0].Test != "should reject expired tokens" {
		t.Errorf("expected test name, got %q", result.Failures[0].Test)
	}
	if result.Failures[0].Error != "Expected 401, received 200" {
		t.Errorf("expected error message, got %q", result.Failures[0].Error)
	}
}

func TestVitestParser_InvalidJSON(t *testing.T) {
	p := &VitestParser{}
	r := p.Parse("not json", "", 1)
	if r.Passed {
		t.Error("expected passed=false")
	}
}

func TestNPMAuditParser_Clean(t *testing.T) {
	input := `{"metadata":{"vulnerabilities":{"critical":0,"high":0,"moderate":0,"low":0,"info":0,"total":0}},"vulnerabilities":{}}`
	p := &NPMAuditParser{}
	r := p.Parse(input, "", 0)
	if !r.Passed {
		t.Error("expected passed=true")
	}
	if r.Summary != "no vulnerabilities found" {
		t.Errorf("unexpected summary: %q", r.Summary)
	}
}

func TestNPMAuditParser_Vulnerabilities(t *testing.T) {
	input := `{
		"metadata": {
			"vulnerabilities": {"critical": 1, "high": 2, "moderate": 1, "low": 1, "info": 0, "total": 5}
		},
		"vulnerabilities": {
			"lodash": {"name": "lodash", "severity": "critical", "title": "Prototype Pollution"}
		}
	}`
	p := &NPMAuditParser{}
	r := p.Parse(input, "", 1)
	if r.Passed {
		t.Error("expected passed=false")
	}

	result := r.Findings.(auditResult)
	if result.Total != 5 {
		t.Errorf("expected total=5, got %d", result.Total)
	}
	if result.Critical != 1 {
		t.Errorf("expected critical=1, got %d", result.Critical)
	}
	if len(result.Advisories) != 1 {
		t.Fatalf("expected 1 advisory, got %d", len(result.Advisories))
	}
	if result.Advisories[0].Module != "lodash" {
		t.Errorf("expected module=lodash, got %q", result.Advisories[0].Module)
	}
}

func TestNPMAuditParser_InvalidJSON(t *testing.T) {
	p := &NPMAuditParser{}
	r := p.Parse("not json", "", 1)
	if r.Passed {
		t.Error("expected passed=false")
	}
}

func TestGenericParser_Pass(t *testing.T) {
	p := &GenericParser{}
	r := p.Parse("output text", "stderr text", 0)
	if !r.Passed {
		t.Error("expected passed=true")
	}
	if r.Summary != "passed (exit code 0)" {
		t.Errorf("unexpected summary: %q", r.Summary)
	}

	result := r.Findings.(genericResult)
	if result.StdoutLength != 11 {
		t.Errorf("expected stdout_length=11, got %d", result.StdoutLength)
	}
}

func TestGenericParser_Fail(t *testing.T) {
	p := &GenericParser{}
	r := p.Parse("err", "", 1)
	if r.Passed {
		t.Error("expected passed=false")
	}
}

func TestParseResult_JSONSerializable(t *testing.T) {
	r := ParseResult{
		Passed:  true,
		Summary: "all good",
		Findings: genericResult{
			ExitCode:     0,
			StdoutLength: 10,
			StderrLength: 0,
		},
	}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}
	if len(data) == 0 {
		t.Error("expected non-empty JSON")
	}
}
