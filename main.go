package main

import (
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"minecraft-log-analyzer/internal/analyzer"
	"minecraft-log-analyzer/internal/ingest"
	"minecraft-log-analyzer/internal/report"
	"minecraft-log-analyzer/internal/store"
)

const defaultSwears = "ass,asshole,bastard,bitch,bullshit,damn,fuck,fucking,shit"

func main() {
	log.SetFlags(log.LstdFlags)
	log.SetPrefix("mclog: ")
	os.Exit(runCommand(os.Args[1:]))
}

func runCommand(args []string) int {
	if err := run(args); err != nil {
		log.Print(err)
		return 1
	}
	return 0
}

func run(args []string) error {
	flags := flag.NewFlagSet("mclog", flag.ContinueOnError)
	var (
		logsDir     = flags.String("logs", "logs/mclogs", "directory containing dated .log and .log.gz files")
		dbPath      = flags.String("db", "minecraft-logs.db", "SQLite database path")
		outDir      = flags.String("out", "report", "static report output directory")
		serverName  = flags.String("server-name", "Minecraft server", "server name shown in generated reports")
		timezone    = flags.String("timezone", "Local", "IANA timezone used by the Minecraft server")
		ingestOnly  = flags.Bool("ingest-only", false, "ingest logs without generating reports")
		reportOnly  = flags.Bool("report-only", false, "generate reports without ingesting logs")
		maxSession  = flags.Duration("max-session", 2*time.Hour, "maximum credited duration for a session missing a reliable end")
		swearWords  = flags.String("swear-words", defaultSwears, "comma-separated whole words counted in chat (empty disables)")
		noChat      = flags.Bool("no-chat", false, "do not generate the chat transcript")
		generatedAt = flags.String("generated-at", "", "fixed RFC3339 report generation time (for reproducible builds)")
	)
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	if *ingestOnly && *reportOnly {
		return errors.New("-ingest-only and -report-only cannot be used together")
	}
	loc, err := time.LoadLocation(*timezone)
	if err != nil {
		return fmt.Errorf("load timezone %q: %w", *timezone, err)
	}
	if *maxSession <= 0 {
		return errors.New("-max-session must be positive")
	}
	var reportGeneratedAt time.Time
	if strings.TrimSpace(*generatedAt) != "" {
		reportGeneratedAt, err = time.Parse(time.RFC3339, strings.TrimSpace(*generatedAt))
		if err != nil {
			return fmt.Errorf("parse -generated-at as RFC3339: %w", err)
		}
	}
	log.Printf("starting (server=%q logs=%s db=%s out=%s timezone=%s)", strings.TrimSpace(*serverName), *logsDir, *dbPath, *outDir, loc)

	log.Printf("opening SQLite database %s", *dbPath)
	var db *sql.DB
	if *reportOnly {
		db, err = store.OpenReadOnly(*dbPath)
	} else {
		db, err = store.Open(*dbPath)
	}
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()

	if !*reportOnly {
		started := time.Now()
		result, err := ingest.DirectoryWithProgress(db, *logsDir, loc, func(progress ingest.Progress) {
			logIngestProgress(progress)
		})
		if err != nil {
			return fmt.Errorf("ingest logs: %w", err)
		}
		log.Printf("ingest complete in %s: %d new files, %d reprocessed, %d already current, %d events, %d identity observations, %d lines",
			time.Since(started).Round(time.Millisecond), result.Ingested, result.Reprocessed, result.Skipped, result.Events, result.Identities, result.Lines)
	}

	if !*ingestOnly {
		started := time.Now()
		log.Printf("report: loading events from SQLite")
		events, identities, horizon, sourceCount, err := store.LoadEvents(db)
		if err != nil {
			return fmt.Errorf("load events: %w", err)
		}
		log.Printf("report: analyzing %d events and %d identity observations from %d archives", len(events), len(identities), sourceCount)
		model := analyzer.Build(events, horizon, sourceCount, analyzer.Options{
			MaxSession:  *maxSession,
			SwearWords:  splitWords(*swearWords),
			Location:    loc,
			Identities:  identities,
			ServerName:  *serverName,
			GeneratedAt: reportGeneratedAt,
		})
		log.Printf("report: writing index, %d player pages, chat transcript=%t to %s", len(model.Players), !*noChat, *outDir)
		if err := report.WriteWithOptionsProgress(*outDir, model, report.Options{IncludeChat: !*noChat}, func(current, total int, path string) {
			if current <= 4 || current%25 == 0 || current == total {
				log.Printf("report: wrote file %d/%d: %s", current, total, path)
			}
		}); err != nil {
			return fmt.Errorf("write reports: %w", err)
		}
		log.Printf("report complete in %s: %d players written", time.Since(started).Round(time.Millisecond), len(model.Players))
	}
	log.Printf("done")
	return nil
}

func logIngestProgress(progress ingest.Progress) {
	switch progress.Phase {
	case "discovered":
		log.Printf("ingest: discovered %d immutable dated archives", progress.Total)
	case "parsing":
		if progress.Current == 1 || progress.Current%100 == 0 || progress.Current == progress.Total {
			log.Printf("ingest: streaming archive %d/%d: %s", progress.Current, progress.Total, filepath.Base(progress.Path))
		}
	}
}

func splitWords(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	words := make([]string, 0, len(parts))
	for _, part := range parts {
		if word := strings.ToLower(strings.TrimSpace(part)); word != "" {
			words = append(words, word)
		}
	}
	return words
}
