package shareddb

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"gorm.io/gorm"
)

func TestDSN(t *testing.T) {
	got := dsn("gidbig.db")
	want := "gidbig.db?_busy_timeout=5000&_journal_mode=WAL&_txlock=immediate"
	if got != want {
		t.Errorf("dsn = %q, want %q", got, want)
	}
}

func TestDSN_KeepsExistingQuery(t *testing.T) {
	got := dsn("gidbig.db?cache=shared")
	want := "gidbig.db?cache=shared&_busy_timeout=5000&_journal_mode=WAL&_txlock=immediate"
	if got != want {
		t.Errorf("dsn = %q, want %q", got, want)
	}
	if strings.Count(got, "?") != 1 {
		t.Errorf("dsn = %q, want a single query separator", got)
	}
}

func TestOpen_EmptyPathUsesFilename(t *testing.T) {
	t.Chdir(t.TempDir())

	db, err := Open("")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db.DB: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	if _, err := os.Stat(Filename); err != nil {
		t.Errorf("stat %s: %v", Filename, err)
	}
}

func TestOpen_CapsThePoolToOneConnection(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), Filename))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db.DB: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	if got := sqlDB.Stats().MaxOpenConnections; got != 1 {
		t.Errorf("MaxOpenConnections = %d, want 1", got)
	}
}

// concurrentRow is a throwaway table used by the write-contention tests.
type concurrentRow struct {
	ID     uint `gorm:"primaryKey"`
	Worker int
	Value  int
}

// TestOpen_ConcurrentWriters runs many goroutines writing at once and asserts
// none fail with SQLITE_BUSY. Before the pool cap and immediate transactions
// this was the expected intermittent failure once every module shared the file.
func TestOpen_ConcurrentWriters(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), Filename))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })

	if err := db.AutoMigrate(&concurrentRow{}); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}

	const workers = 8
	const perWorker = 25

	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				if err := db.Create(&concurrentRow{Worker: worker, Value: i}).Error; err != nil {
					errs <- err
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent write failed: %v", err)
	}

	var count int64
	if err := db.Model(&concurrentRow{}).Count(&count).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if want := int64(workers * perWorker); count != want {
		t.Errorf("rows = %d, want %d", count, want)
	}
}

// TestOpen_ConcurrentReadThenWriteTransactions exercises the deferred
// read-then-write upgrade path that returns SQLITE_BUSY immediately when the
// transaction cannot take the write lock. With _txlock=immediate and a
// one-connection pool it must never surface.
func TestOpen_ConcurrentReadThenWriteTransactions(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), Filename))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })

	if err := db.AutoMigrate(&concurrentRow{}); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}

	const workers = 8
	const perWorker = 15

	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				err := db.Transaction(func(tx *gorm.DB) error {
					var count int64
					if err := tx.Model(&concurrentRow{}).Where("worker = ?", worker).Count(&count).Error; err != nil {
						return err
					}
					return tx.Create(&concurrentRow{Worker: worker, Value: int(count)}).Error
				})
				if err != nil {
					errs <- err
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent transaction failed: %v", err)
	}

	var count int64
	if err := db.Model(&concurrentRow{}).Count(&count).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if want := int64(workers * perWorker); count != want {
		t.Errorf("rows = %d, want %d", count, want)
	}
}
