package analyzer

import (
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"minecraft-log-analyzer/internal/store"
)

type Options struct {
	MaxSession  time.Duration
	SwearWords  []string
	Location    *time.Location
	Identities  []store.Identity
	ServerName  string
	GeneratedAt time.Time
}

type Model struct {
	ServerName  string
	GeneratedAt time.Time
	Horizon     time.Time
	Sources     int
	Server      ServerStats
	Players     []*Player
	Chats       []ChatMessage
	Years       []YearStat
	TopPlayers  []PlayerBar
}

type ChatMessage struct {
	At     time.Time
	Player string
	Text   string
}

type ServerStats struct {
	Restarts           int
	Crashes            int
	ExplicitCrashes    int
	UncleanRestarts    int
	PeakPlayers        int
	LastPlayerActivity time.Time
	TotalSessions      int
	TotalPlayTime      time.Duration
	GeyserConnections  int
}

type Player struct {
	Key               string
	UUID              string
	Name              string
	Slug              string
	SessionCount      int
	TotalTime         time.Duration
	SocialTime        time.Duration
	SoloTime          time.Duration
	LonerPercent      float64
	SocialPercent     float64
	ChatMessages      int
	Commands          int
	Swears            int
	Deaths            int
	Advancements      int
	GeyserConnections int
	ActiveDays        int
	FirstLogin        time.Time
	LastLogin         time.Time
	LongestSession    time.Duration
	AverageSession    time.Duration
	MinLifetime       time.Duration
	MaxLifetime       time.Duration
	AverageLifetime   time.Duration
	LifetimeCount     int
	InferredSessions  int
	Peers             []Peer
	RecentSessions    []SessionView
	Years             []YearStat
	TopCommands       []CommandStat
	Aliases           []Alias

	sessions   []Session
	days       map[string]struct{}
	years      map[int]*YearStat
	commands   map[string]int
	deathTimes []time.Time
}

type Alias struct {
	Name         string
	UUID         string
	FirstSeen    time.Time
	LastSeen     time.Time
	Observations int
}

type Peer struct {
	Name     string
	Slug     string
	Sessions int
	Duration time.Duration
	Percent  float64
}

type Session struct {
	Start    time.Time
	End      time.Time
	Inferred bool
}

type SessionView struct {
	Start    time.Time
	End      time.Time
	Duration time.Duration
	Inferred bool
}

type YearStat struct {
	Year     int
	Sessions int
	Duration time.Duration
	Messages int
	Commands int
	Percent  float64
}

type CommandStat struct {
	Command string
	Count   int
	Percent float64
}

type PlayerBar struct {
	Name     string
	Slug     string
	Duration time.Duration
	Percent  float64
}

type activeSession struct {
	player *Player
	start  time.Time
}

type geyserDetail struct {
	BedrockName string `json:"bedrock_name"`
}

var wordRE = regexp.MustCompile(`[[:alnum:]']+`)

