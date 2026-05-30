// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package gitprotocol

import (
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"io"
)

// Pack object type constants from the git pack-format spec.
const (
	ObjCommit   = 1
	ObjTree     = 2
	ObjBlob     = 3
	ObjTag      = 4
	ObjOfsDelta = 6 // delta relative to an object at a negative offset in the same pack
	ObjRefDelta = 7 // delta relative to an object named by its SHA-1
)

// ObjectHeader holds the type and uncompressed size parsed from a packfile object header.
// For delta objects (ObjOfsDelta, ObjRefDelta), UnpackedSize is the size of the delta
// instructions, not the final reconstructed object size.
type ObjectHeader struct {
	Type         int
	UnpackedSize int64
	RefDeltaBase [20]byte // only populated for ObjRefDelta
}

// StreamPackfile reads a git packfile from r, invoking cb before consuming each object's
// compressed payload. Streaming stops and the error is returned if cb returns non-nil.
// The caller must ensure r is positioned at the start of a valid PACK stream.
func StreamPackfile(r io.Reader, cb func(ObjectHeader) error) error {
	magic := make([]byte, 4)
	if _, err := io.ReadFull(r, magic); err != nil {
		return fmt.Errorf("pack: read signature: %w", err)
	}
	if string(magic) != "PACK" {
		return fmt.Errorf("pack: invalid signature %q", magic)
	}

	var version uint32
	if err := binary.Read(r, binary.BigEndian, &version); err != nil {
		return fmt.Errorf("pack: read version: %w", err)
	}
	if version != 2 && version != 3 {
		return fmt.Errorf("pack: unsupported version %d", version)
	}

	var count uint32
	if err := binary.Read(r, binary.BigEndian, &count); err != nil {
		return fmt.Errorf("pack: read count: %w", err)
	}

	for i := uint32(0); i < count; i++ {
		hdr, err := readObjectHeader(r)
		if err != nil {
			return fmt.Errorf("pack: object %d header: %w", i, err)
		}
		if err := cb(hdr); err != nil {
			return err
		}
		if err := consumeObjectData(r); err != nil {
			return fmt.Errorf("pack: object %d data: %w", i, err)
		}
	}

	// Consume the trailing SHA-1 checksum so r is fully drained.
	trailing := make([]byte, 20)
	_, _ = io.ReadFull(r, trailing)
	return nil
}

// readObjectHeader reads the variable-length type+size encoding for one pack object
// and, for delta types, any additional base reference bytes.
func readObjectHeader(r io.Reader) (ObjectHeader, error) {
	var h ObjectHeader
	var buf [1]byte

	if _, err := io.ReadFull(r, buf[:]); err != nil {
		return h, err
	}
	b := buf[0]
	h.Type = int((b >> 4) & 0x7)
	size := int64(b & 0xf)
	shift := uint(4)

	for b&0x80 != 0 {
		if _, err := io.ReadFull(r, buf[:]); err != nil {
			return h, err
		}
		b = buf[0]
		size |= int64(b&0x7f) << shift
		shift += 7
	}
	h.UnpackedSize = size

	// OFS_DELTA: consume the variable-length negative offset (value not needed).
	if h.Type == ObjOfsDelta {
		if _, err := io.ReadFull(r, buf[:]); err != nil {
			return h, err
		}
		for buf[0]&0x80 != 0 {
			if _, err := io.ReadFull(r, buf[:]); err != nil {
				return h, err
			}
		}
	}

	// REF_DELTA: read the 20-byte base object SHA-1.
	if h.Type == ObjRefDelta {
		if _, err := io.ReadFull(r, h.RefDeltaBase[:]); err != nil {
			return h, err
		}
	}

	return h, nil
}

// consumeObjectData decompresses and discards the zlib-compressed payload for one object,
// advancing r to the start of the next object (or end of pack).
func consumeObjectData(r io.Reader) error {
	zr, err := zlib.NewReader(r)
	if err != nil {
		return err
	}
	if _, err := io.Copy(io.Discard, zr); err != nil {
		return err
	}
	return zr.Close()
}
