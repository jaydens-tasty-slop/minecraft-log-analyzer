package logparse

import (
	"strings"
	"testing"
	"time"
)

func TestParserHandlesHistoricalVariantsAndPreservesChat(t *testing.T) {
	input := strings.Join([]string{
		`[10:00:00] [Server thread/INFO]: Alice[/127.0.0.1:1234] logged in with entity id 1 at (0, 0, 0)`,
		`[10:00:01] [Server thread/INFO]: Alice joined the game`, // duplicate candidate
		`[10:01:00] [Async Chat Thread - #1/INFO]: <Alice> exact chat text, punctuation too!`,
		`[10:02:00] [Server thread/INFO]: [Not Secure] <Alice> newer chat`,
		`[10:03:00] [Server thread/INFO]: Alice issued server command: /warp home`,
		`[10:04:00] [Server thread/INFO]: Alice lost connection: Disconnected`,
		`[10:04:00] [Server thread/INFO]: Alice left the game`, // duplicate candidate
	}, "\n")
	date := time.Date(2021, 5, 6, 0, 0, 0, 0, time.UTC)
	result, err := New(date, time.UTC).Parse(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	wantTypes := []string{"login", "chat", "chat", "command", "logout"}
	if len(result.Events) != len(wantTypes) {
		t.Fatalf("got %d events (%#v), want %d", len(result.Events), result.Events, len(wantTypes))
	}
	for i, want := range wantTypes {
		if result.Events[i].Type != want {
			t.Errorf("event %d type = %q, want %q", i, result.Events[i].Type, want)
		}
	}
	if got := result.Events[1].Detail; got != "exact chat text, punctuation too!" {
		t.Errorf("chat detail = %q", got)
	}
	if got := result.Events[2].Detail; got != "newer chat" {
		t.Errorf("secure chat detail = %q", got)
	}
}

func TestParserHandlesForgeISOAndGeyser(t *testing.T) {
	input := strings.Join([]string{
		`[17Sep2026 12:34:56.123] [Server thread/INFO] [net.minecraft.server]: Bob joined the game`,
		`[2026-09-17 12:35:00] [Server thread/INFO]: <Bob> hello`,
		`[12:35:01] [Server thread/INFO]: [floodgate-bukkit] Added *BedrockBob 00000000-0000-0000-0009-0000056f1b85`,
		`[12:36:00] [Server thread/WARN]: Unexpected exception while parsing console command "geyser reload"`,
		`[12:37:00] [Paper Watchdog Thread/ERROR]: The server has stopped responding! This is (probably) not a Paper bug.`,
	}, "\n")
	date := time.Date(2026, 9, 17, 0, 0, 0, 0, time.UTC)
	result, err := New(date, time.UTC).Parse(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	wantTypes := []string{"login", "chat", "geyser_connect", "server_crash"}
	if len(result.Events) != len(wantTypes) {
		t.Fatalf("got events %#v", result.Events)
	}
	for i, want := range wantTypes {
		if result.Events[i].Type != want {
			t.Errorf("event %d type = %q, want %q", i, result.Events[i].Type, want)
		}
	}
	if got := result.Events[2].Player; got != "*BedrockBob" {
		t.Errorf("Geyser player = %q", got)
	}
}

func TestParserHandlesNewerFloodgateLogin(t *testing.T) {
	input := strings.Join([]string{
		`[16:39:02] [localSession-7-3/INFO]: [Geyser-Spigot] TheRoguenerd (logged in as: TheRoguenerd) has connected to the Java server`,
		`[16:39:02] [Server thread/INFO]: [floodgate] Floodgate player logged in as roguenerd joined (UUID: a5906657-9d12-4183-b9da-69d91d24a06e)`,
		`[16:39:02] [Server thread/INFO]: roguenerd joined the game`,
	}, "\n")
	date := time.Date(2026, 6, 6, 0, 0, 0, 0, time.UTC)
	result, err := New(date, time.UTC).Parse(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Events) != 2 || result.Events[0].Type != "geyser_connect" || result.Events[1].Type != "login" {
		t.Fatalf("events = %#v", result.Events)
	}
	if result.Events[0].Player != "roguenerd" || !strings.Contains(result.Events[0].Detail, `"bedrock_name":"TheRoguenerd"`) {
		t.Errorf("Geyser event = %#v", result.Events[0])
	}
	if len(result.Identities) != 1 || result.Identities[0].UUID != "a5906657-9d12-4183-b9da-69d91d24a06e" {
		t.Errorf("identities = %#v", result.Identities)
	}
}

func TestParserHandlesPaperCleanStopSuffix(t *testing.T) {
	input := `[12:00:00] [Server thread/INFO]: Stopping server (Ignore any thread death message you see! - DO NOT REPORT THREAD DEATH TO PAPER)`
	result, err := New(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), time.UTC).Parse(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Events) != 1 || result.Events[0].Type != "server_stop" {
		t.Fatalf("events = %#v, want a clean server stop", result.Events)
	}
}

func TestParserSeparatesFastReconnectAndRejectsDeathLookalikes(t *testing.T) {
	input := strings.Join([]string{
		`[10:00:00] [Server thread/INFO]: Alice left the game`,
		`[10:00:01] [Server thread/INFO]: Alice joined the game`,
		`[10:00:02] [Server thread/INFO]: Alice left the game`,
		`[10:01:00] [Server thread/INFO]: Alice was kicked for floating too long!`,
		`[10:02:00] [Server thread/INFO]: Alice was denied access to command.`,
		`[10:03:00] [Server thread/INFO]: Alice tried to swim in lava`,
	}, "\n")
	result, err := New(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), time.UTC).Parse(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"logout", "login", "logout", "death"}
	if len(result.Events) != len(want) {
		t.Fatalf("events = %#v", result.Events)
	}
	for index, kind := range want {
		if result.Events[index].Type != kind {
			t.Errorf("event %d = %s, want %s", index, result.Events[index].Type, kind)
		}
	}
}

func TestParserHandlesRenamedJoinAndIPv6LegacyLogin(t *testing.T) {
	input := strings.Join([]string{
		`[10:00:00] [Server thread/INFO]: _Ro1_ (formerly known as watermelon51015) joined the game`,
		`[10:01:00] [Server thread/INFO]: roguenerd[/[2605:a601:a906:1800:91bd:ce35:be9f:e5e%0]:0] logged in with entity id 3297 at (0, 64, 0)`,
	}, "\n")
	result, err := New(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), time.UTC).Parse(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Events) != 2 || result.Events[0].Player != "_Ro1_" || result.Events[1].Player != "roguenerd" {
		t.Fatalf("events = %#v", result.Events)
	}
}

func TestParserDoesNotTurnStaleGeyserHandshakeIntoLogin(t *testing.T) {
	input := strings.Join([]string{
		`[10:00:00] [localSession-1/INFO]: FailedPlayer (logged in as: FailedPlayer) has connected to the Java server`,
		`[10:02:00] [Server thread/INFO]: Alice joined the game`,
	}, "\n")
	result, err := New(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), time.UTC).Parse(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Events) != 1 || result.Events[0].Type != "login" || result.Events[0].Player != "Alice" {
		t.Fatalf("events = %#v", result.Events)
	}
}