func Build(events []store.Event, horizon time.Time, sourceCount int, opts Options) Model {
	if opts.Location == nil {
		opts.Location = time.Local
	}
	if opts.MaxSession <= 0 {
		opts.MaxSession = 2 * time.Hour
	}
	if !horizon.IsZero() {
		horizon = horizon.In(opts.Location)
	}
	serverName := strings.TrimSpace(opts.ServerName)
	if serverName == "" {
		serverName = "Minecraft server"
	}
	generatedAt := opts.GeneratedAt
	if generatedAt.IsZero() {
		generatedAt = time.Now()
	}
	model := Model{ServerName: serverName, GeneratedAt: generatedAt.In(opts.Location), Horizon: horizon, Sources: sourceCount}
	identities := buildIdentityIndex(events, opts.Identities, opts.Location)
	players := map[string]*Player{}
	active := map[string]activeSession{}
	swears := make(map[string]struct{}, len(opts.SwearWords))
	for _, word := range opts.SwearWords {
		if word = strings.ToLower(strings.TrimSpace(word)); word != "" {
			swears[word] = struct{}{}
		}
	}

	getPlayer := func(event store.Event) *Player {
		key := event.Key
		if key == "" {
			key = strings.ToLower(event.Player)
		}
		player := players[key]
		if player == nil {
			player = &Player{Key: key, Name: event.Player, days: map[string]struct{}{}, years: map[int]*YearStat{}, commands: map[string]int{}}
			players[key] = player
		}
		if event.Player != "" {
			player.Name = event.Player
		}
		return player
	}
	closeSession := func(key string, end time.Time, inferred bool) {
		open, ok := active[key]
		if !ok {
			return
		}
		if end.Before(open.start) {
			end = open.start
			inferred = true
		}
		if inferred && end.Sub(open.start) > opts.MaxSession {
			end = open.start.Add(opts.MaxSession)
			inferred = true
		}
		open.player.sessions = append(open.player.sessions, Session{Start: open.start, End: end, Inferred: inferred})
		delete(active, key)
	}
	closeAll := func(end time.Time, inferred bool) {
		for key := range active {
			closeSession(key, end, inferred)
		}
	}

	lastLifecycle := ""
	crashLatched := false
	var previousAt time.Time
	for _, event := range events {
		event.At = event.At.In(opts.Location)
		if canonical, ok := identities.canonical[event.Key]; ok {
			event.Key = canonical
		}
		switch event.Type {
		case "server_start":
			model.Server.Restarts++
			unclean := lastLifecycle == "server_start" || len(active) > 0
			if unclean && !crashLatched {
				model.Server.Crashes++
				model.Server.UncleanRestarts++
			}
			if len(active) > 0 {
				closeAt := previousAt
				if closeAt.IsZero() {
					closeAt = event.At
				}
				closeAll(closeAt, true)
			}
			lastLifecycle = "server_start"
			crashLatched = false
		case "server_stop":
			// A clean server stop is a reliable session boundary even when the
			// server does not emit a separate leave line for every player.
			closeAll(event.At, false)
			lastLifecycle = "server_stop"
		case "server_crash":
			if !crashLatched {
				model.Server.Crashes++
				model.Server.ExplicitCrashes++
				crashLatched = true
			}
			closeAll(event.At, true)
			lastLifecycle = "server_crash"
		default:
			if event.Player != "" {
				if event.At.After(model.Server.LastPlayerActivity) {
					model.Server.LastPlayerActivity = event.At
				}
			}
		}

		if event.Player == "" {
			previousAt = event.At
			continue
		}
		player := getPlayer(event)
		if player.UUID == "" {
			player.UUID = identities.uuidByKey[event.Key]
		}
		if len(player.Aliases) == 0 {
			player.Aliases = identities.aliasesByKey[event.Key]
		}
		year := event.At.In(opts.Location).Year()
		yearStat := ensureYear(player, year)
		switch event.Type {
		case "login":
			if open, ok := active[player.Key]; ok {
				// A second join/login message is common in some server versions.
				if event.At.Sub(open.start) <= 10*time.Second {
					break
				}
				closeSession(player.Key, event.At, true)
			}
			active[player.Key] = activeSession{player: player, start: event.At}
		case "logout":
			closeSession(player.Key, event.At, false)
		case "chat":
			player.ChatMessages++
			yearStat.Messages++
			player.Swears += countSwears(event.Detail, swears)
			model.Chats = append(model.Chats, ChatMessage{At: event.At, Player: event.Player, Text: event.Detail})
		case "command":
			player.Commands++
			yearStat.Commands++
			if command := commandRoot(event.Detail); command != "" {
				player.commands[command]++
			}
		case "death":
			player.Deaths++
			player.deathTimes = append(player.deathTimes, event.At)
		case "advancement":
			player.Advancements++
		case "geyser_connect":
			player.GeyserConnections++
			model.Server.GeyserConnections++
		}
		previousAt = event.At
	}
	if horizon.IsZero() {
		horizon = previousAt
	}
	closeAll(horizon, true)

	for _, player := range players {
		finalizePlayer(player, opts.Location)
		model.Server.TotalSessions += player.SessionCount
		model.Server.TotalPlayTime += player.TotalTime
		model.Players = append(model.Players, player)
	}
	addPeerStats(model.Players)
	model.Server.PeakPlayers = peakPlayers(model.Players)
	buildOverview(&model)
	sort.Slice(model.Players, func(i, j int) bool {
		left, right := model.Players[i], model.Players[j]
		if left.LastLogin.Equal(right.LastLogin) {
			return strings.ToLower(left.Name) < strings.ToLower(right.Name)
		}
		return left.LastLogin.After(right.LastLogin)
	})
	for i, j := 0, len(model.Chats)-1; i < j; i, j = i+1, j-1 {
		model.Chats[i], model.Chats[j] = model.Chats[j], model.Chats[i]
	}
	return model
}

type identityIndex struct {
	canonical    map[string]string
	uuidByKey    map[string]string
	aliasesByKey map[string][]Alias
}

