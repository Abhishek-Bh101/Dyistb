package storage

import (
	"bytes"
	"errors"
	"fmt"
	"io"
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

	ErrNotFound = errors.New("key not found")
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

	keydir, writeOffset, err := recoverLog(file, info.Size())

	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("recover database log: %w", err)
	}

	db := &DB{
		dir:         dir,
		logFile:     file,
		keydir:      keydir,
		writeOffset: writeOffset,
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

func (db *DB) Put(key, value []byte) error {

	//exclusive lock
	db.mu.Lock()

	defer db.mu.Unlock()

	if db.closed {
		return ErrClosed
	}

	if db.failed != nil {
		return fmt.Errorf("database is in failed state : %w", db.failed)
	}

	rec := record{
		timestamp: uint64(db.now().UnixMilli()),
		typ:       recordTypePut,
		key:       key,
		value:     value,
	}

	//encode the record for writing
	encoded, err := encodeRecord(rec)
	if err != nil {
		return fmt.Errorf("encode put record : %w", err)
	}

	offset := db.writeOffset

	//write the bytes at the offset
	n, err := db.logFile.WriteAt(encoded, offset)

	if err == nil && n != len(encoded) {
		err = io.ErrShortWrite
	}

	if err != nil {
		db.failed = fmt.Errorf("append put record: %w", err)
		return db.failed
	}

	if err := db.logFile.Sync(); err != nil {
		db.failed = fmt.Errorf("sync put record: %w", err)
		return db.failed
	}

	//update keydir
	db.keydir[string(key)] = indexEntry{
		recordOffset: offset,
		recordSize:   int64(len(encoded)),
		timestamp:    rec.timestamp,
	}

	//move writeoffset forward
	db.writeOffset += int64(len(encoded))

	return nil
}
func (db *DB) Get(key []byte) ([]byte, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	if db.closed {
		return nil, ErrClosed
	}

	if db.failed != nil {
		return nil, fmt.Errorf("database is in failed state : %w", db.failed)
	}

	entry, ok := db.keydir[string(key)]

	if !ok {
		return nil, ErrNotFound
	}

	reader := io.NewSectionReader(db.logFile, entry.recordOffset, entry.recordSize)

	rec, err := decodeRecord(reader, entry.recordSize)

	if err != nil {
		return nil, fmt.Errorf("read record: %w", err)
	}

	if rec.encodedSize() != entry.recordSize {
		return nil, fmt.Errorf("%w: indexed record mismatch", ErrCorrupt)
	}

	if rec.typ != recordTypePut || !bytes.Equal(rec.key, key) {
		return nil, fmt.Errorf("%w: keydir points to an unexpected record", ErrCorrupt)
	}

	return rec.value, nil
}

// Delete appends the new delete record to the log and deletes the key from keydir
func (db *DB) Delete(key []byte) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	if db.closed {
		return ErrClosed
	}

	if db.failed != nil {
		return fmt.Errorf("database is in failed state: %w", db.failed)
	}

	rec := record{
		timestamp: uint64(db.now().UnixMilli()),
		typ:       recordTypeDelete,
		key:       key,
		value:     nil,
	}

	encoded, err := encodeRecord(rec)

	if err != nil {
		return fmt.Errorf("encode delete record: %w", err)
	}

	offset := db.writeOffset

	n, err := db.logFile.WriteAt(encoded, offset)

	if err == nil && n != len(encoded) {
		err = io.ErrShortWrite
	}

	if err != nil {
		db.failed = fmt.Errorf("append delete record: %w", err)
		return db.failed
	}

	if err := db.logFile.Sync(); err != nil {
		db.failed = fmt.Errorf("sync delete record: %w", err)
		return db.failed
	}

	//delete the key from keydir
	delete(db.keydir, string(key))

	db.writeOffset += int64(len(encoded))

	return nil
}

func recoverLog(file *os.File, fileSize int64) (map[string]indexEntry, int64, error) {
	keydir := make(map[string]indexEntry) //make an empty map

	var offset int64

	for offset < fileSize {

		remaining := fileSize - offset
		readfile := io.NewSectionReader(file, offset, remaining)

		rec, err := decodeRecord(readfile, remaining)

		if err != nil {
			if !errors.Is(err, ErrTruncated) {
				return nil, offset, fmt.Errorf("read record: %w", err)
			}

			if err := file.Truncate(offset); err != nil {
				return nil, 0, fmt.Errorf("truncate incomplete record: %w", err)
			}

			if err := file.Sync(); err != nil {
				return nil, 0, fmt.Errorf("sync recovered log: %w", err)
			}
			return keydir, offset, nil

		}

		recordSize := rec.encodedSize()
		switch rec.typ {
		case recordTypePut:
			keydir[string(rec.key)] = indexEntry{
				recordOffset: offset,
				recordSize:   recordSize,
				timestamp:    rec.timestamp,
			}

		case recordTypeDelete:
			delete(keydir, string(rec.key))

		default:
			return nil, 0, fmt.Errorf("%w: unknown record type at offset %d", ErrCorrupt, offset)
		}

		offset += recordSize
	}

	return keydir, offset, nil

}
