
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

