package report

import (
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"minecraft-log-analyzer/internal/analyzer"
)

var funcs = template.FuncMap{
	"duration": formatDuration,
	"date": func(value time.Time) string {
		if value.IsZero() {
			return "—"
		}
		return value.Format("Jan 2, 2006 3:04 PM MST")
	},
	"day": func(value time.Time) string {
		if value.IsZero() {
			return "—"
		}
		return value.Format("Jan 2, 2006")
	},
	"compactDate": func(value time.Time) string {
		if value.IsZero() {
			return "—"
		}
		return value.Format("2006-01-02")
	},
	"sortTime": func(value time.Time) int64 {
		if value.IsZero() {
			return 0
		}
		return value.Unix()
	},
	"seconds": func(value time.Duration) int64 { return int64(value.Seconds()) },
	"percent": func(value float64) string { return fmt.Sprintf("%.1f%%", value) },
	"hours":   func(value time.Duration) string { return fmt.Sprintf("%.1f", value.Hours()) },
}

var indexTemplate = template.Must(template.New("index").Funcs(funcs).Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{.ServerName}} stats</title>
  <link rel="stylesheet" href="style.css">
  <script src="table-sort.js" defer></script>
</head>
<body>
<main>
  <header>
    <h1>{{.ServerName}} stats</h1>
    <p class="muted">Through {{date .Horizon}}</p>
  </header>

  <section class="summary" aria-label="Server summary">
    <div><strong>{{len .Players}}</strong><span>Players</span></div>
    <div><strong>{{.Server.PeakPlayers}}</strong><span>Peak online</span></div>
    <div><strong>{{.Server.TotalSessions}}</strong><span>Sessions</span></div>
    <div><strong>{{duration .Server.TotalPlayTime}}</strong><span>Total play time</span></div>
    <div><strong>{{date .Server.LastPlayerActivity}}</strong><span>Last player activity</span></div>
  </section>

  <section>
    <h2>Activity overview</h2>
    <div class="chart-grid">
      <article class="chart" aria-labelledby="year-chart-title">
        <h3 id="year-chart-title">Player-hours by year</h3>
        {{range .Years}}<div class="bar-row">
          <span class="bar-label">{{.Year}}</span>
          <progress max="100" value="{{printf "%.2f" .Percent}}" aria-label="{{.Year}}: {{hours .Duration}} player-hours"></progress>
          <strong>{{hours .Duration}}h</strong>
        </div>{{end}}
      </article>
      <article class="chart" aria-labelledby="players-chart-title">
        <h3 id="players-chart-title">Top players by play time</h3>
        {{range .TopPlayers}}<div class="bar-row">
          <a class="bar-label" href="players/{{.Slug}}.html">{{.Name}}</a>
          <progress max="100" value="{{printf "%.2f" .Percent}}" aria-label="{{.Name}}: {{duration .Duration}}"></progress>
          <strong>{{hours .Duration}}h</strong>
        </div>{{end}}
      </article>
    </div>
  </section>

  <div class="table-tools">
    <label class="player-search">Search players
      <input type="search" data-filter-table="players-table" placeholder="Current name or alias" autocomplete="off">
    </label>
    <div class="mobile-sort" data-sort-controls="players-table">
      <label>Sort players
        <select>
        <option value="0" data-key="player" data-type="text">Player</option>
        <option value="1" data-key="sessions" data-type="number">Sessions</option>
        <option value="2" data-key="last-login" data-type="number" selected>Last login</option>
        <option value="3" data-key="first-login" data-type="number">First login</option>
        <option value="4" data-key="hours" data-type="number">Hours</option>
        <option value="5" data-key="social-hours" data-type="number">Social hours</option>
        <option value="6" data-key="loner" data-type="number">Loner %</option>
        <option value="7" data-key="chat" data-type="number">Chat</option>
        <option value="8" data-key="commands" data-type="number">Commands</option>
        <option value="9" data-key="swears" data-type="number">Swears</option>
        <option value="10" data-key="min-lifetime" data-type="number">Min lifetime</option>
        <option value="11" data-key="max-lifetime" data-type="number">Max lifetime</option>
        <option value="12" data-key="avg-lifetime" data-type="number">Average lifetime</option>
        </select>
      </label>
      <button type="button" data-direction="desc">Descending</button>
    </div>
  </div>
  <div class="table-wrap player-table-wrap">
    <table id="players-table" class="sortable" data-default-column="2" data-default-direction="desc">
      <thead><tr>
        <th><button type="button" data-key="player" data-type="text">Player</button></th>
        <th><button type="button" data-key="sessions" data-type="number">Sessions</button></th>
        <th aria-sort="descending"><button type="button" data-key="last-login" data-type="number">Last login</button></th>
        <th><button type="button" data-key="first-login" data-type="number">First login</button></th>
        <th><button type="button" data-key="hours" data-type="number">Hours</button></th>
        <th><button type="button" data-key="social-hours" data-type="number">Social hours</button></th>
        <th><button type="button" data-key="loner" data-type="number">Loner %</button></th>
        <th><button type="button" data-key="chat" data-type="number">Chat</button></th>
        <th><button type="button" data-key="commands" data-type="number">Cmds</button></th>
        <th><button type="button" data-key="swears" data-type="number">Swears</button></th>
        <th><button type="button" data-key="min-lifetime" data-type="number">Min life</button></th>
        <th><button type="button" data-key="max-lifetime" data-type="number">Max life</button></th>
        <th><button type="button" data-key="avg-lifetime" data-type="number">Avg life</button></th>
      </tr></thead>
      <tbody>
      {{range .Players}}<tr data-search="{{.Name}} {{range .Aliases}}{{.Name}} {{end}}">
        <td data-label="Player" data-sort="{{.Name}}"><a href="players/{{.Slug}}.html">{{.Name}}</a></td>
        <td data-label="Sessions" data-sort="{{.SessionCount}}">{{.SessionCount}}</td>
        <td data-label="Last login" data-sort="{{sortTime .LastLogin}}" title="{{date .LastLogin}}">{{compactDate .LastLogin}}</td>
        <td data-label="First login" data-sort="{{sortTime .FirstLogin}}" title="{{date .FirstLogin}}">{{compactDate .FirstLogin}}</td>
        <td data-label="Hours" data-sort="{{seconds .TotalTime}}">{{hours .TotalTime}}</td>
        <td data-label="Social hours" data-sort="{{seconds .SocialTime}}">{{hours .SocialTime}}</td>
        <td data-label="Loner" data-sort="{{printf "%.4f" .LonerPercent}}">{{percent .LonerPercent}}</td>
        <td data-label="Chat" data-sort="{{.ChatMessages}}">{{.ChatMessages}}</td>
        <td data-label="Commands" data-sort="{{.Commands}}">{{.Commands}}</td>
        <td data-label="Swears" data-sort="{{.Swears}}">{{.Swears}}</td>
        <td data-label="Min lifetime" data-sort="{{seconds .MinLifetime}}">{{if .LifetimeCount}}{{duration .MinLifetime}}{{else}}—{{end}}</td>
        <td data-label="Max lifetime" data-sort="{{seconds .MaxLifetime}}">{{if .LifetimeCount}}{{duration .MaxLifetime}}{{else}}—{{end}}</td>
        <td data-label="Average lifetime" data-sort="{{seconds .AverageLifetime}}">{{if .LifetimeCount}}{{duration .AverageLifetime}}{{else}}—{{end}}</td>
      </tr>{{end}}
      </tbody>
    </table>
  </div>

  <section class="summary secondary-summary" aria-label="Server diagnostics">
    <div><strong>{{.Server.Restarts}}</strong><span>Restart markers</span></div>
    <div><strong>{{.Server.ExplicitCrashes}}</strong><span>Confirmed crashes</span></div>
    <div><strong>{{.Server.UncleanRestarts}}</strong><span>Unclean restarts</span></div>
    <div><strong>{{.Server.GeyserConnections}}</strong><span>Geyser logins</span></div>
  </section>

  <section class="notes">
    <h2>How these numbers work</h2>
    <p>Social time is the union of time spent online with at least one other player. Loner percentage is the remaining solo time divided by total play time. Shared sessions are contiguous overlaps between two players.</p>
    <p>Each recorded death completes one observed lifetime. A lifetime begins at the previous death, or at the first tracked login, and adds only reconstructed online session time between those points; disconnected time is excluded.</p>
    <p>Sessions without a clean logout are conservatively closed at a crash, restart, the end of known logs, or the configured maximum. They are counted as incomplete on player pages. Geyser logins come from successful Floodgate identity events, not connection probes.</p>
  </section>
  <footer>Generated {{date .GeneratedAt}}</footer>
