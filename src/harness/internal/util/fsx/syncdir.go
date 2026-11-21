package fsx

// SyncDir makes the directory entries of dir durable after a file was created,
// linked or renamed into it: an fsync of the directory on POSIX. On Windows it is
// a no-op, because Windows cannot flush a directory handle and NTFS journals the
// entry with the file's metadata, which a Sync of the file flushes.
func SyncDir(dir string) error { return syncDir(dir) }
