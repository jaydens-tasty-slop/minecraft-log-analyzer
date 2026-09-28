// Package logparse turns Minecraft/Paper log messages into stable, normalized
// events. It deliberately keys off message text rather than exact logger/thread
// names because those names have changed between Minecraft versions.
package logparse

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
)

// Version must be incremented whenever parsing changes require already known
// archives to have their stored events rebuilt.
const Version = 3

type Event struct {
	At     time.Time
	Type   string
	Player string
	Detail string
	Line   int
}

type Identity struct {
	At     time.Time
	Player string
	UUID   string
	Line   int
}

type Result struct {
	Events           []Event
	Identities       []Identity
	Lines            int
	TimestampedLines int
	FirstAt          time.Time
	LastAt           time.Time
}

var (
	ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*[[:alpha:]]`)
	// Vanilla/Paper: [12:34:56] [Server thread/INFO]: message
	clockRE = regexp.MustCompile(`^\[(\d{2}:\d{2}:\d{2})(?:[.,]\d+)?\]\s+\[[^]]+\](?::\s*|\s+)(.*)$`)
	// Some launchers include an ISO date in every line.
	isoRE = regexp.MustCompile(`^\[(\d{4}-\d{2}-\d{2})[ T](\d{2}:\d{2}:\d{2})(?:[.,]\d+)?\]\s+\[[^]]+\](?::\s*|\s+)(.*)$`)
	// Forge-style date: [17Sep2026 12:34:56.123] [Server thread/INFO] [...]: message
	forgeRE        = regexp.MustCompile(`^\[(\d{2}[A-Za-z]{3}\d{4}) (\d{2}:\d{2}:\d{2})(?:[.,]\d+)?\]\s+\[[^]]+\]\s*(.*)$`)
	loggerPrefixRE = regexp.MustCompile(`^\[[^]]+\](?::\s*|\s+)(.*)$`)

	joinedRE  = regexp.MustCompile(`^([A-Za-z0-9_.*-]{1,32})(?: \(formerly known as [A-Za-z0-9_.*-]{1,32}\))? joined the game$`)
	loginRE   = regexp.MustCompile(`^([A-Za-z0-9_.*-]{1,32})\[.*\] logged in with entity id\b`)
	leftRE    = regexp.MustCompile(`^([A-Za-z0-9_.*-]{1,32}) left the game$`)
	lostRE    = regexp.MustCompile(`^([A-Za-z0-9_.*-]{1,32}) lost connection:`)
	commandRE = regexp.MustCompile(`^([A-Za-z0-9_.*-]{1,32}) issued server command:\s*(.*)$`)
	chatRE    = regexp.MustCompile(`^(?:\[Not Secure\]\s*)?<([A-Za-z0-9_.*-]{1,32})>\s?(.*)$`)
	advanceRE = regexp.MustCompile(`^([A-Za-z0-9_.*-]{1,32}) has (?:made the advancement|completed the challenge|reached the goal) \[(.+)\]$`)
	deathRE   = regexp.MustCompile(`^([A-Za-z0-9_.*-]{1,32}) (?:` +
		`was (?:slain|shot|blown up|impaled|killed|burnt|fireballed|squished|poked|squashed|pricked|struck|doomed|obliterated|pummeled|skewered|roasted|stung|frozen)\b|` +
		`fell\b|drowned\b|blew up\b|burned\b|went up in flames\b|hit the ground\b|died\b|starved\b|suffocated\b|withered\b|` +
		`experienced kinetic energy\b|discovered the floor\b|didn't want to live\b|walked into\b|froze to death\b|left the confines\b|tried to swim in lava\b).*$`)
	uuidRE = regexp.MustCompile(`^UUID of player ([A-Za-z0-9_.*-]{1,32}) is ([0-9a-fA-F-]{36})$`)
	// Floodgate emits this only after a Bedrock player has authenticated. The
	// account name is the effective Java-side name, including any configured
	// prefix (commonly "*"). This makes it safer than counting raw UDP probes.
	floodgateAddedRE  = regexp.MustCompile(`^Added ([A-Za-z0-9_.*-]{1,32}) ([0-9a-fA-F-]{36})$`)
	floodgateJoinedRE = regexp.MustCompile(`^Floodgate player logged in as ([A-Za-z0-9_.*-]{1,32}) joined \(UUID: ([0-9a-fA-F-]{36})\)$`)
	geyserJavaRE      = regexp.MustCompile(`^([A-Za-z0-9_.*-]{1,32}) \(logged in as: [A-Za-z0-9_.*-]{1,32}\) has connected to (?:remote java server on address .+|the Java server)$`)
)