</main>
</body>
</html>`))

type playerPage struct {
	Player *analyzer.Player
	Model  analyzer.Model
}

var playerTemplate = template.Must(template.New("player").Funcs(funcs).Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{.Player.Name}} · {{.Model.ServerName}} stats</title>
  <link rel="stylesheet" href="../style.css">
</head>
<body>
<main>
  <nav><a href="../index.html">← All players</a></nav>
  <header>
    <h1>{{.Player.Name}}</h1>
    <p class="muted">First joined {{date .Player.FirstLogin}} · Last joined {{date .Player.LastLogin}}</p>
    {{if .Player.UUID}}<p class="identity">UUID <code>{{.Player.UUID}}</code></p>{{end}}
  </header>

  <section class="summary" aria-label="Player summary">
    <div><strong>{{.Player.SessionCount}}</strong><span>Sessions</span></div>
    <div><strong>{{duration .Player.TotalTime}}</strong><span>Total time</span></div>
    <div><strong>{{duration .Player.SocialTime}}</strong><span>Social time</span></div>
    <div><strong>{{percent .Player.LonerPercent}}</strong><span>Loner percentage</span></div>
    <div><strong>{{duration .Player.LongestSession}}</strong><span>Longest session</span></div>
    <div><strong>{{duration .Player.AverageSession}}</strong><span>Average session</span></div>
    <div><strong>{{if .Player.LifetimeCount}}{{duration .Player.MinLifetime}}{{else}}—{{end}}</strong><span>Minimum lifetime</span></div>
    <div><strong>{{if .Player.LifetimeCount}}{{duration .Player.MaxLifetime}}{{else}}—{{end}}</strong><span>Maximum lifetime</span></div>
    <div><strong>{{if .Player.LifetimeCount}}{{duration .Player.AverageLifetime}}{{else}}—{{end}}</strong><span>Average lifetime</span></div>
    <div><strong>{{.Player.ActiveDays}}</strong><span>Active days</span></div>
    <div><strong>{{.Player.InferredSessions}}</strong><span>Incomplete sessions</span></div>
    <div><strong>{{.Player.ChatMessages}}</strong><span>Chat messages</span></div>
    <div><strong>{{.Player.Commands}}</strong><span>Commands</span></div>
    <div><strong>{{.Player.Swears}}</strong><span>Swear words</span></div>
    <div><strong>{{.Player.GeyserConnections}}</strong><span>Geyser logins</span></div>
    <div><strong>{{.Player.Deaths}}</strong><span>Deaths</span></div>
    <div><strong>{{.Player.Advancements}}</strong><span>Advancements</span></div>
    <div><strong>{{len .Player.Aliases}}</strong><span>Known names</span></div>
  </section>

  <section class="chart-grid" aria-label="Player charts">
    <article class="chart">
      <h2>Solo and social time</h2>
      <div class="bar-row"><span class="bar-label">Social</span><progress max="100" value="{{printf "%.2f" .Player.SocialPercent}}"></progress><strong>{{percent .Player.SocialPercent}}</strong></div>
      <div class="bar-row"><span class="bar-label">Solo</span><progress max="100" value="{{printf "%.2f" .Player.LonerPercent}}"></progress><strong>{{percent .Player.LonerPercent}}</strong></div>
    </article>
    <article class="chart">
      <h2>Play time by year</h2>
      {{range .Player.Years}}<div class="bar-row"><span class="bar-label">{{.Year}}</span><progress max="100" value="{{printf "%.2f" .Percent}}"></progress><strong>{{hours .Duration}}h</strong></div>{{end}}
    </article>
  </section>

  <section>
    <h2>Played with</h2>
    {{if .Player.Peers}}<div class="table-wrap"><table>
      <thead><tr><th>Player</th><th>Shared sessions</th><th>Time together</th></tr></thead>
      <tbody class="peer-table">{{range .Player.Peers}}<tr>
        <td><a href="{{.Slug}}.html">{{.Name}}</a></td><td>{{.Sessions}}</td><td><div class="table-bar"><span>{{duration .Duration}}</span><progress max="100" value="{{printf "%.2f" .Percent}}"></progress></div></td>
      </tr>{{end}}</tbody>
    </table></div>{{else}}<p class="muted">No overlapping sessions found.</p>{{end}}
  </section>

  <div class="two-column">
    <section>
      <h2>Activity by year</h2>
      {{if .Player.Years}}<table>
        <thead><tr><th>Year</th><th>Sessions</th><th>Hours</th><th>Chat</th><th>Commands</th></tr></thead>
        <tbody>{{range .Player.Years}}<tr><td>{{.Year}}</td><td>{{.Sessions}}</td><td>{{hours .Duration}}</td><td>{{.Messages}}</td><td>{{.Commands}}</td></tr>{{end}}</tbody>
      </table>{{else}}<p class="muted">No activity found.</p>{{end}}
    </section>
    <section>
      <h2>Top commands</h2>
      {{if .Player.TopCommands}}<table>
        <thead><tr><th>Command</th><th>Count</th></tr></thead>
        <tbody>{{range .Player.TopCommands}}<tr><td><code>{{.Command}}</code></td><td><div class="table-bar"><span>{{.Count}}</span><progress max="100" value="{{printf "%.2f" .Percent}}"></progress></div></td></tr>{{end}}</tbody>
      </table>{{else}}<p class="muted">No commands found.</p>{{end}}
    </section>
  </div>

  <section>
    <h2>Recent sessions</h2>
    {{if .Player.RecentSessions}}<div class="table-wrap"><table>
      <thead><tr><th>Started</th><th>Ended</th><th>Duration</th></tr></thead>
      <tbody>{{range .Player.RecentSessions}}<tr>
        <td>{{date .Start}}</td><td>{{date .End}}</td><td>{{duration .Duration}}</td>
      </tr>{{end}}</tbody>
    </table></div>{{else}}<p class="muted">No complete player session found.</p>{{end}}
  </section>

  <section>
    <h2>Known names</h2>
    {{if .Player.Aliases}}<div class="table-wrap"><table class="alias-table">
      <thead><tr><th>Name</th><th>UUID</th><th>First observed</th><th>Last observed</th><th>Observations</th></tr></thead>
      <tbody>{{range .Player.Aliases}}<tr><td data-label="Name">{{.Name}}</td><td data-label="UUID"><code>{{.UUID}}</code></td><td data-label="First observed" title="{{date .FirstSeen}}">{{compactDate .FirstSeen}}</td><td data-label="Last observed" title="{{date .LastSeen}}">{{compactDate .LastSeen}}</td><td data-label="Observations">{{.Observations}}</td></tr>{{end}}</tbody>
    </table></div>{{else}}<p class="muted">No UUID-bearing identity line was found for this player.</p>{{end}}
  </section>
  <footer>Generated {{date .Model.GeneratedAt}}</footer>
</main>
</body>
</html>`))

