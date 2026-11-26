// Package instructions is DH-D-CONTEXT-2 (component C41, context-compaction-skill.md
// sec. 6.2 and DH-ADR-CONTEXT-2): the agent follows the workspace's instruction
// files, and notices when they change.
//
// Discover finds AGENTS.md and its kin (Config.Candidates, then
// Config.LocalCandidates) in every directory from the project root down to the
// session's working directory. Every file is found and read through the
// filesystem seam (world/fs, C32), bounded by Config.MaxSourceBytes per file and
// Config.MaxTotalSourceBytes in all. The fence applies: a candidate whose resolved
// target leaves the project root is not read and is recorded as refused with
// fs/sandbox-denied. Each file read is digested; files with the same digest are
// kept once, from the first in order.
//
// Render is pure: it frames the files in chain order (a later file is more
// specific and says so), escapes any frame tag inside a file, and applies the
// byte budget Config.MaxBytes by dropping the broadest files first and truncating
// the most specific one last, with a marker. Its Visible record is the source data
// of the message the Contributor returns, and FoldView folds it from the log as
// instructions.visible, so a replay reads no file.
//
// A Reconciler holds one session's chain: watch notices (util/watch), file-tool
// touches (Touch, from fs.Observed), a working-directory change (SetDirectory)
// and a resume (Resume) each mark it to be discovered again at the next step. An
// unreadable file keeps its own last-good text.
package instructions
