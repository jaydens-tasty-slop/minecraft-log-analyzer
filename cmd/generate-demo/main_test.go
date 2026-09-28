package main

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"

	"minecraft-log-analyzer/internal/store"
)

func TestGenerateIsDeterministicAndCoversMultipleYears(t *testing.T) {
	dir := t.TempDir()
	firstPath := filepath.Join(dir, "first.db")
	secondPath := filepath.Join(dir, "second.db")
	if err := generate(firstPath); err != nil {
		t.Fatal(err)
	}
	if err := generate(secondPath); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(firstPath)
	if err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(secondPath)
	if err != nil {
		t.Fatal(err)
	}
	if sha256.Sum256(first) != sha256.Sum256(second) {
		t.Fatal("identical demo generations produced different databases")
	}

	db, err := store.OpenReadOnly(firstPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var sources, events, identities, firstYear, lastYear int
	if err := db.QueryRow(`SELECT COUNT(*), MIN(CAST(substr(log_date, 1, 4) AS INTEGER)), MAX(CAST(substr(log_date, 1, 4) AS INTEGER)) FROM source_files`).Scan(&sources, &firstYear, &lastYear); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM events`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM identity_observations`).Scan(&identities); err != nil {
		t.Fatal(err)
	}
	if sources < 200 || events < 4000 || identities < 1000 || firstYear != 2018 || lastYear != 2026 {
		t.Fatalf("unexpected demo coverage: sources=%d events=%d identities=%d years=%d-%d", sources, events, identities, firstYear, lastYear)
	}
}
