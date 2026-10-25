// Package claude reads the files Claude Code loads (skills, command files,
// hook configuration and plugin manifests) into plain facts, and turns a tree
// listing into the list of paths worth reading. It does no I/O and judges
// nothing: the caller supplies file text, and whether what it says is good
// enough is internal/rules' decision.
//
// An unreadable file never yields partial facts. A check that read half a file
// could pass on the half it missed, so a file that is not in the shape Claude
// Code loads comes back with Unreadable set and everything else zero.
//
// Every list or limit that comes from Claude Code's documentation cites its
// page where it is defined; the layout rules follow
// https://code.claude.com/docs/en/plugins-reference (standard layout and the
// way each manifest key replaces, adds to or merges with its default),
// https://code.claude.com/docs/en/skills and
// https://code.claude.com/docs/en/hooks.
package claude
