package storage

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
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

// decodeRecord rebuilds the in-memory keyDir by reconstructing the records from bytes
func decodeRecord(r io.Reader, remaining int64) (record, error) {

	//check how many bytes remain
	if remaining < 0 {
		return record{}, fmt.Errorf(
			"negative remaining byte count : %d",
			remaining,
		)
	}

	if remaining == 0 {
		return record{}, io.EOF
	}

	if remaining < int64(recordHeaderSize) {
		return record{}, fmt.Errorf(
			"%w : header has %d bytes; need %d",
			ErrTruncated,
			remaining,
			recordHeaderSize,
		)
	}

	//read the fixed size header
	var header [recordHeaderSize]byte

	if _, err := io.ReadFull(r, header[:]); err != nil {
		if errors.Is(err, io.EOF) ||
			errors.Is(err, io.ErrUnexpectedEOF) {
			return record{}, fmt.Errorf(
				"%w: reading header: %v",
				ErrTruncated,
				err,
			)
		}
		return record{}, fmt.Errorf("read record header: %w", err)
	}

	//extract the checksum
	storedChecksum := binary.BigEndian.Uint32(
		header[:checksumSize],
	)

	//extract the timestamp, type and lengths
	timestampStart := checksumSize
	timestampEnd := timestampStart + timestampSize

	timestamp := binary.BigEndian.Uint64(
		header[timestampStart:timestampEnd],
	)

	typeOffset := timestampEnd
	typ := recordType(header[typeOffset])

	keyLenOffset := typeOffset + recordTypeSize
	valueLenOffset := keyLenOffset + keyLengthSize

	keyLen := uint64(binary.BigEndian.Uint32(
		header[keyLenOffset : keyLenOffset+keyLengthSize],
	))

	valueLen := uint64(binary.BigEndian.Uint32(
		header[valueLenOffset : valueLenOffset+valueLengthSize],
	))

	//validate the metadata
	if err := validateRecordMetadata(
		typ,
		keyLen,
		valueLen,
	); err != nil {
		return record{}, fmt.Errorf(
			"%w: invalid metadata: %v",
			ErrCorrupt,
			err,
		)
	}

	//check that the complete record fits
	encodedLen := int64(recordHeaderSize) + int64(keyLen) + int64(valueLen)

	if encodedLen > remaining {
		return record{}, fmt.Errorf(
			"%w: record requires %d bytes; only %d remain",
			ErrTruncated,
			encodedLen,
			remaining,
		)
	}

	//allocate a payload buffer and reconstruct the payload
	payloadLen := recordMetadataSize + int(keyLen) + int(valueLen)

	payload := make([]byte, payloadLen)

	copy(payload, header[checksumSize:])

	//read the remaining bytes
	if _, err := io.ReadFull(
		r,
		payload[recordMetadataSize:],
	); err != nil {
		if errors.Is(err, io.EOF) ||
			errors.Is(err, io.ErrUnexpectedEOF) {
			return record{}, fmt.Errorf(
				"%w: reading payload: %v",
				ErrTruncated,
				err,
			)
		}
		return record{}, fmt.Errorf("read record payload: %w", err)
	}

	//verify the checksum
	actualChecksum := crc32.Checksum(payload, crcTable)

	if actualChecksum != storedChecksum {
		return record{}, fmt.Errorf(
			"%w: checksum mismatch: stored %08x, calculated %08x",
			ErrCorrupt,
			storedChecksum,
			actualChecksum,
		)
	}

	//extract the key and value
	keyStart := recordMetadataSize
	keyEnd := keyStart + int(keyLen)
	valueEnd := keyEnd + int(valueLen)

	rec := record{
		timestamp: timestamp,
		typ:       typ,

		key:   payload[keyStart:keyEnd:keyEnd],
		value: payload[keyEnd:valueEnd:valueEnd],
	}

	return rec, nil
}