type geyserDetail struct {
	BedrockName string `json:"bedrock_name"`
	UUID        string `json:"uuid,omitempty"`
}

type pendingGeyserConnection struct {
	bedrockName string
	at          time.Time
}

type Parser struct {
	date time.Time
	loc  *time.Location
}

func New(date time.Time, loc *time.Location) *Parser {
	return &Parser{date: date, loc: loc}
}

func (p *Parser) Parse(r io.Reader) (Result, error) {
	var result Result
	scanner := bufio.NewScanner(r)
	// Stack traces and mod messages can be much larger than Scanner's default.
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	recentLogin := map[string]time.Time{}
	recentLogout := map[string]time.Time{}
	recentGeyser := map[string]time.Time{}
	var pendingGeyser []pendingGeyserConnection
	crashSeen := false

	for scanner.Scan() {
		result.Lines++
		at, message, ok := p.envelope(scanner.Text())
		if !ok {
			continue
		}
		result.TimestampedLines++
		if result.FirstAt.IsZero() || at.Before(result.FirstAt) {
			result.FirstAt = at
		}
		if result.LastAt.IsZero() || at.After(result.LastAt) {
			result.LastAt = at
		}

		emit := func(kind, player, detail string) {
			result.Events = append(result.Events, Event{At: at, Type: kind, Player: player, Detail: detail, Line: result.Lines})
		}
		recordIdentity := func(player, uuid string) {
			result.Identities = append(result.Identities, Identity{At: at, Player: player, UUID: strings.ToLower(uuid), Line: result.Lines})
		}
		emitGeyser := func(player, uuid string) {
			if !notRecent(recentGeyser, player, at) {
				return
			}
			bedrockName := strings.TrimLeft(player, "*.")
			valid := make([]pendingGeyserConnection, 0, len(pendingGeyser))
			for _, pending := range pendingGeyser {
				age := at.Sub(pending.at)
				if age >= 0 && age <= time.Minute {
					valid = append(valid, pending)
				}
			}
			match := -1
			for index, pending := range valid {
				if strings.EqualFold(pending.bedrockName, bedrockName) {
					match = index
					break
				}
			}
			// A linked Bedrock name may differ from its effective Java name. It
			// is safe to associate that only when exactly one handshake is live.
			if match < 0 && len(valid) == 1 {
				match = 0
			}
			if match >= 0 {
				bedrockName = valid[match].bedrockName
				valid = append(valid[:match], valid[match+1:]...)
			}
			pendingGeyser = valid
			detail, _ := json.Marshal(geyserDetail{BedrockName: bedrockName, UUID: uuid})
			emit("geyser_connect", player, string(detail))
			if uuid != "" {
				recordIdentity(player, uuid)
			}
		}

		if strings.HasPrefix(message, "Starting minecraft server version ") {
			emit("server_start", "", strings.TrimPrefix(message, "Starting minecraft server version "))
			crashSeen = false
			continue
		}
		if message == "Stopping server" || strings.HasPrefix(message, "Stopping server (") {
			emit("server_stop", "", "")
			continue
		}
		if !crashSeen && isCrash(message) {
			emit("server_crash", "", message)
			crashSeen = true
			continue
		}
		if match := uuidRE.FindStringSubmatch(message); match != nil {
			recordIdentity(match[1], match[2])
			continue
		}
		if match := joinedRE.FindStringSubmatch(message); match != nil {
			delete(recentLogout, strings.ToLower(match[1]))
			if notRecent(recentLogin, match[1], at) {
				emit("login", match[1], "join")
			}
			continue
		}
		if match := loginRE.FindStringSubmatch(message); match != nil {
			delete(recentLogout, strings.ToLower(match[1]))
			if notRecent(recentLogin, match[1], at) {
				emit("login", match[1], "legacy-login")
			}
			continue
		}
		if match := lostRE.FindStringSubmatch(message); match != nil {
			delete(recentLogin, strings.ToLower(match[1]))
			if notRecent(recentLogout, match[1], at) {
				emit("logout", match[1], strings.TrimPrefix(message, match[1]+" lost connection: "))
			}
			continue
		}
		if match := leftRE.FindStringSubmatch(message); match != nil {
			delete(recentLogin, strings.ToLower(match[1]))
			if notRecent(recentLogout, match[1], at) {
				emit("logout", match[1], "left")
			}
			continue
		}
		if match := commandRE.FindStringSubmatch(message); match != nil && !strings.EqualFold(match[1], "CONSOLE") {
			emit("command", match[1], match[2])
			continue
		}
		if match := chatRE.FindStringSubmatch(message); match != nil {
			emit("chat", match[1], match[2])
			continue
		}
		if match := advanceRE.FindStringSubmatch(message); match != nil {
			emit("advancement", match[1], match[2])
			continue
		}
		if match := floodgateAddedRE.FindStringSubmatch(message); match != nil {
			emitGeyser(match[1], match[2])
			continue
		}
		if match := floodgateJoinedRE.FindStringSubmatch(message); match != nil {
			emitGeyser(match[1], match[2])
			continue
		}
		if match := geyserJavaRE.FindStringSubmatch(message); match != nil {
			pendingGeyser = append(pendingGeyser, pendingGeyserConnection{bedrockName: match[1], at: at})
			continue
		}
		if match := deathRE.FindStringSubmatch(message); match != nil {
			emit("death", match[1], strings.TrimPrefix(message, match[1]+" "))
		}
	}
	if err := scanner.Err(); err != nil {
		return Result{}, fmt.Errorf("scan log: %w", err)
	}
	return result, nil
}

