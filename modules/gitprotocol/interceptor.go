// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

// Package gitprotocol implements a streaming interceptor for the git Smart HTTP
// receive-pack protocol. It parses the incoming packfile before forwarding it to
// git, allowing callers to inspect or reject individual objects synchronously.
//
// When a MAX_PUSH_BLOB_SIZE limit is active, any delta object (ObjOfsDelta or
// ObjRefDelta) causes immediate rejection because the parser only sees the
// compressed delta instruction size, not the final expanded object size.
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
// Contract for callers:
//   - If ReceivePack returns nil, it has either written a complete error
//     response to w (rejection or early failure) or successfully handed off
//     to runGit.
//   - If ReceivePack returns a non-nil error, it is a setup failure before
//     any response bytes were written (e.g. temp file creation). The caller
//     should log it and the client will see an incomplete/failed response.
//
// On rejection (fn returns error): drains r, writes a valid receive-pack error
// response to w using the collected ref updates, and returns nil.
//
// On early protocol/setup errors after we have seen the first pkt-line:
// we make a best-effort attempt to write a proper "unpack error" response
// before returning nil, so the git client sees a clean rejection instead of
// a 200 with truncated body.
//
// On success: seeks the buffered request back to the start and calls runGit.
// The error from runGit (if any) is returned to the caller.
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
		// Protocol error very early — best effort response with no ref list.
		// Use sideband=false (conservative; client will still usually understand
		// a bare "unpack error ..." pkt-line sequence).
		_ = writeErrorResponse(w, nil, "protocol error reading ref updates: "+err.Error(), false)
		_, _ = io.Copy(io.Discard, r)
		return nil
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
		// We have the ref list; give the client a proper error instead of
		// letting the HTTP handler just log and return a broken response.
		_ = writeErrorResponse(w, refUpdates, "internal error buffering push data: "+err.Error(), useSideband)
		return nil
	}
	return runGit(tmp, w)
}

// readRefUpdates reads the pkt-line ref-update commands (and optional push-options
// section) until the PACK data begins.
//
// The first pkt-line carries NUL-separated capability tokens after the first ref
// update. We extract "side-band-64k"/"side-band" (for error response framing) and
// "push-options" (so we know to consume the extra section after the first flush).
//
// When push-options was negotiated, the client sends after the first flush pkt:
//   one or more "push-option <value>" pkt-lines
//   a second flush pkt
// Then the raw PACK data follows.
//
// We consume (and ignore) the push-option lines so that StreamPackfile sees
// the "PACK" magic as the next bytes.
func readRefUpdates(r io.Reader) ([]RefUpdate, bool, error) {
	var updates []RefUpdate
	useSideband := false
	hasPushOptions := false
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
					switch cap {
					case "side-band-64k", "side-band":
						useSideband = true
					case "push-options":
						hasPushOptions = true
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

	// If the client negotiated push-options, there is a second section of
	// "push-option ..." lines terminated by another flush before the PACK data.
	if hasPushOptions {
		for {
			_, isFlush, err := ReadPktLine(r)
			if err != nil {
				return nil, false, err
			}
			if isFlush {
				break
			}
			// discard the push-option value; we don't need it for size limiting
		}
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
			// Each status line is sent as a sideband-1 pkt-line:
			//   PKT-LINE( \x01 <inner-pkt-line-of-status> )
			inner := fmt.Sprintf("%04x", len(line)+4) + string(line)
			if err := WritePktLine(w, append([]byte{0x01}, inner...)); err != nil {
				return err
			}
		}
		// The final flush (0000) is sent bare, not sideband-wrapped.
		// This matches observed behavior of git receive-pack when emitting
		// a report-status error over side-band-64k.
	} else {
		for _, line := range lines {
			if err := WritePktLine(w, line); err != nil {
				return err
			}
		}
	}
	return WriteFlushPkt(w)
}