var chatTemplate = template.Must(template.New("chat").Funcs(funcs).Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{.ServerName}} chat history</title>
  <link rel="stylesheet" href="style.css">
</head>
<body>
<main>
  <header>
    <h1>{{.ServerName}} chat history</h1>
    <p class="muted">All {{len .Chats}} messages, newest first.</p>
  </header>
  <div class="table-wrap"><table class="chat-table">
    <thead><tr><th>Time</th><th>Player</th><th>Message</th></tr></thead>
    <tbody>{{range .Chats}}<tr><td>{{date .At}}</td><td>{{.Player}}</td><td>{{.Text}}</td></tr>{{end}}</tbody>
  </table></div>
</main>
</body>
</html>`))

const styleCSS = `:root {
  color-scheme: light dark;
  font-family: system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
  line-height: 1.45;
  --background: #f7f7f5;
  --surface: #ffffff;
  --text: #20221f;
  --muted: #666b64;
  --border: #d8dbd5;
  --accent: #28643a;
  --accent-soft: #e8f0e9;
}
* { box-sizing: border-box; }
body { margin: 0; background: var(--background); color: var(--text); }
main { max-width: 1400px; margin: 0 auto; padding: 2rem 1.25rem 4rem; }
header { margin: 1.25rem 0 1.5rem; }
h1 { font-size: 2rem; margin: 0 0 .25rem; }
h2 { font-size: 1.25rem; margin: 2rem 0 .75rem; }
a { color: var(--accent); text-underline-offset: 2px; }
.muted, footer { color: var(--muted); }
.identity { color: var(--muted); font-size: .85rem; overflow-wrap: anywhere; }
.summary { display: grid; grid-template-columns: repeat(auto-fit, minmax(140px, 1fr)); gap: .75rem; margin-bottom: 1.5rem; }
.summary div { border: 1px solid var(--border); background: var(--surface); padding: .85rem; border-radius: 4px; }
.summary strong, .summary span { display: block; }
.summary strong { font-size: 1.2rem; overflow-wrap: anywhere; }
.summary span { color: var(--muted); font-size: .85rem; margin-top: .2rem; }
.secondary-summary { margin-top: 1.5rem; }
.table-wrap { width: 100%; border: 1px solid var(--border); background: var(--surface); }
.table-tools { display: flex; align-items: end; justify-content: space-between; gap: .75rem; margin-bottom: .75rem; }
.player-search { display: grid; width: min(22rem, 100%); gap: .25rem; color: var(--muted); font-size: .8rem; }
.player-search input { min-height: 2.4rem; border: 1px solid var(--border); border-radius: 3px; background: var(--surface); color: var(--text); padding: .4rem .55rem; }
.mobile-sort { display: none; }
table { width: 100%; border-collapse: collapse; background: var(--surface); }
th, td { text-align: left; padding: .6rem .7rem; border-bottom: 1px solid var(--border); overflow-wrap: anywhere; }
.sortable { table-layout: auto; font-size: .86rem; white-space: nowrap; }
.sortable th, .sortable td { padding: .5rem .4rem; }
.sortable th:first-child { width: 12%; }
.sortable tbody tr[hidden] { display: none; }
.chat-table { table-layout: fixed; }
.chat-table th:first-child { width: 15rem; }
.chat-table th:nth-child(2) { width: 11rem; }
.chat-table td:last-child { white-space: normal; overflow-wrap: anywhere; }
th { font-size: .82rem; color: var(--muted); background: var(--background); }
tbody tr:last-child td { border-bottom: 0; }
tbody tr:hover td { background: var(--accent-soft); }
th button { all: inherit; color: inherit; cursor: pointer; width: 100%; }
th[aria-sort="ascending"] button::after { content: " ↑"; }
th[aria-sort="descending"] button::after { content: " ↓"; }
.two-column { display: grid; grid-template-columns: repeat(auto-fit, minmax(320px, 1fr)); gap: 1.5rem; }
.chart-grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 1rem; margin-bottom: 1.5rem; }
.chart { border: 1px solid var(--border); background: var(--surface); border-radius: 4px; padding: 1rem; }
.chart h2, .chart h3 { font-size: 1rem; margin: 0 0 .9rem; }
.bar-row { display: grid; grid-template-columns: minmax(0, 8rem) minmax(4rem, 1fr) 4.7rem; align-items: center; gap: .65rem; margin: .48rem 0; font-size: .84rem; }
.bar-row strong { text-align: right; font-variant-numeric: tabular-nums; }
.bar-label { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
progress { appearance: none; width: 100%; height: .72rem; border: 0; border-radius: 2px; background: var(--border); overflow: hidden; }
progress::-webkit-progress-bar { background: var(--border); }
progress::-webkit-progress-value { background: var(--accent); }
progress::-moz-progress-bar { background: var(--accent); }
.table-bar { display: grid; grid-template-columns: auto minmax(3rem, 1fr); align-items: center; gap: .55rem; min-width: 0; }
.table-bar progress { height: .45rem; }
.peer-table .table-bar { grid-template-columns: 5.25rem minmax(3rem, 1fr); }
.peer-table .table-bar span { text-align: right; white-space: nowrap; font-variant-numeric: tabular-nums; }
.notes { max-width: 850px; }
code { font-size: .9em; }
footer { margin-top: 2.5rem; font-size: .85rem; }
@media (prefers-color-scheme: dark) {
  :root { --background: #171916; --surface: #20231f; --text: #e7e9e5; --muted: #adb3aa; --border: #3a3f38; --accent: #8bc49a; --accent-soft: #28382c; }
}
@media (max-width: 1300px) {
  main { padding: 1.25rem .75rem 3rem; }
  .chart-grid { grid-template-columns: 1fr; }
  .two-column { grid-template-columns: 1fr; }
  .table-tools { display: block; }
  .player-search { width: 100%; margin-bottom: .75rem; }
  .mobile-sort { display: flex; align-items: end; gap: .6rem; margin: 0 0 .75rem; }
  .mobile-sort label { display: grid; flex: 1; gap: .25rem; color: var(--muted); font-size: .8rem; }
  .mobile-sort select, .mobile-sort button { min-height: 2.4rem; border: 1px solid var(--border); border-radius: 3px; background: var(--surface); color: var(--text); padding: .4rem .55rem; }
  .player-table-wrap { overflow-x: auto; overscroll-behavior-inline: contain; }
  .sortable { min-width: 78rem; }
  .sortable th:first-child, .sortable td:first-child { position: sticky; left: 0; z-index: 1; background: var(--surface); box-shadow: 1px 0 0 var(--border); }
  .sortable th:first-child { z-index: 2; background: var(--background); }
  .sortable tbody tr:hover td:first-child { background: var(--accent-soft); }
  .alias-table, .alias-table tbody { display: block; background: transparent; }
  .alias-table thead { position: absolute; width: 1px; height: 1px; padding: 0; margin: -1px; overflow: hidden; clip: rect(0, 0, 0, 0); white-space: nowrap; border: 0; }
  .alias-table tbody tr { display: block; border-bottom: 1px solid var(--border); padding: .45rem .6rem; }
  .alias-table tbody tr:last-child { border-bottom: 0; }
  .alias-table tbody td { display: grid; grid-template-columns: 7rem minmax(0, 1fr); gap: .5rem; padding: .25rem; border: 0; }
  .alias-table tbody td::before { content: attr(data-label); color: var(--muted); font-size: .78rem; }
  .chat-table { table-layout: auto; }
  .chat-table th:first-child, .chat-table th:nth-child(2) { width: auto; }
}
@media (max-width: 560px) {
  .chat-table, .chat-table tbody { display: block; background: transparent; }
  .chat-table thead { position: absolute; width: 1px; height: 1px; padding: 0; margin: -1px; overflow: hidden; clip: rect(0, 0, 0, 0); white-space: nowrap; border: 0; }
  .chat-table tbody tr { display: block; padding: .65rem .75rem; border-bottom: 1px solid var(--border); }
  .chat-table tbody tr:last-child { border-bottom: 0; }
  .chat-table tbody td { display: block; width: 100%; padding: 0; border: 0; }
  .chat-table tbody td:first-child { color: var(--muted); font-size: .75rem; }
  .chat-table tbody td:nth-child(2) { margin: .12rem 0 .25rem; font-weight: 700; }
  .chat-table tbody td:last-child { overflow-wrap: anywhere; }
  .bar-row { grid-template-columns: minmax(0, 6.5rem) minmax(3rem, 1fr) 4.2rem; gap: .4rem; }
}
`

const sortJS = `document.querySelectorAll('table.sortable').forEach((table) => {
  const body = table.tBodies[0];
  const headers = Array.from(table.tHead.rows[0].cells);
  const controls = document.querySelector('[data-sort-controls="' + table.id + '"]');
  const filter = document.querySelector('[data-filter-table="' + table.id + '"]');
  const select = controls?.querySelector('select');
  const directionButton = controls?.querySelector('button');
  const sortRows = (column, direction, numeric) => {
    headers.forEach((item) => item.removeAttribute('aria-sort'));
    headers[column].setAttribute('aria-sort', direction === 'ascending' ? 'ascending' : 'descending');
    const rows = Array.from(body.rows);
    rows.sort((left, right) => {
      const a = left.cells[column].dataset.sort ?? left.cells[column].textContent.trim();
      const b = right.cells[column].dataset.sort ?? right.cells[column].textContent.trim();
      const comparison = numeric ? Number(a) - Number(b) : a.localeCompare(b, undefined, {sensitivity: 'base'});
      return direction === 'ascending' ? comparison : -comparison;
    });
    rows.forEach((row) => body.appendChild(row));
  };
  const syncControls = (column, direction) => {
    if (!select || !directionButton) return;
    select.value = String(column);
    directionButton.dataset.direction = direction === 'ascending' ? 'asc' : 'desc';
    directionButton.textContent = direction === 'ascending' ? 'Ascending' : 'Descending';
  };
  const updateHash = (column, direction) => {
    const key = headers[column].querySelector('button')?.dataset.key;
    if (!key) return;
    const fragment = new URLSearchParams({
      sort: key,
      direction: direction === 'ascending' ? 'asc' : 'desc'
    });
    history.replaceState(null, '', '#' + fragment.toString());
  };
  const applySort = (column, direction, updateURL) => {
    const button = headers[column]?.querySelector('button');
    if (!button) return;
    sortRows(column, direction, button.dataset.type === 'number');
    syncControls(column, direction);
    if (updateURL) updateHash(column, direction);
  };
  headers.forEach((header, column) => {
    const button = header.querySelector('button');
    if (!button) return;
    button.addEventListener('click', () => {
      const current = header.getAttribute('aria-sort');
      const direction = current === 'ascending' ? 'descending' : 'ascending';
      applySort(column, direction, true);
    });
  });
  if (select && directionButton) {
    const applyMobileSort = () => {
      const direction = directionButton.dataset.direction === 'asc' ? 'ascending' : 'descending';
      applySort(Number(select.value), direction, true);
    };
    select.addEventListener('change', applyMobileSort);
    directionButton.addEventListener('click', () => {
      directionButton.dataset.direction = directionButton.dataset.direction === 'asc' ? 'desc' : 'asc';
      applyMobileSort();
    });
  }
  if (filter) {
    filter.addEventListener('input', () => {
      const query = filter.value.trim().toLocaleLowerCase();
      Array.from(body.rows).forEach((row) => {
        const names = row.dataset.search ?? row.cells[0].textContent;
        row.hidden = query !== '' && !names.toLocaleLowerCase().includes(query);
      });
    });
  }
  const applyHash = () => {
    const fragment = new URLSearchParams(location.hash.slice(1));
    const key = fragment.get('sort');
    const shortDirection = fragment.get('direction');
    if (!key || (shortDirection !== 'asc' && shortDirection !== 'desc')) return;
    const column = headers.findIndex((header) => header.querySelector('button')?.dataset.key === key);
    if (column < 0) return;
    applySort(column, shortDirection === 'asc' ? 'ascending' : 'descending', false);
  };
  applyHash();
  window.addEventListener('hashchange', applyHash);
});
`

func Write(outDir string, model analyzer.Model) error {
	return WriteWithProgress(outDir, model, nil)
}

type ProgressFunc func(current, total int, path string)

// WriteWithProgress writes each page atomically and reports completed files.
func WriteWithProgress(outDir string, model analyzer.Model, progress ProgressFunc) error {
	return WriteWithOptionsProgress(outDir, model, Options{IncludeChat: true}, progress)
}

// Options controls optional report files.
type Options struct {
	IncludeChat bool
}

// WriteWithOptionsProgress writes report pages and optionally the full chat transcript.
func WriteWithOptionsProgress(outDir string, model analyzer.Model, options Options, progress ProgressFunc) error {
	if strings.TrimSpace(model.ServerName) == "" {
		model.ServerName = "Minecraft server"
	}
	playersDir := filepath.Join(outDir, "players")
	if err := os.MkdirAll(playersDir, 0o755); err != nil {
		return err
	}
	total := 3 + len(model.Players)
	if options.IncludeChat {
		total++
	}
	completed := 0
	written := func(path string) {
		completed++
		if progress != nil {
			progress(completed, total, path)
		}
	}
	if err := writeTemplate(filepath.Join(outDir, "index.html"), indexTemplate, model); err != nil {
		return err
	}
	written("index.html")
	if err := writeFile(filepath.Join(outDir, "style.css"), []byte(styleCSS)); err != nil {
		return err
	}
	written("style.css")
	if err := writeFile(filepath.Join(outDir, "table-sort.js"), []byte(sortJS)); err != nil {
		return err
	}
	written("table-sort.js")
	chatPath := filepath.Join(outDir, "chat.html")
	if options.IncludeChat {
		// This complete transcript is intentionally generated without adding a link
		// to it from any report page.
		if err := writeTemplate(chatPath, chatTemplate, model); err != nil {
			return err
		}
		written("chat.html")
	} else if err := os.Remove(chatPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, player := range model.Players {
		relativePath := filepath.Join("players", player.Slug+".html")
		path := filepath.Join(outDir, relativePath)
		if err := writeTemplate(path, playerTemplate, playerPage{Player: player, Model: model}); err != nil {
			return err
		}
		written(relativePath)
	}
	return pruneStalePlayerPages(playersDir, model.Players)
}

var generatedPlayerPageRE = regexp.MustCompile(`-[0-9a-f]{8}\.html$`)

// pruneStalePlayerPages removes obsolete pages left by renamed or newly merged
// identities. It deliberately leaves unrelated files alone, even inside the
// report's players directory.
func pruneStalePlayerPages(playersDir string, players []*analyzer.Player) error {
	wanted := make(map[string]struct{}, len(players))
	for _, player := range players {
		wanted[player.Slug+".html"] = struct{}{}
	}
	entries, err := os.ReadDir(playersDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !generatedPlayerPageRE.MatchString(name) {
			continue
		}
		if _, ok := wanted[name]; ok {
			continue
		}
		if err := os.Remove(filepath.Join(playersDir, name)); err != nil {
			return fmt.Errorf("remove obsolete player page %s: %w", name, err)
		}
	}
	return nil
}

func writeTemplate(path string, tmpl *template.Template, data any) error {
	dir := filepath.Dir(path)
	temp, err := os.CreateTemp(dir, ".report-*.tmp")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	ok := false
	defer func() {
		temp.Close()
		if !ok {
			os.Remove(tempName)
		}
	}()
	if err := tmpl.Execute(temp, data); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tempName, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tempName, path); err != nil {
		return err
	}
	ok = true
	return nil
}

func writeFile(path string, content []byte) error {
	temp, err := os.CreateTemp(filepath.Dir(path), ".report-*.tmp")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	ok := false
	defer func() {
		temp.Close()
		if !ok {
			os.Remove(tempName)
		}
	}()
	if _, err := temp.Write(content); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tempName, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tempName, path); err != nil {
		return err
	}
	ok = true
	return nil
}

func formatDuration(value time.Duration) string {
	if value < 0 {
		value = 0
	}
	minutes := int64(value.Round(time.Minute) / time.Minute)
	if value > 0 && minutes == 0 {
		return "<1m"
	}
	hours := minutes / 60
	minutes %= 60
	if hours == 0 {
		return strconv.FormatInt(minutes, 10) + "m"
	}
	if minutes == 0 {
		return strconv.FormatInt(hours, 10) + "h"
	}
	return strings.Join([]string{strconv.FormatInt(hours, 10) + "h", strconv.FormatInt(minutes, 10) + "m"}, " ")
}