type unionFind struct{ parent map[string]string }

func (u *unionFind) add(value string) {
	if value != "" {
		if _, ok := u.parent[value]; !ok {
			u.parent[value] = value
		}
	}
}

func (u *unionFind) find(value string) string {
	parent := u.parent[value]
	if parent == value {
		return value
	}
	u.parent[value] = u.find(parent)
	return u.parent[value]
}

func (u *unionFind) union(left, right string) {
	u.add(left)
	u.add(right)
	if left == "" || right == "" {
		return
	}
	leftRoot, rightRoot := u.find(left), u.find(right)
	if leftRoot != rightRoot {
		u.parent[rightRoot] = leftRoot
	}
}

func buildIdentityIndex(events []store.Event, observations []store.Identity, loc *time.Location) identityIndex {
	index := identityIndex{canonical: map[string]string{}, uuidByKey: map[string]string{}, aliasesByKey: map[string][]Alias{}}
	uf := &unionFind{parent: map[string]string{}}
	activity := map[string]int{}
	for _, event := range events {
		if event.Key != "" {
			uf.add(event.Key)
			activity[event.Key]++
		}
	}

	uuidsByName := map[string]map[string]struct{}{}
	for _, observation := range observations {
		uf.add(observation.Key)
		if uuidsByName[observation.Key] == nil {
			uuidsByName[observation.Key] = map[string]struct{}{}
		}
		uuidsByName[observation.Key][observation.UUID] = struct{}{}
	}
	firstByUUID := map[string]string{}
	for _, observation := range observations {
		// A name seen with multiple UUIDs may have been reused. Keep its
		// events as an ambiguous name-only identity instead of allowing it to
		// transitively merge otherwise distinct UUID-backed people.
		if len(uuidsByName[observation.Key]) != 1 {
			continue
		}
		if first := firstByUUID[observation.UUID]; first != "" {
			uf.union(first, observation.Key)
		} else {
			firstByUUID[observation.UUID] = observation.Key
		}
	}
	firstByBedrock := map[string]string{}
	for _, event := range events {
		if event.Type != "geyser_connect" {
			continue
		}
		var detail geyserDetail
		if json.Unmarshal([]byte(event.Detail), &detail) != nil || detail.BedrockName == "" {
			continue
		}
		bedrockKey := strings.ToLower(detail.BedrockName)
		if first := firstByBedrock[bedrockKey]; first != "" {
			uf.union(first, event.Key)
		} else {
			firstByBedrock[bedrockKey] = event.Key
		}
	}

	components := map[string][]string{}
	for alias := range uf.parent {
		root := uf.find(alias)
		components[root] = append(components[root], alias)
	}
	componentKey := map[string]string{}
	for root, aliases := range components {
		aliasSet := map[string]struct{}{}
		for _, alias := range aliases {
			aliasSet[alias] = struct{}{}
		}
		uuidScores := map[string]int{}
		seenPair := map[string]struct{}{}
		for _, observation := range observations {
			if _, ok := aliasSet[observation.Key]; !ok {
				continue
			}
			if len(uuidsByName[observation.Key]) != 1 {
				continue
			}
			pair := observation.UUID + "\x00" + observation.Key
			if _, ok := seenPair[pair]; ok {
				continue
			}
			seenPair[pair] = struct{}{}
			uuidScores[observation.UUID] += activity[observation.Key] + 1
		}
		primaryUUID, bestScore := "", -1
		for uuid, score := range uuidScores {
			if score > bestScore || (score == bestScore && uuid < primaryUUID) {
				primaryUUID, bestScore = uuid, score
			}
		}
		key := ""
		if primaryUUID != "" {
			key = "uuid:" + primaryUUID
		} else {
			for _, alias := range aliases {
				if key == "" || betterAlias(alias, key, activity) {
					key = alias
				}
			}
		}
		componentKey[root] = key
		index.uuidByKey[key] = primaryUUID
		for _, alias := range aliases {
			index.canonical[alias] = key
		}
	}

	type aliasKey struct{ identity, uuid, name string }
	aliasStats := map[aliasKey]*Alias{}
	for _, observation := range observations {
		root := uf.find(observation.Key)
		key := componentKey[root]
		statKey := aliasKey{identity: key, uuid: observation.UUID, name: observation.Key}
		stat := aliasStats[statKey]
		at := observation.At.In(loc)
		if stat == nil {
			stat = &Alias{Name: observation.Player, UUID: observation.UUID, FirstSeen: at, LastSeen: at}
			aliasStats[statKey] = stat
		}
		stat.Observations++
		if at.Before(stat.FirstSeen) {
			stat.FirstSeen = at
		}
		if at.After(stat.LastSeen) {
			stat.LastSeen = at
			stat.Name = observation.Player
		}
	}
	for key, stat := range aliasStats {
		index.aliasesByKey[key.identity] = append(index.aliasesByKey[key.identity], *stat)
	}
	for key := range index.aliasesByKey {
		sort.Slice(index.aliasesByKey[key], func(i, j int) bool {
			left, right := index.aliasesByKey[key][i], index.aliasesByKey[key][j]
			if left.LastSeen.Equal(right.LastSeen) {
				return strings.ToLower(left.Name) < strings.ToLower(right.Name)
			}
			return left.LastSeen.After(right.LastSeen)
		})
	}
	return index
}

