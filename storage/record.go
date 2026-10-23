package storage

import (
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

var ErrTruncated = errors.New("truncated record")

var ErrCorrupt = errors.New("corrupt record")

var crcTable = crc32.MakeTable(crc32.Castagnoli)

type recordType uint8

const (
	recordTypePut    recordType = 1
	recordTypeDelete recordType = 2
)

type record struct {
	timestamp uint64
	typ       recordType
	key       []byte
	value     []byte
}

func (rec record) encodedSize() int64 {
	return int64(recordHeaderSize + len(rec.key) + len(rec.value))
}

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
	if valueLen > maxKeySize {
		return fmt.Errorf(
			"value length %d exceeds maximum %d",
			valueLen,
			maxKeySize,
		)
	}
	if typ == recordTypeDelete && valueLen != 0 {
		return errors.New("delete record must have an empty value")
	}
	return nil
}
