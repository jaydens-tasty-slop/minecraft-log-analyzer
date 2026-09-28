// Command generate-demo creates the deterministic, wholly synthetic database
// used by the public demo site.
package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"minecraft-log-analyzer/internal/logparse"
	"minecraft-log-analyzer/internal/store"
)

type demoPlayer struct {
	name     string
	oldName  string
	uuid     string
	joinYear int
	bedrock  bool
}

type demoEvent struct {
	at     time.Time
	kind   string
	player string
	detail string
	line   int
}

type demoIdentity struct {
	at     time.Time
	uuid   string
	player string
	line   int
}

var players = []demoPlayer{
	{name: "AmberFox", oldName: "CopperFox", uuid: "10000000-0000-4000-8000-000000000001", joinYear: 2018},
	{name: "BirchBuilder", uuid: "10000000-0000-4000-8000-000000000002", joinYear: 2018},
	{name: "CaveCricket", uuid: "10000000-0000-4000-8000-000000000003", joinYear: 2018},
	{name: "DawnMiner", oldName: "NightMiner", uuid: "10000000-0000-4000-8000-000000000004", joinYear: 2019},
	{name: "EchoLlama", uuid: "10000000-0000-4000-8000-000000000005", joinYear: 2019},
	{name: "FrostedBee", uuid: "10000000-0000-4000-8000-000000000006", joinYear: 2019},
	{name: "GlowSquid", uuid: "10000000-0000-4000-8000-000000000007", joinYear: 2020},
	{name: "HoneySlime", uuid: "10000000-0000-4000-8000-000000000008", joinYear: 2020},
	{name: "IronBadger", oldName: "StoneBadger", uuid: "10000000-0000-4000-8000-000000000009", joinYear: 2020},
	{name: "JuniperJay", uuid: "10000000-0000-4000-8000-000000000010", joinYear: 2021},
	{name: "KelpKnight", uuid: "10000000-0000-4000-8000-000000000011", joinYear: 2021},
	{name: "MossyMoth", uuid: "10000000-0000-4000-8000-000000000012", joinYear: 2021},
	{name: "NetherNoodle", uuid: "10000000-0000-4000-8000-000000000013", joinYear: 2022},
	{name: "OakOracle", uuid: "10000000-0000-4000-8000-000000000014", joinYear: 2022},
	{name: "PondPanda", uuid: "10000000-0000-4000-8000-000000000015", joinYear: 2023},
	{name: "QuartzQuail", uuid: "10000000-0000-4000-8000-000000000016", joinYear: 2023},
	{name: "RiverOtter", uuid: "10000000-0000-4000-8000-000000000017", joinYear: 2024, bedrock: true},
	{name: "SpruceSprite", uuid: "10000000-0000-4000-8000-000000000018", joinYear: 2025},
}

var chats = []string{
	"anyone need more oak logs?",
	"the bridge is finally finished",
	"meet at the village after sunset",
	"I found another geode",
	"bringing food and spare tools",
	"that creeper made me jump",
	"the new farm works surprisingly well",
	"coordinates are in the book by spawn",
	"thanks for helping with the roof",
	"one more tunnel and we are there",
}

var commands = []string{"/home", "/spawn", "/list", "/msg AmberFox hello", "/warp market", "/time query daytime"}
var advancements = []string{"Stone Age", "Acquire Hardware", "Diamonds!", "A Seedy Place", "We Need to Go Deeper", "Monster Hunter"}
var deaths = []string{"fell from a high place", "was slain by Zombie", "tried to swim in lava", "drowned", "blew up"}

func main() {
	out := flag.String("out", "demo/minecraft-logs-demo.db", "path for the new SQLite database")
	flag.Parse()
	if err := generate(*out); err != nil {
		log.Fatal(err)
	}
}