func notRecent(recent map[string]time.Time, player string, at time.Time) bool {
	key := strings.ToLower(player)
	if previous, ok := recent[key]; ok {
		delta := at.Sub(previous)
		if delta >= 0 && delta <= 3*time.Second {
			return false
		}
	}
	recent[key] = at
	return true
}

func isCrash(message string) bool {
	return message == "The server has stopped responding! This is (probably) not a Paper bug." ||
		strings.HasPrefix(message, "Encountered an unexpected exception") ||
		message == "---- Minecraft Crash Report ----" ||
		strings.HasPrefix(message, "This crash report has been saved to:")
}

func (p *Parser) envelope(line string) (time.Time, string, bool) {
	line = ansiRE.ReplaceAllString(strings.TrimSuffix(line, "\r"), "")
	if match := isoRE.FindStringSubmatch(line); match != nil {
		at, err := time.ParseInLocation("2006-01-02 15:04:05", match[1]+" "+match[2], p.loc)
		return at, normalizeMessage(match[3]), err == nil
	}
	if match := forgeRE.FindStringSubmatch(line); match != nil {
		at, err := time.ParseInLocation("02Jan2006 15:04:05", match[1]+" "+match[2], p.loc)
		return at, normalizeMessage(match[3]), err == nil
	}
	if match := clockRE.FindStringSubmatch(line); match != nil {
		clock, err := time.ParseInLocation("15:04:05", match[1], p.loc)
		if err != nil {
			return time.Time{}, "", false
		}
		at := time.Date(p.date.Year(), p.date.Month(), p.date.Day(), clock.Hour(), clock.Minute(), clock.Second(), 0, p.loc)
		return at, normalizeMessage(match[2]), true
	}
	return time.Time{}, "", false
}

func normalizeMessage(message string) string {
	message = strings.TrimLeft(message, " \t")
	if match := loggerPrefixRE.FindStringSubmatch(message); match != nil {
		return strings.TrimLeft(match[1], " \t")
	}
	return message
}
