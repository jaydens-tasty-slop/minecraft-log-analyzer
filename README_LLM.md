# Minecraft log analyzer: operations

## Run

Build with Go 1.23+, CGO enabled, and a C compiler:

```sh
go build -o minecraft-log-analyzer .
./minecraft-log-analyzer \
  -logs /srv/minecraft/logs \
  -db /var/lib/mclog/logs.db \
  -out /var/www/minecraft-stats \
  -server-name "My Minecraft Server" \
  -timezone America/Denver
```

The program ingests dated `YYYY-MM-DD-N.log[.gz]` archives into SQLite and
regenerates a static HTML report. Keep the database between runs. `latest.log`
is skipped because Minecraft is still appending to it. Use the server's IANA
timezone so log timestamps and session boundaries are interpreted correctly.

The report includes a full `chat.html` transcript by default. Add `-no-chat` to
omit it; player chat counts and chat statistics remain in the report. Other
useful flags: `-ingest-only` updates SQLite without writing HTML, `-report-only`
rebuilds HTML from SQLite, `-max-session 2h` caps sessions without a reliable
logout, and `-swear-words ""` disables swear counting.

## Daily cron

Install the binary at `/opt/minecraft-log-analyzer/minecraft-log-analyzer` and
create the database and report directories with write access for the cron user.
This runs daily at 04:15, prevents overlapping runs, and records progress in a
log file:

```cron
15 4 * * * flock -n /run/mclog.lock /opt/minecraft-log-analyzer/minecraft-log-analyzer -logs /srv/minecraft/logs -db /var/lib/mclog/logs.db -out /var/www/minecraft-stats -server-name "My Minecraft Server" -timezone America/Denver -no-chat >> /var/log/mclog.log 2>&1
```

Adjust paths, timezone, and schedule for the server. The database can be backed
up while the analyzer is idle. Each archive is committed transactionally and
identified by its decompressed content hash, so rerunning safely skips unchanged
archives and retries a failed ingestion on the next run.

## Data and report notes

- Source logs must be immutable dated archives; keep them available for future
  ingestion.
- `-report-only` opens the database read-only and fails if it contains data from
  an outdated parser version; run normal ingestion to rebuild it.
- Chat messages are stored in SQLite even with `-no-chat`; that flag only omits
  the generated transcript file.
- Player pages include sessions, play time, chat and command counts, deaths,
  advancements, aliases, and activity summaries. Session time without a clean
  end is estimated at a crash/restart, the end of archived logs, or the
  `-max-session` limit.

Run `go test ./...` for the project test suite.
