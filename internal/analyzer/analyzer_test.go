package analyzer

import (
	"math"
	"strings"
	"testing"
	"time"

	"minecraft-log-analyzer/internal/store"
)

func TestBuildSessionsOverlapsAndCounts(t *testing.T) {
	base := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	events := []store.Event{
		{At: base, Type: "server_start"},
		{At: base, Type: "login", Player: "Alice", Key: "alice"},
		{At: base.Add(time.Hour), Type: "login", Player: "Bob", Key: "bob"},
		{At: base.Add(90 * time.Minute), Type: "chat", Player: "Alice", Key: "alice", Detail: "shit shitake SHIT"},
		{At: base.Add(2 * time.Hour), Type: "logout", Player: "Alice", Key: "alice"},
		{At: base.Add(3 * time.Hour), Type: "logout", Player: "Bob", Key: "bob"},
	}
	model := Build(events, base.Add(3*time.Hour), 1, Options{MaxSession: 24 * time.Hour, SwearWords: []string{"shit"}, Location: time.UTC})
	if model.ServerName != "Minecraft server" {
		t.Fatalf("default server name = %q", model.ServerName)
	}
	if model.Server.PeakPlayers != 2 {
		t.Fatalf("peak = %d, want 2", model.Server.PeakPlayers)
	}
	alice := findPlayer(t, model, "alice")
	if alice.TotalTime != 2*time.Hour || alice.SocialTime != time.Hour || alice.SoloTime != time.Hour {
		t.Errorf("Alice times = total %v social %v solo %v", alice.TotalTime, alice.SocialTime, alice.SoloTime)
	}
	if math.Abs(alice.LonerPercent-50) > 0.001 {
		t.Errorf("Alice loner percent = %f", alice.LonerPercent)
	}
	if alice.Swears != 2 {
		t.Errorf("Alice swears = %d, want 2", alice.Swears)
	}
	if len(alice.Peers) != 1 || alice.Peers[0].Sessions != 1 || alice.Peers[0].Duration != time.Hour {
		t.Errorf("Alice peers = %#v", alice.Peers)
	}
}

func TestUncleanRestartClosesSessionConservatively(t *testing.T) {
	base := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	events := []store.Event{
		{At: base, Type: "server_start"},
		{At: base.Add(time.Minute), Type: "login", Player: "Alice", Key: "alice"},
		{At: base.Add(time.Hour), Type: "chat", Player: "Alice", Key: "alice", Detail: "still here"},
		{At: base.Add(3 * time.Hour), Type: "server_start"}, // no stop/crash marker
	}
	model := Build(events, base.Add(4*time.Hour), 1, Options{MaxSession: 24 * time.Hour, Location: time.UTC})
	alice := findPlayer(t, model, "alice")
	if model.Server.Crashes != 1 {
		t.Errorf("crashes = %d, want 1", model.Server.Crashes)
	}
	if model.Server.ExplicitCrashes != 0 || model.Server.UncleanRestarts != 1 {
		t.Errorf("crash breakdown = explicit %d, unclean %d", model.Server.ExplicitCrashes, model.Server.UncleanRestarts)
	}
	if alice.TotalTime != 59*time.Minute || alice.InferredSessions != 1 {
		t.Errorf("session = %v, inferred = %d", alice.TotalTime, alice.InferredSessions)
	}
}

func TestReliableLogoutIsNotCapped(t *testing.T) {
	base := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	events := []store.Event{
		{At: base, Type: "login", Player: "Alice", Key: "alice"},
		{At: base.Add(30 * time.Hour), Type: "logout", Player: "Alice", Key: "alice"},
	}
	model := Build(events, events[1].At, 1, Options{MaxSession: 24 * time.Hour, Location: time.UTC})
	alice := findPlayer(t, model, "alice")
	if alice.TotalTime != 30*time.Hour || alice.InferredSessions != 0 {
		t.Fatalf("session = %v, incomplete = %d; reliable logout should be retained", alice.TotalTime, alice.InferredSessions)
	}
}

func TestMissingLogoutUsesTwoHourDefaultCap(t *testing.T) {
	base := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	events := []store.Event{{At: base, Type: "login", Player: "Alice", Key: "alice"}}
	model := Build(events, base.Add(5*time.Hour), 1, Options{Location: time.UTC})
	alice := findPlayer(t, model, "alice")
	if alice.TotalTime != 2*time.Hour || alice.InferredSessions != 1 {
		t.Fatalf("session = %v, incomplete = %d; want two-hour default cap", alice.TotalTime, alice.InferredSessions)
	}
}

