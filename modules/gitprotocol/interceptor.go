// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

// Package gitprotocol implements a streaming interceptor for the git Smart HTTP
// receive-pack protocol. It parses the incoming packfile before forwarding it to
// git, allowing callers to inspect or reject individual objects synchronously.
package gitprotocol

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// RefUpdate represents one ref being created, updated, or deleted in a push.
type RefUpdate struct {
	OldOID string
	NewOID string
	Ref    string
}

// InterceptFunc is called for each object header in the incoming packfile.
// Returning a non-nil error causes the entire push to be rejected; the error
// message is forwarded to the git client.
type InterceptFunc func(ObjectHeader) error

// ReceivePack intercepts a git-receive-pack HTTP request body, parsing the
// packfile and calling fn for each object header before forwarding to git.
//
// On rejection (fn returns error): drains r, writes a valid receive-pack error
// response to w, and returns nil (the rejection itself is not an error).
//
// On success: seeks the buffered request back to the start and calls runGit,
// passing the buffered reader and w. Any error from runGit is returned directly.
//
// On a pack parse failure: falls through to runGit rather than silently dropping
// the push, so a parser bug does not permanently block legitimate pushes.
func ReceivePack(r io.Reader, w io.Writer, fn InterceptFunc, runGit func(io.Reader, io.Writer) error) error {
	tmp, err := os.CreateTemp("", "gitea-pack-intercept-*")
	if err != nil {
		return fmt.Errorf("interceptor: create temp file: %w", err)
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()

	tee := io.TeeReader(r, tmp)

	refUpdates, useSideband, err := readRefUpdates(tee)
	if err != nil {
		return fmt.Errorf("interceptor: read ref updates: %w", err)
	}

	var rejectErr error
	parseErr := StreamPackfile(tee, func(hdr ObjectHeader) error {
		if err := fn(hdr); err != nil {
			rejectErr = err
			return err
		}
		return nil
	})

	if rejectErr != nil {
		_, _ = io.Copy(io.Discard, r)
		return writeErrorResponse(w, refUpdates, rejectErr.Error(), useSideband)
	}

	// Drain any bytes that remain after the last object + trailing checksum.
	_, _ = io.Copy(tmp, tee)

	if parseErr != nil {
		// Forward to git unchanged; let git produce its own error for malformed packs.
	}

	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("interceptor: seek temp file: %w", err)
	}
	return runGit(tmp, w)
}

// readRefUpdates reads pkt-line ref-update commands until a flush packet.
// The first line carries NUL-separated capability tokens; the presence of
// "side-band-64k" or "side-band" determines the response framing.
func readRefUpdates(r io.Reader) ([]RefUpdate, bool, error) {
	var updates []RefUpdate
	useSideband := false
	first := true

	for {
		data, isFlush, err := ReadPktLine(r)
		if err != nil {
			return nil, false, err
		}
		if isFlush {
			break
		}

		line := strings.TrimSuffix(string(data), "\n")
		if first {
			first = false
			if nul := strings.IndexByte(line, 0); nul >= 0 {
				caps := line[nul+1:]
				line = line[:nul]
				for _, cap := range strings.Fields(caps) {
					if cap == "side-band-64k" || cap == "side-band" {
						useSideband = true
					}
				}
			}
		}

		fields := strings.SplitN(line, " ", 3)
		if len(fields) != 3 {
			return nil, false, fmt.Errorf("malformed ref-update line: %q", line)
		}
		updates = append(updates, RefUpdate{OldOID: fields[0], NewOID: fields[1], Ref: fields[2]})
	}
	return updates, useSideband, nil
}

// writeErrorResponse writes a receive-pack unpack-status error to w.
//
// When useSideband is true each inner report-status pkt-line is wrapped in a
// sideband band-1 packet, exactly as git receive-pack does when side-band-64k
// was negotiated: PKT-LINE(\x01 <inner-pkt-line>).
func writeErrorResponse(w io.Writer, refs []RefUpdate, msg string, useSideband bool) error {
	lines := make([][]byte, 0, len(refs)+1)
	lines = append(lines, []byte("unpack error "+msg+"\n"))
	for _, r := range refs {
		lines = append(lines, []byte("ng "+r.Ref+" "+msg+"\n"))
	}

	if useSideband {
		for _, line := range lines {
			// Encode as inner pkt-line, then prepend the band-1 byte.
			inner := fmt.Sprintf("%04x", len(line)+4) + string(line)
			if err := WritePktLine(w, append([]byte{0x01}, inner...)); err != nil {
				return err
			}
		}
		// Sideband-wrapped inner flush: PKT-LINE(\x01 0000)
		if err := WritePktLine(w, []byte("\x010000")); err != nil {
			return err
		}
	} else {
		for _, line := range lines {
			if err := WritePktLine(w, line); err != nil {
				return err
			}
		}
	}
	return WriteFlushPkt(w)
}
