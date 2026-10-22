----------BITCASK IMPLEMENTATION----------------------

-Bitcask uses an append-only log and keeps a keydir in memory.

-> The append only log performs sequential writes instead of random writes, and 
the data written is stored on disk,thus suriving restarts.

-> The keydir in memory is a hash index which maps each key to the location of its latest record in the log.

-> KeyDir maps every key to a file, an offset and a timestamp. That is, It stores the file ID of the datafile storing it, the exact byteoffset withing that file where the record begins, and a timestamp.


-A datafile is active, then immutable, then merged away.

-> When we perform PUT() operation, the datafile is active, but when the size of the data file reaches past its limit, it becomes immutable and thus a new log file is created for further append and write operations.

-> These immutable files then go through compaction, which are thus merged.

-> Merge process iterates over all immutable files and collects only the live and the latest versions of each present key into the new merged datafiles. Its entry in keydir undergoes an atomic updation, freeing previously sprawling datafiles.

-Overwriting a key does not modify an old record.

-> Instead of searching and editing the exisiting data in place, Bitcaask just writes the new updates/data to the end of the current active log file.

-Deletions in bitcask is done through tombstones.

-> To delete a record, a special marker called a tombstone is added to the active data file and the key from keydir is removed, the old entry on the disk is neither modified nor deleted. 

-> GET(Key) for a deleted key returns an error.

-Hint Files make bootup fast and bounded.

-> If bitcask crashes or we restart it, it must read all the datafiles and build a new keydir to complete the bootup process. Scanningg the entire gigabytes of merged files would take a long time. So instead, the compaction process creates a very small hint file for every datafile which contains the key and the metadata(tsz, vsz, offset etc) , Thus making restart easier. 
