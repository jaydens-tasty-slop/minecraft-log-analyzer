> **COGNITOHAZARD:** This readme is written by a human but this project is
> otherwise 100% vibe coded

# minecraft-log-analyzer

This project analyzes your Minecraft server's logs, pulls them into a SQLite
database for analysis. Then it runs a queries to pull out statistics about
play time and sessions and spits out plain HTML reports. Intended to be run 
as a cronjob. No plugins required. Has been tested against Paper up to 1.18.2 and
about 120 unique players.

[See the demo here](https://jaydens-tasty-slop.github.io/minecraft-log-analyzer/)

Here is an incomplete list of 

### Server Stats:

- Total unique players
- Total play time in hours
  - Play time by year
  - Play time by player

### Player Stats:

- Play time (hours) (and by year)
- Play time with other players (see who plays with who)
- Sessions
- Time played
  - Social time (with at least 1 other player on)
  - Loner time (playing alone)
- Longest, shortest, and average session
- Longest, shortest, and minimum in-game lifetime
- Deaths
- Chat messages / commands
- Previous aliases

### Chat Messages:

- By default there is also a chat log rendered (but not linked to from other pages)
- Pass `-no-chat` to omit the generated `chat.html` transcript. Chat counts remain
  in player statistics.

# Usage

```sh
go build
./minecraft-log-analyzer \
  -logs /path/to/server/logs \
  -db /var/lib/mclog/minecraft-logs.db \
  -out /var/www/minecraft-stats \
  -server-name "My Minecraft Server" \
  -timezone America/Denver
```

# Installation

I have pre-built binaries for Linux, grab them from the releases tab, and
set up a cronjob if you want. 

See [The LLM's readme](./README_LLM.md) for more information than you'd ever want.