func betterAlias(candidate, current string, activity map[string]int) bool {
	prefixed := func(value string) bool { return strings.HasPrefix(value, "*") || strings.HasPrefix(value, ".") }
	if prefixed(candidate) != prefixed(current) {
		return !prefixed(candidate)
	}
	if activity[candidate] != activity[current] {
		return activity[candidate] > activity[current]
	}
	return candidate < current
}

func ensureYear(player *Player, year int) *YearStat {
	stat := player.years[year]
	if stat == nil {
		stat = &YearStat{Year: year}
		player.years[year] = stat
	}
	return stat
}

func finalizePlayer(player *Player, loc *time.Location) {
	sort.Slice(player.sessions, func(i, j int) bool { return player.sessions[i].Start.Before(player.sessions[j].Start) })
	for _, session := range player.sessions {
		duration := session.End.Sub(session.Start)
		player.SessionCount++
		player.TotalTime += duration
		if duration > player.LongestSession {
			player.LongestSession = duration
		}
		if session.Inferred {
			player.InferredSessions++
		}
		if player.FirstLogin.IsZero() || session.Start.Before(player.FirstLogin) {
			player.FirstLogin = session.Start
		}
		if session.Start.After(player.LastLogin) {
			player.LastLogin = session.Start
		}
		day := session.Start.In(loc).Format("2006-01-02")
		player.days[day] = struct{}{}
		ensureYear(player, session.Start.In(loc).Year()).Sessions++
		addDurationByYear(player, session.Start, session.End, loc)
		player.RecentSessions = append(player.RecentSessions, SessionView{
			Start: session.Start, End: session.End, Duration: duration, Inferred: session.Inferred,
		})
	}
	player.ActiveDays = len(player.days)
	if player.SessionCount > 0 {
		player.AverageSession = player.TotalTime / time.Duration(player.SessionCount)
	}
	finalizeLifetimes(player)
	if len(player.RecentSessions) > 50 {
		player.RecentSessions = player.RecentSessions[len(player.RecentSessions)-50:]
	}
	for i, j := 0, len(player.RecentSessions)-1; i < j; i, j = i+1, j-1 {
		player.RecentSessions[i], player.RecentSessions[j] = player.RecentSessions[j], player.RecentSessions[i]
	}
	for _, stat := range player.years {
		player.Years = append(player.Years, *stat)
	}
	sort.Slice(player.Years, func(i, j int) bool { return player.Years[i].Year > player.Years[j].Year })
	var maxYear time.Duration
	for _, stat := range player.Years {
		if stat.Duration > maxYear {
			maxYear = stat.Duration
		}
	}
	if maxYear > 0 {
		for index := range player.Years {
			player.Years[index].Percent = float64(player.Years[index].Duration) / float64(maxYear) * 100
		}
	}
	for command, count := range player.commands {
		player.TopCommands = append(player.TopCommands, CommandStat{Command: command, Count: count})
	}
	sort.Slice(player.TopCommands, func(i, j int) bool {
		if player.TopCommands[i].Count == player.TopCommands[j].Count {
			return player.TopCommands[i].Command < player.TopCommands[j].Command
		}
		return player.TopCommands[i].Count > player.TopCommands[j].Count
	})
	if len(player.TopCommands) > 10 {
		player.TopCommands = player.TopCommands[:10]
	}
	if len(player.TopCommands) > 0 {
		maxCount := player.TopCommands[0].Count
		for index := range player.TopCommands {
			player.TopCommands[index].Percent = float64(player.TopCommands[index].Count) / float64(maxCount) * 100
		}
	}
	player.Slug = slug(player.Name, player.Key)
}