func generate(path string) error {
	info, err := os.Stat(path)
	if err == nil && info.Size() > 0 {
		return fmt.Errorf("refusing to overwrite non-empty database %s", path)
	}
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	db, err := store.Open(path)
	if err != nil {
		return err
	}
	closed := false
	defer func() {
		if !closed {
			db.Close()
		}
	}()

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	for year := 2018; year <= 2026; year++ {
		lastMonth := 12
		if year == 2026 {
			lastMonth = 9
		}
		for month := 1; month <= lastMonth; month++ {
			for _, day := range []int{3, 17} {
				date := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
				if err := addDay(tx, date); err != nil {
					tx.Rollback()
					return err
				}
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if _, err := db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		return err
	}
	if _, err := db.Exec(`PRAGMA journal_mode=DELETE`); err != nil {
		return err
	}
	if _, err := db.Exec(`VACUUM`); err != nil {
		return err
	}
	if err := db.Close(); err != nil {
		return err
	}
	closed = true
	return nil
}

func addDay(tx *sql.Tx, date time.Time) error {
	dayIndex := int(date.Sub(time.Date(2018, 1, 1, 0, 0, 0, 0, time.UTC)).Hours() / 24)
	active := make([]demoPlayer, 0, len(players))
	for _, player := range players {
		if player.joinYear <= date.Year() {
			active = append(active, player)
		}
	}
	participantCount := 4 + dayIndex%5
	if participantCount > len(active) {
		participantCount = len(active)
	}

	events := []demoEvent{{at: date.Add(8 * time.Hour), kind: "server_start", detail: "1.21.1", line: 1}}
	identities := make([]demoIdentity, 0, participantCount)
	line := 2
	crashDay := dayIndex%37 == 0
	uncleanDay := !crashDay && dayIndex%43 == 0
	for i := 0; i < participantCount; i++ {
		player := active[(dayIndex+i*3)%len(active)]
		name := displayName(player, date.Year())
		start := date.Add(8*time.Hour + time.Duration(5+i*11)*time.Minute)
		duration := time.Duration(45+(dayIndex+i*29)%210) * time.Minute
		end := start.Add(duration)
		if crashDay && i == 0 {
			end = date.Add(16 * time.Hour)
		}
		identities = append(identities, demoIdentity{at: start.Add(-time.Second), uuid: player.uuid, player: name, line: line})
		line++
		if player.bedrock {
			bedrockName := strings.TrimLeft(name, "*.")
			detail := fmt.Sprintf(`{"bedrock_name":%q,"uuid":%q}`, bedrockName, player.uuid)
			events = append(events, demoEvent{at: start.Add(-2 * time.Second), kind: "geyser_connect", player: name, detail: detail, line: line})
			line++
		}
		events = append(events, demoEvent{at: start, kind: "login", player: name, detail: "join", line: line})
		line++
		events = append(events, demoEvent{at: start.Add(12 * time.Minute), kind: "chat", player: name, detail: chats[(dayIndex+i)%len(chats)], line: line})
		line++
		if (dayIndex+i)%4 == 0 {
			events = append(events, demoEvent{at: start.Add(19 * time.Minute), kind: "command", player: name, detail: commands[(dayIndex+i)%len(commands)], line: line})
			line++
		}
		if (dayIndex+i)%9 == 0 {
			events = append(events, demoEvent{at: start.Add(27 * time.Minute), kind: "advancement", player: name, detail: advancements[(dayIndex+i)%len(advancements)], line: line})
			line++
		}
		if (dayIndex+i)%13 == 0 {
			events = append(events, demoEvent{at: start.Add(34 * time.Minute), kind: "death", player: name, detail: deaths[(dayIndex+i)%len(deaths)], line: line})
			line++
		}
		if (dayIndex+i)%31 == 0 {
			events = append(events, demoEvent{at: start.Add(38 * time.Minute), kind: "chat", player: name, detail: "damn, there goes another pickaxe", line: line})
			line++
		}
		if !(crashDay && i == 0) {
			events = append(events, demoEvent{at: end, kind: "logout", player: name, detail: "left", line: line})
			line++
		}
	}
	if crashDay {
		events = append(events, demoEvent{at: date.Add(15 * time.Hour), kind: "server_crash", detail: "The server has stopped responding!", line: line})
		line++
	} else if !uncleanDay {
		events = append(events, demoEvent{at: date.Add(18 * time.Hour), kind: "server_stop", line: line})
		line++
	}

	path := "demo://logs/" + date.Format("2006-01-02") + "-1.log.gz"
	digest := sha256.Sum256([]byte("minecraft-log-analyzer synthetic demo " + date.Format("2006-01-02")))
	lastAt := events[0].at
	for _, event := range events[1:] {
		if event.at.After(lastAt) {
			lastAt = event.at
		}
	}
	result, err := tx.Exec(`
		INSERT INTO source_files
			(path, content_sha256, log_date, byte_size, line_count, timestamped_line_count,
			 event_count, first_at, last_at, ingested_at, parser_version)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		path, hex.EncodeToString(digest[:]), date.Format("2006-01-02"), 4096+dayIndex%8192,
		line+20, line+10, len(events), events[0].at.Format(time.RFC3339), lastAt.Format(time.RFC3339),
		date.Add(23*time.Hour+59*time.Minute).Format(time.RFC3339), logparse.Version)
	if err != nil {
		return err
	}
	fileID, err := result.LastInsertId()
	if err != nil {
		return err
	}
	for _, event := range events {
		var playerName, playerKey any
		if event.player != "" {
			playerName = event.player
			playerKey = strings.ToLower(event.player)
		}
		if _, err := tx.Exec(`
			INSERT INTO events
				(source_file_id, line_number, occurred_at, event_type, player_name, player_key, detail)
			VALUES (?, ?, ?, ?, ?, ?, ?)`, fileID, event.line, event.at.Format(time.RFC3339), event.kind,
			playerName, playerKey, event.detail); err != nil {
			return err
		}
	}
	for _, identity := range identities {
		if _, err := tx.Exec(`
			INSERT INTO identity_observations
				(source_file_id, line_number, occurred_at, player_uuid, player_name, player_key)
			VALUES (?, ?, ?, ?, ?, ?)`, fileID, identity.line, identity.at.Format(time.RFC3339), identity.uuid,
			identity.player, strings.ToLower(identity.player)); err != nil {
			return err
		}
	}
	return nil
}

func displayName(player demoPlayer, year int) string {
	if player.oldName != "" && year < 2022 {
		return player.oldName
	}
	if player.bedrock {
		return "*" + player.name
	}
	return player.name
}
