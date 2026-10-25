package storage

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

const logFileName = "data.log"

var (
	ErrClosed = errors.New("database is closed")

	ErrRecoveryRequired = errors.New("non-empty log required recovery")

	ErrAlreadyOpen = errors.New("database is already open")
)

// indexEntry describes the location of a record in the log
type indexEntry struct {
	recordOffset int64

	recordSize int64

	timestamp uint64
}

type DB struct {
	mu sync.RWMutex //mutex for read-write opns

	dir     string   //remembers the database directory
	logFile *os.File //hold the open log file

	keydir      map[string]indexEntry //find the latest record for a key
	writeOffset int64                 //track where the next record should be written

	closed bool  //prevent opns after closing
	failed error //remember a write failure that makes the db unsafe to keep using

	now func() time.Time //supply the timestamps
}

// Open opens or creates a database in dir
func Open(dir string) (*DB, error) {
	if dir == "" {
		return nil, errors.New("database directory must not be empty")
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create database directory : %w", err)
	}

	logPath := filepath.Join(dir, logFileName)

	//create the file if needed, or open the existing file
	file, err := os.OpenFile(
		logPath,
		os.O_CREATE|os.O_RDWR,
		0o600,
	)
	if err != nil {
		return nil, fmt.Errorf("open database log : %w", err)
	}

	//lock the file so two processes cannot open the same database simultaneously
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()

		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("%w: %s", ErrAlreadyOpen, dir)
		}
		return nil, fmt.Errorf("lock database log : %w", err)
	}

	// file.Stat() returns info about the file(like its metadata)
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("stat database log: %w", err)
	}

	//check whether the log contains data, if yes, reject it(recovery is not implemented yet)
	if info.Size() != 0 {
		_ = file.Close()
		return nil, fmt.Errorf(
			"%w: %s",
			ErrRecoveryRequired,
			logPath,
		)
	}

	db := &DB{
		dir:         dir,
		logFile:     file,
		keydir:      make(map[string]indexEntry),
		writeOffset: 0,
		now:         time.Now,
	}

	return db, nil

}

// Close syncs and closes the database log
func (db *DB) Close() error {

	// mutex ensures only one goroutine can hold the lock
	db.mu.Lock()
	defer db.mu.Unlock()

	if db.closed {
		return ErrClosed
	}

	db.closed = true

	syncErr := db.logFile.Sync() //sync file's contents to stable storage

	unlockErr := syscall.Flock(int(db.logFile.Fd()), syscall.LOCK_UN) //release the os level file lock

	closeErr := db.logFile.Close() //close the file

	if syncErr != nil {
		return fmt.Errorf("sync database log on close : %w", syncErr)
	}

	if unlockErr != nil {
		return fmt.Errorf("unlock database log : %w", unlockErr)
	}

	if closeErr != nil {
		return fmt.Errorf("close database log : %w", closeErr)
	}

	return nil
}
