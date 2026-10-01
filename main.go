package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"
	"compress/gzip"

	"github.com/mattn/go-sqlite3"
)

var (
	dbPath       = flag.String("db", "", "Path to the canonical SQLite database")
	destDir      = flag.String("dest", "", "Destination directory for backups")
	interval     = flag.Duration("interval", time.Hour, "Interval between backups")
	keepHourly   = flag.Int("keep-hourly", 48, "Number of most recent backups to keep")
	keepDaily    = flag.Int("keep-daily", 14, "Number of daily backups to keep beyond the hourly limit")
	configPath   = flag.String("config", "", "Path to a global config file (unimplemented)")
)

func init() {
	// Register the SQLite driver manually if needed or let the blank import do it.
	// Since we need to use the backup API, we access the underlying connection.
	sql.Register("sqlite3_with_backup", &sqlite3.SQLiteDriver{})
}

func main() {
	flag.Parse()

	if *configPath != "" {
		log.Fatalf("Global config is not yet implemented. Use per-project flags.")
	}

	if *dbPath == "" || *destDir == "" {
		log.Fatalf("Both --db and --dest are required.")
	}

	log.Printf("Starting sqlite-backup for %s -> %s (interval: %v, hourly: %d, daily: %d days)", 
		*dbPath, *destDir, *interval, *keepHourly, *keepDaily)

	if err := os.MkdirAll(*destDir, 0755); err != nil {
		log.Fatalf("Failed to create destination directory: %v", err)
	}

	for {
		if err := runBackup(*dbPath, *destDir); err != nil {
			log.Printf("Backup failed: %v", err)
		} else {
			if err := pruneBackups(*destDir, *keepHourly, *keepDaily); err != nil {
				log.Printf("Prune failed: %v", err)
			}
		}

		time.Sleep(*interval)
	}
}

func runBackup(sourceDB, dest string) error {
	if _, err := os.Stat(sourceDB); os.IsNotExist(err) {
		return fmt.Errorf("source database does not exist: %s", sourceDB)
	}

	ts := time.Now().UTC().Format("20060102-150405")
	tempDB := filepath.Join(dest, fmt.Sprintf(".backup-%s.db", ts))
	outGz := filepath.Join(dest, fmt.Sprintf("backup-%s.db.gz", ts))

	// Step 1: Perform online backup
	if err := performOnlineBackup(sourceDB, tempDB); err != nil {
		os.Remove(tempDB)
		return fmt.Errorf("online backup failed: %w", err)
	}

	// Step 2: Integrity check the backup
	if err := checkIntegrity(tempDB); err != nil {
		os.Remove(tempDB)
		return fmt.Errorf("integrity check failed: %w", err)
	}

	// Step 3: Gzip the backup
	if err := compressFile(tempDB, outGz); err != nil {
		os.Remove(tempDB)
		os.Remove(outGz)
		return fmt.Errorf("compression failed: %w", err)
	}

	os.Remove(tempDB)
	log.Printf("Backup created successfully: %s", outGz)
	return nil
}

func performOnlineBackup(srcPath, dstPath string) error {
	srcDB, err := sql.Open("sqlite3_with_backup", srcPath)
	if err != nil {
		return err
	}
	defer srcDB.Close()

	dstDB, err := sql.Open("sqlite3_with_backup", dstPath)
	if err != nil {
		return err
	}
	defer dstDB.Close()

	srcConn, err := srcDB.Conn(context.Background())
	if err != nil {
		return err
	}
	defer srcConn.Close()

	dstConn, err := dstDB.Conn(context.Background())
	if err != nil {
		return err
	}
	defer dstConn.Close()

	return srcConn.Raw(func(src interface{}) error {
		return dstConn.Raw(func(dst interface{}) error {
			srcConnStr := src.(*sqlite3.SQLiteConn)
			dstConnStr := dst.(*sqlite3.SQLiteConn)
			
			b, err := dstConnStr.Backup("main", srcConnStr, "main")
			if err != nil {
				return err
			}
			
			// Copy all pages
			_, err = b.Step(-1)
			b.Finish()
			
			if err != nil {
				return err
			}
			return nil
		})
	})
}

func checkIntegrity(dbPath string) error {
	db, err := sql.Open("sqlite3_with_backup", dbPath)
	if err != nil {
		return err
	}
	defer db.Close()

	var result string
	err = db.QueryRow("PRAGMA integrity_check;").Scan(&result)
	if err != nil {
		return err
	}
	if result != "ok" {
		return fmt.Errorf("integrity check returned: %s", result)
	}
	return nil
}

func compressFile(srcPath, dstPath string) error {
	src, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer src.Close()

	dst, err := os.Create(dstPath)
	if err != nil {
		return err
	}
	defer dst.Close()

	gw := gzip.NewWriter(dst)
	defer gw.Close()

	_, err = io.Copy(gw, src)
	return err
}

func pruneBackups(dest string, hourly, daily int) error {
	pattern := filepath.Join(dest, "backup-*.db.gz")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return err
	}

	if len(matches) <= hourly {
		return nil
	}

	// Sort newest to oldest
	sort.Slice(matches, func(i, j int) bool {
		return matches[i] > matches[j]
	})

	// First 'hourly' are kept unconditionally
	kept := make(map[string]bool)
	for i := 0; i < hourly; i++ {
		kept[matches[i]] = true
	}

	dateRegex := regexp.MustCompile(`backup-(\d{8})-\d{6}\.db\.gz`)
	seenDays := make(map[string]bool)

	cutoffDate := time.Now().UTC().AddDate(0, 0, -daily).Format("20060102")

	for i := hourly; i < len(matches); i++ {
		match := dateRegex.FindStringSubmatch(filepath.Base(matches[i]))
		if len(match) < 2 {
			continue // Unrecognized file format
		}
		
		dayStr := match[1]

		if dayStr < cutoffDate {
			// Older than our daily keep period
			os.Remove(matches[i])
			log.Printf("Pruned (age): %s", filepath.Base(matches[i]))
			continue
		}

		if seenDays[dayStr] {
			// We already kept one for this day
			os.Remove(matches[i])
			log.Printf("Pruned (daily dedup): %s", filepath.Base(matches[i]))
		} else {
			// Keep the newest one for this day
			seenDays[dayStr] = true
			kept[matches[i]] = true
		}
	}

	return nil
}
