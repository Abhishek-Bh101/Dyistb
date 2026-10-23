package storage

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
)

// [ crc:4 ][ timestamp:8 ][ type:1 ][ keyLen:4 ][ valLen:4 ][ key ][ value ]
const (
	checksumSize       = 4
	timestampSize      = 8
	recordTypeSize     = 1
	keyLengthSize      = 4
	valueLengthSize    = 4
	recordMetadataSize = timestampSize + recordTypeSize + keyLengthSize + valueLengthSize
	recordHeaderSize   = checksumSize + recordMetadataSize
	maxKeySize         = 1 << 20
	maxValueSize       = 64 << 20
)

// ErrTruncated indicates that a record is incomplete.
var ErrTruncated = errors.New("truncated record")

// ErrCorrupt indicates invalid record metadata or a checksum mismatch.
var ErrCorrupt = errors.New("corrupt record")

// crcTable selects the CRC-32C (Castagnoli) algorithm.
var crcTable = crc32.MakeTable(crc32.Castagnoli)

// recordType identifies the operation a record represents.
type recordType uint8

const (
	recordTypePut    recordType = 1
	recordTypeDelete recordType = 2
)

// record represents one logical operation in the append-only log.
type record struct {
	timestamp uint64
	typ       recordType
	key       []byte
	value     []byte
}

// encodedSize returns the number of bytes the record occupies on disk.
func (rec record) encodedSize() int64 {
	return int64(recordHeaderSize + len(rec.key) + len(rec.value))
}

// validateRecordMetadata validates the type and field lengths.
func validateRecordMetadata(typ recordType, keyLen uint64, valueLen uint64) error {
	switch typ {
	case recordTypePut, recordTypeDelete:
	default:
		return fmt.Errorf("unknown record type : %d", typ)
	}
	if keyLen == 0 {
		return errors.New("key must not be empty")
	}
	if keyLen > maxKeySize {
		return fmt.Errorf(
			"key length %d exceeds maximum %d",
			keyLen,
			maxKeySize,
		)
	}
	if valueLen > maxValueSize {
		return fmt.Errorf(
			"value length %d exceeds maximum %d",
			valueLen,
			maxValueSize,
		)
	}
	if typ == recordTypeDelete && valueLen != 0 {
		return errors.New("delete record must have an empty value")
	}
	return nil
}

// encodeRecord takes a record and converts it into a sequence of bytes that can be stored in the db's log file
func encodeRecord(rec record) ([]byte, error) {
	keyLen := uint64(len(rec.key))
	valueLen := uint64(len(rec.value))

	//validaate the record
	if err := validateRecordMetadata(
		rec.typ,
		keyLen,
		valueLen,
	); err != nil {
		return nil, fmt.Errorf("encode record : %w", err)

	}

	//allocate output buffer
	encodedLen := recordHeaderSize + len(rec.key) + len(rec.value)

	encoded := make([]byte, encodedLen)

	payload := encoded[checksumSize:]

	//encode the metadata
	binary.BigEndian.PutUint64(
		payload[0:timestampSize],
		rec.timestamp,
	)
	payload[timestampSize] = byte(rec.typ)

	keyLenOffset := timestampSize + recordTypeSize
	valueLenOffset := keyLenOffset + keyLengthSize

	binary.BigEndian.PutUint32(
		payload[keyLenOffset:keyLenOffset+keyLengthSize],
		uint32(keyLen),
	)

	binary.BigEndian.PutUint32(
		payload[valueLenOffset:valueLenOffset+valueLengthSize],
		uint32(valueLen),
	)

	//place the key value bytes into their set positions in the buffer
	copy(payload[recordMetadataSize:], rec.key)

	copy(
		payload[recordMetadataSize+len(rec.key):],
		rec.value,
	)

	//calculate crc32
	checksum := crc32.Checksum(payload, crcTable)

	//store the checksum
	binary.BigEndian.PutUint32(
		encoded[:checksumSize],
		checksum,
	)

	return encoded, nil
}
