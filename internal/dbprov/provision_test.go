package dbprov

import (
	"strings"
	"testing"

	"github.com/lucasnoah/taintfactory/internal/config"
)

func TestBuildProvisionSQL(t *testing.T) {
	cfg := &config.DatabaseConfig{
		Name:     "mydb",
		User:     "myuser",
		Password: "mypass",
	}
	stmts := buildProvisionSQL(cfg)
	if len(stmts) != 3 {
		t.Fatalf("expected 3 statements, got %d", len(stmts))
	}

	if !strings.Contains(stmts[0], `CREATE ROLE "myuser"`) {
		t.Errorf("stmt[0] = %q, missing CREATE ROLE", stmts[0])
	}
	if !strings.Contains(stmts[0], `PASSWORD 'mypass'`) {
		t.Errorf("stmt[0] = %q, missing PASSWORD", stmts[0])
	}
	if !strings.Contains(stmts[1], `CREATE DATABASE "mydb" OWNER "myuser"`) {
		t.Errorf("stmt[1] = %q", stmts[1])
	}
	if !strings.Contains(stmts[2], `GRANT ALL PRIVILEGES ON DATABASE "mydb" TO "myuser"`) {
		t.Errorf("stmt[2] = %q", stmts[2])
	}
}

func TestBuildProvisionSQL_SpecialChars(t *testing.T) {
	cfg := &config.DatabaseConfig{
		Name:     "mydb",
		User:     "myuser",
		Password: "pass'word",
	}
	stmts := buildProvisionSQL(cfg)
	// Single quotes in password must be escaped as ''
	if !strings.Contains(stmts[0], `PASSWORD 'pass''word'`) {
		t.Errorf("stmt[0] = %q, single quote not escaped", stmts[0])
	}
}

func TestAdminConnStr(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{
			name:  "standard url",
			input: "postgres://user:pass@host:5432/mydb?sslmode=disable",
			want:  "postgres://user:pass@host:5432/postgres?sslmode=disable",
		},
		{
			name:  "no query params",
			input: "postgres://user:pass@host:5432/mydb",
			want:  "postgres://user:pass@host:5432/postgres",
		},
		{
			name:    "invalid url",
			input:   "://bad",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := adminConnStr(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