func addDurationByYear(player *Player, start, end time.Time, loc *time.Location) {
	for cursor := start; cursor.Before(end); {
		local := cursor.In(loc)
		nextYear := time.Date(local.Year()+1, 1, 1, 0, 0, 0, 0, loc)
		partEnd := end
		if nextYear.Before(partEnd) {
			partEnd = nextYear
		}
		ensureYear(player, local.Year()).Duration += partEnd.Sub(cursor)
		cursor = partEnd
	}
}

func addPeerStats(players []*Player) {
	byKey := make(map[string]*Player, len(players))
	for _, player := range players {
		byKey[player.Key] = player
	}
	social := make(map[string][]interval, len(players))
	for i := 0; i < len(players); i++ {
		for j := i + 1; j < len(players); j++ {
			overlaps := intersections(players[i].sessions, players[j].sessions)
			if len(overlaps) == 0 {
				continue
			}
			var duration time.Duration
			for _, overlap := range overlaps {
				duration += overlap.end.Sub(overlap.start)
			}
			players[i].Peers = append(players[i].Peers, Peer{Name: players[j].Name, Slug: players[j].Slug, Sessions: len(overlaps), Duration: duration})
			players[j].Peers = append(players[j].Peers, Peer{Name: players[i].Name, Slug: players[i].Slug, Sessions: len(overlaps), Duration: duration})
			social[players[i].Key] = append(social[players[i].Key], overlaps...)
			social[players[j].Key] = append(social[players[j].Key], overlaps...)
		}
	}
	for key, intervals := range social {
		merged := mergeIntervals(intervals)
		for _, span := range merged {
			byKey[key].SocialTime += span.end.Sub(span.start)
		}
	}
	for _, player := range players {
		if player.SocialTime > player.TotalTime {
			player.SocialTime = player.TotalTime
		}
		player.SoloTime = player.TotalTime - player.SocialTime
		if player.TotalTime > 0 {
			player.LonerPercent = float64(player.SoloTime) / float64(player.TotalTime) * 100
			player.SocialPercent = 100 - player.LonerPercent
		}
		sort.Slice(player.Peers, func(i, j int) bool {
			if player.Peers[i].Duration == player.Peers[j].Duration {
				return strings.ToLower(player.Peers[i].Name) < strings.ToLower(player.Peers[j].Name)
			}
			return player.Peers[i].Duration > player.Peers[j].Duration
		})
		if len(player.Peers) > 0 && player.Peers[0].Duration > 0 {
			maximum := player.Peers[0].Duration
			for index := range player.Peers {
				player.Peers[index].Percent = float64(player.Peers[index].Duration) / float64(maximum) * 100
			}
		}
	}
}

func buildOverview(model *Model) {
	years := map[int]*YearStat{}
	for _, player := range model.Players {
		for _, playerYear := range player.Years {
			year := years[playerYear.Year]
			if year == nil {
				year = &YearStat{Year: playerYear.Year}
				years[playerYear.Year] = year
			}
			year.Sessions += playerYear.Sessions
			year.Duration += playerYear.Duration
			year.Messages += playerYear.Messages
			year.Commands += playerYear.Commands
		}
	}
	var maxYear time.Duration
	for _, year := range years {
		model.Years = append(model.Years, *year)
		if year.Duration > maxYear {
			maxYear = year.Duration
		}
	}
	sort.Slice(model.Years, func(i, j int) bool { return model.Years[i].Year > model.Years[j].Year })
	if maxYear > 0 {
		for index := range model.Years {
			model.Years[index].Percent = float64(model.Years[index].Duration) / float64(maxYear) * 100
		}
	}

	players := append([]*Player(nil), model.Players...)
	sort.Slice(players, func(i, j int) bool {
		if players[i].TotalTime == players[j].TotalTime {
			return strings.ToLower(players[i].Name) < strings.ToLower(players[j].Name)
		}
		return players[i].TotalTime > players[j].TotalTime
	})
	if len(players) > 10 {
		players = players[:10]
	}
	var maximum time.Duration
	if len(players) > 0 {
		maximum = players[0].TotalTime
	}
	for _, player := range players {
		percent := 0.0
		if maximum > 0 {
			percent = float64(player.TotalTime) / float64(maximum) * 100
		}
		model.TopPlayers = append(model.TopPlayers, PlayerBar{Name: player.Name, Slug: player.Slug, Duration: player.TotalTime, Percent: percent})
	}
}

