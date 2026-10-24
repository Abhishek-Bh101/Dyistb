
# DyistDb Storage Specification

## 1. Purpose

Build a persistent, single-node key-value storage engine
in Go. This will later serve as the local storage layer
for a Dynamo-inspired distributed database.

## 2. API

- Open(dir): open or create a database.
- Put(key, value): insert or overwrite a value.
- Get(key): retrieve the current value.
- Delete(key): logically delete a key.
- Close(): close the database safely.

Keys and values are opaque byte sequences.
The engine does not interpret the contents of values.

## 3. Storage design

- Store records in an append-only log.
- Maintain an in-memory keydir pointing to the latest
  record for each key.
- Use CRC32 to detect accidental record corruption.
- Use tombstones to represent deletions.
- Rotate the active file when it reaches a size limit.
- Keep older files immutable.

## 4. Durability

A successful Put or Delete means the complete record
has been written and the file has been synced using
File.Sync().

## 5. Recovery

Rebuild the keydir when opening the database.

- Process records in the correct order.
- Apply overwrites and deletions.
- Truncate an incomplete trailing record to the last
  valid record boundary.
- Report checksum failures in complete records as
  corruption rather than silently discarding data.

## 6. Merging and hint files

- Merge eligible immutable files.
- Preserve the correct latest value for each live key.
- Prevent deleted or overwritten values from reappearing.
- Generate hint files containing key and record-location
  metadata to accelerate index reconstruction.
- Retain the original files until merged data and
  associated metadata have been safely installed.
- Recover safely from an interrupted merge.

## 7. Concurrency and limitations

- Protect in-process operations using synchronization.
- Initially permit only one process to open a database
  directory at a time.
- Do not support range queries.
- Compaction is synchronous initially; normal operations
  may be paused while a merge runs.

## 8. Record format and codec

### Record layout

Each record consists of a fixed-size header followed by a variable-length key and value.

```text
[ CRC-32C: 4 bytes ]
[ Timestamp: 8 bytes ]
[ Record type: 1 byte ]
[ Key length: 4 bytes ]
[ Value length: 4 bytes ]
[ Key bytes ]
[ Value bytes ]
```

The fixed header is 21 bytes, including the checksum. The metadata following the checksum occupies 17 bytes.

The total encoded record size is:

`record_size = 21 + key_length + value_length`

All integer fields use big-endian byte order. Lengths are measured in bytes.

### Record types

- PUT: type value `1`. Stores a key-value pair. Empty values are permitted.
- DELETE: type value `2`. Represents a deletion and must have an empty value.

Keys must not be empty. Unknown record types are invalid.

A physical DELETE record is distinct from a distributed logical tombstone, which must preserve version information for conflict resolution in the distributed layer.

### Timestamp

Each record contains an unsigned 64-bit timestamp representing Unix time in milliseconds.

The codec stores and retrieves the supplied timestamp but does not generate it. Storage operations will assign timestamps when creating records. Compaction must preserve the timestamps of retained records.

Wall-clock timestamps alone are not sufficient to establish causality between concurrent distributed writes.

### Size limits

- Maximum key size: 1 MiB (1,048,576 bytes).
- Maximum value size: 64 MiB (67,108,864 bytes).

The encoder and decoder must enforce these limits. The decoder must validate the record type and declared lengths before allocating memory based on those lengths.

The decoder must also check that the complete record fits within the number of bytes remaining in the source.

### Checksum

The checksum uses CRC-32C (Castagnoli) and occupies the first four bytes of the record.

It covers every byte after the checksum field, including the timestamp, record type, lengths, key and value. The checksum field itself is excluded.

A checksum mismatch indicates corruption. CRC-32C detects accidental corruption but is not a cryptographic integrity mechanism.

### Codec behaviour and errors

- `encodeRecord` converts a valid logical record into its binary representation.
- `decodeRecord` reads and validates exactly one record from an `io.Reader`.
- The codec does not perform file writes, sync data to disk, maintain the keydir or implement recovery.

The decoder distinguishes the following outcomes:

- `io.EOF`: No bytes remain at the expected record boundary.
- `ErrTruncated`: The record is incomplete or its declared size exceeds the reported remaining bytes.
- `ErrCorrupt`: The record metadata is invalid or the checksum does not match.
- Other I/O errors: The underlying reader encountered another error.

Callers can use `errors.Is` to check for the sentinel errors.

During recovery, an incomplete trailing record may be truncated to the last valid record boundary according to the recovery policy. A checksum failure in a complete record must be reported as corruption rather than silently discarded.

An invalid length near the end of a file can resemble an interrupted write. Error classification alone cannot always establish the cause, so recovery must consider the record's position and available evidence.

### Record locations

The codec can calculate the encoded size of a record. The storage engine will track file IDs and record offsets in the keydir.

The value offset is:

`value_offset = record_offset + 21 + key_length`

Reading only a value by offset does not independently verify the checksum covering the entire record.

### File format versioning

A file-level magic number and format version are planned for the log-file implementation. These are separate from the 21-byte record header and will be specified when implemented.