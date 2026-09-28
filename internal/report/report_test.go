package report

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"minecraft-log-analyzer/internal/analyzer"
)

func TestWriteProducesSimpleLinkedPages(t *testing.T) {
	dir := t.TempDir()
	playersDir := filepath.Join(dir, "players")
	if err := os.MkdirAll(playersDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(playersDir, "old-name-deadbeef.html")
	unrelated := filepath.Join(playersDir, "keep-me.html")
	if err := os.WriteFile(stale, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unrelated, []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	player := &analyzer.Player{Name: "Alice", Slug: "alice-1234", SessionCount: 1, LastLogin: time.Now(), LifetimeCount: 1, MinLifetime: time.Minute, MaxLifetime: 3 * time.Minute, AverageLifetime: 2 * time.Minute}
	model := analyzer.Model{
		ServerName:  "Test & Realm",
		Sources:     2472,
		Players:     []*analyzer.Player{player},
		GeneratedAt: time.Now(),
		Chats:       []analyzer.ChatMessage{{At: time.Now(), Player: "Alice", Text: `<script>alert("no")</script>`}},
	}
	if err := Write(dir, model); err != nil {
		t.Fatal(err)
	}
	index, err := os.ReadFile(filepath.Join(dir, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(index), `players/alice-1234.html`) {
		t.Fatal("index does not link to player page")
	}
	if !strings.Contains(string(index), `<title>Test &amp; Realm stats</title>`) {
		t.Fatal("index does not contain the escaped configured server name")
	}
	if strings.Contains(string(index), "chat.html") {
		t.Fatal("index links to the intentionally unlisted chat page")
	}
	if strings.Contains(string(index), "archived log files") {
		t.Fatal("index still advertises the archived log-file count")
	}
	if !strings.Contains(string(index), `data-sort-controls="players-table"`) || !strings.Contains(string(index), `data-key="avg-lifetime"`) {
		t.Fatal("index is missing mobile sort controls or lifetime columns")
	}
	if !strings.Contains(string(index), `data-filter-table="players-table"`) {
		t.Fatal("index is missing the player search control")
	}
	if strings.Contains(string(index), `data-key="geyser"`) || strings.Contains(string(index), `data-key="incomplete-sessions"`) {
		t.Fatal("index player table still contains demoted Geyser or incomplete-session columns")
	}
	playerHTML, err := os.ReadFile(filepath.Join(dir, "players", "alice-1234.html"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(playerHTML), "<th>Quality</th>") {
		t.Fatal("recent sessions still contain the removed Quality column")
	}
	if strings.Index(string(playerHTML), "<h2>Known names</h2>") < strings.Index(string(playerHTML), "<h2>Recent sessions</h2>") {
		t.Fatal("Known names should be below Recent sessions")
	}
	chat, err := os.ReadFile(filepath.Join(dir, "chat.html"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(chat), `<script>alert`) || !strings.Contains(string(chat), `&lt;script&gt;`) {
		t.Fatal("chat message was not HTML-escaped")
	}
	css, err := os.ReadFile(filepath.Join(dir, "style.css"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(string(css)), "gradient") {
		t.Fatal("CSS contains a gradient")
	}
	if !strings.Contains(string(css), ".player-table-wrap { overflow-x: auto") || !strings.Contains(string(css), "position: sticky") {
		t.Fatal("CSS is missing the scrollable mobile player table with a pinned name column")
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("obsolete generated player page was not removed: %v", err)
	}
	if _, err := os.Stat(unrelated); err != nil {
		t.Fatalf("unrelated player file was removed: %v", err)
	}
}

func TestWriteWithProgressAndFormattingEdges(t *testing.T) {
	dir := t.TempDir()
	var updates []string
	err := WriteWithProgress(dir, analyzer.Model{Players: []*analyzer.Player{{Name: "No Login", Slug: "no-login-12345678"}}}, func(current, total int, path string) {
		updates = append(updates, path)
		if current != len(updates) || total != 5 {
			t.Errorf("progress = %d/%d at %q, want callback %d/5", current, total, path, len(updates))
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(updates) != 5 || updates[0] != "index.html" || updates[4] != filepath.Join("players", "no-login-12345678.html") {
		t.Fatalf("progress paths = %#v", updates)
	}

	date := funcs["date"].(func(time.Time) string)
	if got := date(time.Time{}); got != "—" {
		t.Errorf("date(zero) = %q", got)
	}
	if got := date(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)); got == "—" {
		t.Errorf("date(nonzero) = %q", got)
	}
	for name, fn := range map[string]func(time.Time) string{
		"day":         funcs["day"].(func(time.Time) string),
		"compactDate": funcs["compactDate"].(func(time.Time) string),
	} {
		if got := fn(time.Time{}); got != "—" {
			t.Errorf("%s(zero) = %q", name, got)
		}
		if got := fn(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)); got == "—" {
			t.Errorf("%s(nonzero) = %q", name, got)
		}
	}
	if got := funcs["sortTime"].(func(time.Time) int64)(time.Time{}); got != 0 {
		t.Errorf("sortTime(zero) = %d", got)
	}
	if got := funcs["sortTime"].(func(time.Time) int64)(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)); got == 0 {
		t.Errorf("sortTime(nonzero) = %d", got)
	}

	for _, test := range []struct {
		value time.Duration
		want  string
	}{
		{-time.Minute, "0m"},
		{0, "0m"},
		{time.Second * 10, "<1m"},
		{90 * time.Second, "2m"},
		{time.Hour, "1h"},
		{time.Hour + 2*time.Minute, "1h 2m"},
	} {
		if got := formatDuration(test.value); got != test.want {
			t.Errorf("formatDuration(%v) = %q, want %q", test.value, got, test.want)
		}
	}
}

func TestAtomicWritersCleanUpAfterFailures(t *testing.T) {
	dir := t.TempDir()
	if err := writeTemplate(filepath.Join(dir, "missing", "index.html"), indexTemplate, analyzer.Model{}); err == nil {
		t.Fatal("writeTemplate succeeded with a missing parent directory")
	}
	if err := writeTemplate(filepath.Join(dir, "player.html"), playerTemplate, playerPage{}); err == nil {
		t.Fatal("writeTemplate accepted a nil player template value")
	}
	if err := os.Mkdir(filepath.Join(dir, "target.html"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeTemplate(filepath.Join(dir, "target.html"), indexTemplate, analyzer.Model{}); err == nil {
		t.Fatal("writeTemplate replaced a directory")
	}
	if err := writeFile(filepath.Join(dir, "missing", "style.css"), []byte("css")); err == nil {
		t.Fatal("writeFile succeeded with a missing parent directory")
	}
	if err := os.Mkdir(filepath.Join(dir, "style.css"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(filepath.Join(dir, "style.css"), []byte("css")); err == nil {
		t.Fatal("writeFile replaced a directory")
	}
	if err := pruneStalePlayerPages(filepath.Join(dir, "absent"), nil); err == nil {
		t.Fatal("pruneStalePlayerPages accepted a missing directory")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".report-") {
			t.Errorf("failed atomic write left temporary file %q", entry.Name())
		}
	}
}
