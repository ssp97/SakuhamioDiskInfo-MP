package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

type TempRecord struct {
	DiskID     string
	DiskModel  string
	MaxTemp    float64
	AvgTemp    float64
	MinTemp    float64
	Samples    int
	RecordedAt time.Time
}

type DB struct {
	sqlDB *sql.DB
}

func Open(dbPath string) (*DB, error) {
	if dbPath == "" {
		exe, err := os.Executable()
		if err != nil {
			return nil, fmt.Errorf("get executable path: %w", err)
		}
		dbPath = filepath.Join(filepath.Dir(exe), "sakuhamio.db")
	}

	sqlDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	sqlDB.SetMaxOpenConns(1)

	db := &DB{sqlDB: sqlDB}
	if err := db.migrate(); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return db, nil
}

func (db *DB) Close() error {
	return db.sqlDB.Close()
}

func (db *DB) migrate() error {
	_, err := db.sqlDB.Exec(`
		CREATE TABLE IF NOT EXISTS temperature_history (
			disk_id      TEXT NOT NULL,
			disk_model   TEXT NOT NULL,
			max_temp     REAL NOT NULL,
			avg_temp     REAL NOT NULL,
			min_temp     REAL NOT NULL,
			sample_count INTEGER NOT NULL DEFAULT 10,
			recorded_at  TEXT NOT NULL,
			UNIQUE(disk_id, recorded_at)
		);
		CREATE INDEX IF NOT EXISTS idx_temp_history_lookup
			ON temperature_history(disk_id, recorded_at);
	`)
	return err
}

func (db *DB) InsertTempRecord(diskID, diskModel string, maxTemp, avgTemp, minTemp float64, samples int, recordedAt time.Time) error {
	_, err := db.sqlDB.Exec(
		`INSERT OR IGNORE INTO temperature_history
		(disk_id, disk_model, max_temp, avg_temp, min_temp, sample_count, recorded_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		diskID, diskModel, maxTemp, avgTemp, minTemp, samples,
		recordedAt.UTC().Format(time.RFC3339),
	)
	return err
}

type HistoryQuery struct {
	DiskID string
	Range  string
	Limit  int
}

func (db *DB) QueryTemperatureHistory(q HistoryQuery) ([]TempRecord, error) {
	var since time.Time
	switch q.Range {
	case "1h":
		since = time.Now().Add(-1 * time.Hour)
	case "7d":
		since = time.Now().Add(-7 * 24 * time.Hour)
	default:
		since = time.Now().Add(-24 * time.Hour)
	}

	limit := q.Limit
	if limit <= 0 {
		limit = 288
	}

	rows, err := db.sqlDB.Query(
		`SELECT disk_id, disk_model, max_temp, avg_temp, min_temp, sample_count, recorded_at
		FROM temperature_history
		WHERE disk_id = ? AND recorded_at >= ?
		ORDER BY recorded_at ASC
		LIMIT ?`,
		q.DiskID, since.UTC().Format(time.RFC3339), limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []TempRecord
	for rows.Next() {
		var r TempRecord
		var recordedAt string
		if err := rows.Scan(&r.DiskID, &r.DiskModel, &r.MaxTemp, &r.AvgTemp, &r.MinTemp, &r.Samples, &recordedAt); err != nil {
			return nil, err
		}
		r.RecordedAt, _ = time.Parse(time.RFC3339, recordedAt)
		records = append(records, r)
	}
	return records, rows.Err()
}