func TestGeyserAliasesAreUnified(t *testing.T) {
	base := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	events := []store.Event{
		{At: base, Type: "geyser_connect", Player: "*TheRoguenerd", Key: "*theroguenerd", Detail: `{"bedrock_name":"TheRoguenerd"}`},
		{At: base, Type: "login", Player: "*TheRoguenerd", Key: "*theroguenerd"},
		{At: base.Add(time.Minute), Type: "logout", Player: "*TheRoguenerd", Key: "*theroguenerd"},
		{At: base.Add(time.Hour), Type: "geyser_connect", Player: "roguenerd", Key: "roguenerd", Detail: `{"bedrock_name":"TheRoguenerd"}`},
		{At: base.Add(time.Hour), Type: "login", Player: "roguenerd", Key: "roguenerd"},
		{At: base.Add(2 * time.Hour), Type: "logout", Player: "roguenerd", Key: "roguenerd"},
	}
	model := Build(events, base.Add(2*time.Hour), 1, Options{MaxSession: 24 * time.Hour, Location: time.UTC})
	if len(model.Players) != 1 {
		t.Fatalf("players = %d, want unified player", len(model.Players))
	}
	player := model.Players[0]
	if player.Key != "roguenerd" || player.GeyserConnections != 2 || player.SessionCount != 2 {
		t.Errorf("unified player = %#v", player)
	}
}

func TestUUIDAliasesAreUnifiedAndRetained(t *testing.T) {
	base := time.Date(2024, 1, 1, 10, 0, 0, 0, time.UTC)
	uuid := "90e0d439-65ba-4fb1-8e7d-633cde0d1a80"
	events := []store.Event{
		{At: base, Type: "login", Player: "_Ro1_", Key: "_ro1_"},
		{At: base.Add(time.Minute), Type: "logout", Player: "_Ro1_", Key: "_ro1_"},
		{At: base.Add(365 * 24 * time.Hour), Type: "login", Player: "watermelon51015", Key: "watermelon51015"},
		{At: base.Add(365*24*time.Hour + time.Minute), Type: "logout", Player: "watermelon51015", Key: "watermelon51015"},
	}
	identities := []store.Identity{
		{At: base, UUID: uuid, Player: "_Ro1_", Key: "_ro1_"},
		{At: base.Add(365 * 24 * time.Hour), UUID: uuid, Player: "watermelon51015", Key: "watermelon51015"},
	}
	model := Build(events, events[len(events)-1].At, 1, Options{MaxSession: 24 * time.Hour, Location: time.UTC, Identities: identities})
	if len(model.Players) != 1 {
		t.Fatalf("players = %d, want one UUID identity", len(model.Players))
	}
	player := model.Players[0]
	if player.UUID != uuid || player.Name != "watermelon51015" || len(player.Aliases) != 2 || player.SessionCount != 2 {
		t.Errorf("player = %#v", player)
	}
}

