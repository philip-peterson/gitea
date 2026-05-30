// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package gitprotocol

import (
	"fmt"
	"io"
	"strconv"
)

// ReadPktLine reads one pkt-line from r.
// Returns (data, false, nil) for a data packet or (nil, true, nil) for a flush (0000).
func ReadPktLine(r io.Reader) ([]byte, bool, error) {
	lenBuf := make([]byte, 4)
	if _, err := io.ReadFull(r, lenBuf); err != nil {
		return nil, false, fmt.Errorf("pkt-line: read length: %w", err)
	}
	length, err := strconv.ParseUint(string(lenBuf), 16, 32)
	if err != nil {
		return nil, false, fmt.Errorf("pkt-line: parse length %q: %w", lenBuf, err)
	}
	if length == 0 {
		return nil, true, nil // flush packet
	}
	if length < 4 || length > 65524 {
		return nil, false, fmt.Errorf("pkt-line: invalid length %d", length)
	}
	data := make([]byte, length-4)
	if _, err := io.ReadFull(r, data); err != nil {
		return nil, false, fmt.Errorf("pkt-line: read data: %w", err)
	}
	return data, false, nil
}

// WritePktLine writes data as a pkt-line to w.
func WritePktLine(w io.Writer, data []byte) error {
	header := fmt.Sprintf("%04x", len(data)+4)
	if _, err := io.WriteString(w, header); err != nil {
		return err
	}
	_, err := w.Write(data)
	return err
}

// WriteFlushPkt writes a flush packet (0000) to w.
func WriteFlushPkt(w io.Writer) error {
	_, err := io.WriteString(w, "0000")
	return err
}