func finalizeLifetimes(player *Player) {
	if len(player.sessions) == 0 || len(player.deathTimes) == 0 {
		return
	}
	sort.Slice(player.deathTimes, func(i, j int) bool { return player.deathTimes[i].Before(player.deathTimes[j]) })
	start := player.sessions[0].Start
	var total time.Duration
	for _, death := range player.deathTimes {
		if death.Before(start) {
			// Even when the death predates the first usable session, it is a
			// reliable boundary for a later, fully observed life.
			start = death
			continue
		}
		if !onlineAt(player.sessions, death) {
			// Do not manufacture a completed life from a death line that cannot
			// be placed inside one of this player's reconstructed sessions.
			start = death
			continue
		}
		duration := onlineDurationBetween(player.sessions, start, death)
		player.LifetimeCount++
		total += duration
		if player.LifetimeCount == 1 || duration < player.MinLifetime {
			player.MinLifetime = duration
		}
		if duration > player.MaxLifetime {
			player.MaxLifetime = duration
		}
		start = death
	}
	if player.LifetimeCount > 0 {
		player.AverageLifetime = total / time.Duration(player.LifetimeCount)
	}
}

func onlineAt(sessions []Session, at time.Time) bool {
	for _, session := range sessions {
		if session.Start.After(at) {
			return false
		}
		if !at.After(session.End) {
			return true
		}
	}
	return false
}

func onlineDurationBetween(sessions []Session, start, end time.Time) time.Duration {
	var total time.Duration
	for _, session := range sessions {
		if !session.End.After(start) {
			continue
		}
		if !session.Start.Before(end) {
			break
		}
		partStart := session.Start
		if start.After(partStart) {
			partStart = start
		}
		partEnd := session.End
		if end.Before(partEnd) {
			partEnd = end
		}
		if partEnd.After(partStart) {
			total += partEnd.Sub(partStart)
		}
	}
	return total
}

type interval struct{ start, end time.Time }

func intersections(a, b []Session) []interval {
	var result []interval
	for i, j := 0, 0; i < len(a) && j < len(b); {
		start := a[i].Start
		if b[j].Start.After(start) {
			start = b[j].Start
		}
		end := a[i].End
		if b[j].End.Before(end) {
			end = b[j].End
		}
		if end.After(start) {
			result = append(result, interval{start, end})
		}
		if a[i].End.Before(b[j].End) {
			i++
		} else {
			j++
		}
	}
	return mergeIntervals(result)
}

func mergeIntervals(items []interval) []interval {
	if len(items) == 0 {
		return nil
	}
	sort.Slice(items, func(i, j int) bool { return items[i].start.Before(items[j].start) })
	merged := []interval{items[0]}
	for _, item := range items[1:] {
		last := &merged[len(merged)-1]
		if !item.start.After(last.end) {
			if item.end.After(last.end) {
				last.end = item.end
			}
			continue
		}
		merged = append(merged, item)
	}
	return merged
}

func peakPlayers(players []*Player) int {
	type point struct {
		at    time.Time
		delta int
	}
	var points []point
	for _, player := range players {
		for _, session := range player.sessions {
			if session.End.After(session.Start) {
				points = append(points, point{session.Start, 1}, point{session.End, -1})
			}
		}
	}
	sort.Slice(points, func(i, j int) bool {
		if points[i].at.Equal(points[j].at) {
			return points[i].delta < points[j].delta
		}
		return points[i].at.Before(points[j].at)
	})
	current, peak := 0, 0
	for _, point := range points {
		current += point.delta
		if current > peak {
			peak = current
		}
	}
	return peak
}

func countSwears(message string, swears map[string]struct{}) int {
	count := 0
	for _, word := range wordRE.FindAllString(strings.ToLower(message), -1) {
		if _, ok := swears[word]; ok {
			count++
		}
	}
	return count
}

func commandRoot(command string) string {
	fields := strings.Fields(strings.TrimPrefix(strings.TrimSpace(command), "/"))
	if len(fields) == 0 {
		return ""
	}
	return "/" + strings.ToLower(fields[0])
}

func slug(name, key string) string {
	var clean strings.Builder
	lastDash := false
	for _, char := range strings.ToLower(name) {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') {
			clean.WriteRune(char)
			lastDash = false
		} else if !lastDash {
			clean.WriteByte('-')
			lastDash = true
		}
	}
	base := strings.Trim(clean.String(), "-")
	if base == "" {
		base = "player"
	}
	digest := sha1.Sum([]byte(key))
	return fmt.Sprintf("%s-%x", base, digest[:4])
}