func TestReusedNameDoesNotBridgeDistinctUUIDs(t *testing.T) {
	base := time.Date(2024, 1, 1, 10, 0, 0, 0, time.UTC)
	events := []store.Event{
		{At: base, Type: "login", Player: "OldA", Key: "olda"},
		{At: base.Add(time.Minute), Type: "logout", Player: "OldA", Key: "olda"},
		{At: base.Add(time.Hour), Type: "login", Player: "Shared", Key: "shared"},
		{At: base.Add(time.Hour + time.Minute), Type: "logout", Player: "Shared", Key: "shared"},
		{At: base.Add(2 * time.Hour), Type: "login", Player: "NewB", Key: "newb"},
		{At: base.Add(2*time.Hour + time.Minute), Type: "logout", Player: "NewB", Key: "newb"},
	}
	identities := []store.Identity{
		{At: base, UUID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", Player: "OldA", Key: "olda"},
		{At: base.Add(time.Hour), UUID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", Player: "Shared", Key: "shared"},
		{At: base.Add(time.Hour), UUID: "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb", Player: "Shared", Key: "shared"},
		{At: base.Add(2 * time.Hour), UUID: "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb", Player: "NewB", Key: "newb"},
	}
	model := Build(events, events[len(events)-1].At, 1, Options{MaxSession: 24 * time.Hour, Location: time.UTC, Identities: identities})
	if len(model.Players) != 3 {
		t.Fatalf("players = %d, reused name bridged distinct UUID identities: %#v", len(model.Players), model.Players)
	}
	if findPlayer(t, model, "uuid:aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa").Name != "OldA" {
		t.Fatal("first UUID identity was not retained")
	}
	if findPlayer(t, model, "uuid:bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb").Name != "NewB" {
		t.Fatal("second UUID identity was not retained")
	}
	if findPlayer(t, model, "shared").UUID != "" {
		t.Fatal("ambiguous reused name was assigned a UUID")
	}
}

func TestOverviewYearsAreSortedByYearDescending(t *testing.T) {
	events := []store.Event{
		{At: time.Date(2024, 1, 1, 10, 0, 0, 0, time.UTC), Type: "login", Player: "Alice", Key: "alice"},
		{At: time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC), Type: "logout", Player: "Alice", Key: "alice"},
		{At: time.Date(2025, 1, 1, 10, 0, 0, 0, time.UTC), Type: "login", Player: "Bob", Key: "bob"},
		{At: time.Date(2025, 1, 1, 11, 0, 0, 0, time.UTC), Type: "logout", Player: "Bob", Key: "bob"},
	}
	model := Build(events, events[len(events)-1].At, 1, Options{MaxSession: 24 * time.Hour, Location: time.UTC})
	if len(model.Years) != 2 || model.Years[0].Year != 2025 || model.Years[1].Year != 2024 {
		t.Fatalf("overview years = %#v, want descending years", model.Years)
	}
}

func TestLifetimesAccumulateOnlineTimeAcrossSessions(t *testing.T) {
	base := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	events := []store.Event{
		{At: base, Type: "login", Player: "Alice", Key: "alice"},
		{At: base.Add(time.Hour), Type: "logout", Player: "Alice", Key: "alice"},
		{At: base.Add(24 * time.Hour), Type: "login", Player: "Alice", Key: "alice"},
		{At: base.Add(26 * time.Hour), Type: "death", Player: "Alice", Key: "alice"},
		{At: base.Add(26*time.Hour + 30*time.Minute), Type: "death", Player: "Alice", Key: "alice"},
		{At: base.Add(27 * time.Hour), Type: "logout", Player: "Alice", Key: "alice"},
	}
	model := Build(events, events[len(events)-1].At, 1, Options{Location: time.UTC})
	alice := findPlayer(t, model, "alice")
	if alice.LifetimeCount != 2 {
		t.Fatalf("lifetime count = %d, want 2", alice.LifetimeCount)
	}
	if alice.MinLifetime != 30*time.Minute || alice.MaxLifetime != 3*time.Hour || alice.AverageLifetime != 105*time.Minute {
		t.Fatalf("lifetimes min=%v max=%v avg=%v, want 30m, 3h, 1h45m", alice.MinLifetime, alice.MaxLifetime, alice.AverageLifetime)
	}
}

func TestBuildDefaultsAndUnusualEventPaths(t *testing.T) {
	base := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	generated := base.Add(24 * time.Hour)
	events := []store.Event{
		{At: time.Time{}, Type: "login", Player: "ZeroTime"},
		{At: base, Type: "server_start"}, // closes the zero-time session at this point
		{At: base.Add(time.Minute), Type: "server_stop"},
		{At: base.Add(2 * time.Minute), Type: "server_crash"},
		{At: base.Add(3 * time.Minute), Type: "server_crash"}, // one crash report per crash sequence
		{At: base.Add(4 * time.Minute), Type: "server_start"},
		{At: base.Add(5 * time.Minute), Type: "logout", Player: "Orphan", Key: "orphan"},
		{At: base.Add(6 * time.Minute), Type: "login", Player: "Clamp", Key: "clamp"},
		{At: base.Add(5 * time.Minute), Type: "logout", Player: "Clamp", Key: "clamp"}, // clamp an out-of-order close
		{At: base.Add(7 * time.Minute), Type: "login", Player: "Clamp", Key: "clamp"},
		{At: base.Add(7*time.Minute + 5*time.Second), Type: "login", Player: "Clamp", Key: "clamp"}, // duplicate login
		{At: base.Add(8 * time.Minute), Type: "logout", Player: "Clamp", Key: "clamp"},
		{At: base.Add(9 * time.Minute), Type: "command", Player: "Clamp", Key: "clamp"},
		{At: base.Add(9*time.Minute + time.Second), Type: "command", Player: "Clamp", Key: "clamp", Detail: "/help home"},
		{At: base.Add(10 * time.Minute), Type: "advancement", Player: "Clamp", Key: "clamp"},
		{At: base.Add(11 * time.Minute), Type: "geyser_connect", Player: "Clamp", Key: "clamp"},
		{At: base.Add(12 * time.Minute), Type: "unknown", Player: "Activity", Key: "activity"},
		{At: base.Add(13 * time.Minute), Type: "unknown"},
	}
	model := Build(events, time.Time{}, 2, Options{
		MaxSession:  -time.Second,
		SwearWords:  []string{"", "  "},
		ServerName:  "  Custom server  ",
		GeneratedAt: generated,
	})
	if model.ServerName != "Custom server" || !model.GeneratedAt.Equal(generated) || model.Server.Restarts != 2 || model.Server.Crashes != 2 || model.Server.ExplicitCrashes != 1 {
		t.Fatalf("model metadata/server stats = %#v / %#v", model.ServerName, model.Server)
	}
	if model.Server.UncleanRestarts != 1 || model.Server.TotalSessions == 0 {
		t.Fatalf("restart/session stats = %#v", model.Server)
	}
	clamp := findPlayer(t, model, "clamp")
	if clamp.Commands != 2 || len(clamp.TopCommands) != 1 || clamp.Advancements != 1 || clamp.GeyserConnections != 1 {
		t.Errorf("Clamp activity = %#v", clamp)
	}
	if !model.Server.LastPlayerActivity.Equal(base.Add(12 * time.Minute)) {
		t.Errorf("last player activity = %v", model.Server.LastPlayerActivity)
	}
}

func TestAnalyzerIdentityAndIntervalHelperBranches(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	observations := []store.Identity{
		{At: base.Add(time.Hour), UUID: "uuid-a", Player: "Older", Key: "same"},
		{At: base, UUID: "uuid-a", Player: "Oldest", Key: "same"},
		{At: base.Add(2 * time.Hour), UUID: "uuid-a", Player: "Newest", Key: "same"},
		{At: base, UUID: "uuid-b", Player: "Bedrock", Key: "bedrock"},
		{At: base, UUID: "uuid-empty", Player: "", Key: ""},
	}
	events := []store.Event{
		{At: base, Type: "geyser_connect", Player: "One", Key: "one", Detail: "not JSON"},
		{At: base, Type: "geyser_connect", Player: "Two", Key: "two", Detail: `{"bedrock_name":""}`},
		{At: base, Type: "geyser_connect", Player: "Three", Key: "three", Detail: `{"bedrock_name":"Linked"}`},
		{At: base.Add(time.Minute), Type: "geyser_connect", Player: "Four", Key: "four", Detail: `{"bedrock_name":"Linked"}`},
		{At: base.Add(2 * time.Minute), Type: "geyser_connect", Player: "Five", Key: "four", Detail: `{"bedrock_name":"Linked"}`},
	}
	index := buildIdentityIndex(events, observations, time.UTC)
	if index.canonical["same"] == "" || index.canonical["one"] == "" || index.canonical["four"] != index.canonical["three"] {
		t.Fatalf("identity canonicalization = %#v", index.canonical)
	}
	if got := len(index.aliasesByKey[index.canonical["same"]]); got != 1 {
		t.Errorf("same-name aliases = %d, want one consolidated alias", got)
	}

	activity := map[string]int{"active": 3, "quiet": 1}
	if !betterAlias("Active", "*Bedrock", activity) || !betterAlias("active", "quiet", activity) || !betterAlias("alpha", "beta", activity) {
		t.Error("betterAlias failed prefix, activity, or lexical ordering")
	}
	if commandRoot("  ") != "" || commandRoot(" /Spawn home ") != "/spawn" {
		t.Error("commandRoot did not handle empty or normalized commands")
	}
	if !strings.HasPrefix(slug("!!!", "x"), "player-") || slug("A_B!9", "x") == "" {
		t.Error("slug did not handle an empty base and punctuation")
	}

	if got := mergeIntervals(nil); got != nil {
		t.Errorf("mergeIntervals(empty) = %#v, want nil", got)
	}
	intervals := []interval{
		{start: base, end: base.Add(2 * time.Hour)},
		{start: base.Add(time.Hour), end: base.Add(90 * time.Minute)}, // contained in the first span
		{start: base.Add(3 * time.Hour), end: base.Add(4 * time.Hour)},
	}
	merged := mergeIntervals(intervals)
	if len(merged) != 2 || !merged[0].end.Equal(base.Add(2*time.Hour)) {
		t.Errorf("merged intervals = %#v", merged)
	}
}

func findPlayer(t *testing.T, model Model, key string) *Player {
	t.Helper()
	for _, player := range model.Players {
		if player.Key == key {
			return player
		}
	}
	t.Fatalf("player %q not found", key)
	return nil
}
