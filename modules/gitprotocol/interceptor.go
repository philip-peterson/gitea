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
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// ErrPushRejectedByInterceptor is returned by ReceivePack when the InterceptFunc
// rejected the push. In this case a well-formed git receive-pack error response
// has already been written to the client writer.
var ErrPushRejectedByInterceptor = errors.New("push rejected by interceptor")

// InterceptFunc is called for each object header in the incoming packfile.
// Returning a non-nil error causes the entire push to be rejected; the error
// message is forwarded to the git client.
type InterceptFunc func(ObjectHeader) error

// ReceivePack intercepts a git-receive-pack HTTP request body, parsing the
// packfile and calling fn for each object header before forwarding to git.
//
// Behavior:
//   - On success: seeks the buffered data and calls runGit. Returns whatever
//     runGit returns (usually nil or a git-level error).
//   - On InterceptFunc rejection: writes a well-formed receive-pack error
//     response to w and returns a wrapped ErrPushRejectedByInterceptor.
//   - On early protocol or internal errors: best-effort error response is
//     written when possible; a non-wrapped error may be returned for truly
//     low-level failures (temp file creation, etc.).
//
// This design lets callers distinguish policy rejections (for logging/metrics)
// from other outcomes.
func ReceivePack(r io.Reader, w io.Writer, fn InterceptFunc, runGit func(io.Reader, io.Writer) error) error {
	tmp, err := os.CreateTemp("", "gitea-pack-intercept-*")
	if err != nil {
		return fmt.Errorf("interceptor: create temp file: %w", err)
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()

	tee := io.TeeReader(r, tmp)

	useSideband, err := readReceivePackHeader(tee)
	if err != nil {
		// Best-effort error response for early protocol garbage.
		_ = writeErrorResponse(w, "protocol error: "+err.Error(), false)
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
		_ = writeErrorResponse(w, rejectErr.Error(), useSideband)
		return fmt.Errorf("%w: %v", ErrPushRejectedByInterceptor, rejectErr)
	}

	// Drain any bytes that remain after the last object + trailing checksum.
	_, _ = io.Copy(tmp, tee)

	if parseErr != nil {
		// Forward to git unchanged; let git produce its own error for malformed packs.
	}

	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		_ = writeErrorResponse(w, "internal error buffering push data: "+err.Error(), useSideband)
		return nil
	}
	return runGit(tmp, w)
}

// readReceivePackHeader reads the initial pkt-lines of a receive-pack request
// (ref updates + optional push-options section) until the PACK data begins.
// It returns whether side-band-64k was negotiated (needed only for error responses).
//
// When push-options was negotiated, the client sends after the first flush:
//   zero or more "push-option ..." pkt-lines
//   another flush
// We simply consume this section so StreamPackfile sees the real PACK magic.
func readReceivePackHeader(r io.Reader) (useSideband bool, err error) {
	hasPushOptions := false
	first := true

	for {
		data, isFlush, err := ReadPktLine(r)
		if err != nil {
			return false, err
		}
		if isFlush {
			break
		}

		line := strings.TrimSuffix(string(data), "\n")
		if first {
			first = false
			if nul := strings.IndexByte(line, 0); nul >= 0 {
				for _, cap := range strings.Fields(line[nul+1:]) {
					switch cap {
					case "side-band-64k", "side-band":
						useSideband = true
					case "push-options":
						hasPushOptions = true
					}
				}
			}
		}
		// We no longer collect RefUpdate lines; a simple "unpack error" is
		// sufficient for the size-limit use case.
	}

	if hasPushOptions {
		// Consume the push-options section (terminated by another flush).
		for {
			_, isFlush, err := ReadPktLine(r)
			if err != nil {
				return false, err
			}
			if isFlush {
				break
			}
		}
	}
	return useSideband, nil
}

// writeErrorResponse writes a minimal but valid receive-pack error response.
//
// We deliberately emit only the "unpack error" line. Emitting per-ref "ng"
// lines added significant complexity and was the source of several bugs for
// very little practical benefit when the goal is simply "reject big blobs".
func writeErrorResponse(w io.Writer, msg string, useSideband bool) error {
	line := []byte("unpack error " + msg + "\n")

	if useSideband {
		// PKT-LINE( \x01 <inner-pkt-line> )
		inner := fmt.Sprintf("%04x", len(line)+4) + string(line)
		if err := WritePktLine(w, append([]byte{0x01}, inner...)); err != nil {
			return err
		}
	} else {
		if err := WritePktLine(w, line); err != nil {
			return err
		}
	}
	return WriteFlushPkt(w)
}
