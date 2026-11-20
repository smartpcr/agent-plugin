// Package fsx is the one place where the harness replaces, creates, locks and
// identifies files (DH-D-FS-1, DH-ADR-NODEFS-1). Every other file operation calls the
// standard library directly; publishing durable state with os.Rename outside fsx is a
// review finding.
//
// What each operation does, per platform:
//
//   - Replace stages a file beside the target (exclusive create, owner-only), runs
//     the caller's writer, sets the final mode, syncs when Durable, closes and renames
//     it over the target. A reader sees the complete old or the complete new content,
//     never a mix. On Windows the rename is MoveFileEx (with MOVEFILE_WRITE_THROUGH
//     when Durable) and is retried for a bounded time while another program holds the
//     target open; on POSIX it is rename(2) followed, when Durable, by a sync of the
//     directory.
//   - CreateIfAbsent publishes the staged file only if no file has the name: Linux
//     renameat2(RENAME_NOREPLACE), Windows MoveFileEx without the replace flag,
//     elsewhere a hard link followed by removing the staged name.
//   - ReplaceIn and CreateIfAbsentIn do the same by name inside an os.Root. The
//     rooted create-if-absent links the staged file to the name (Root.Link); where
//     linking is refused it reserves the name with an exclusive create and renames
//     the staged file over the reservation. When Durable, POSIX syncs the
//     directory through the root; Windows, whose rooted rename cannot write
//     through, opens the published name through the root and flushes it, and
//     reports ErrDurabilityUnknown when it cannot (a read-only result).
//   - Lock and TryLock take an operating-system lock on a lock file (flock,
//     LockFileEx); the kernel releases it when the holder dies.
//   - ID names a file by device and inode (POSIX) or volume serial and file index
//     (Windows); Stat adds size and times for a version token; Digest hashes content.
//   - Canonical and Inside answer "which file is this" and "is it under that
//     directory" by components, never by string prefix.
//   - IsText, the line-ending helpers, Decode and Encode give one verdict on text for
//     every reader and editor.
//   - IsRemote is the filesystem-type probe the watch service uses to choose polling.
//
// A staged file is named with StagePrefix, so a leftover from a killed process is
// recognisable and SweepStaged removes it by name and age.
package fsx
