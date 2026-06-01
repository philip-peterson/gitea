// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package gitprotocol

import (
	"bytes"
	"testing"
)

func TestReadObjectHeader(t *testing.T) {
	tests := []struct {
		name         string
		header       []byte // variable-length type+size (+ delta extra bytes)
		wantType     int
		wantSize     uint64
		wantRefDelta bool
	}{
		{
			name:     "small blob (type 3, size 12 = 0b1100)",
			header:   []byte{0x3c}, // 0011 1100 -> type=3, size low nibble=0xc (12)
			wantType: ObjBlob,
			wantSize: 12,
		},
		{
			name:     "commit size 0",
			header:   []byte{0x10}, // 0001 0000 -> type=1, size=0
			wantType: ObjCommit,
			wantSize: 0,
		},
		{
			name:     "ofs delta (type 6, size 5)",
			header:   []byte{0x65, 0x01}, // 0110 0101 (type6, size5) + ofs-delta continuation byte
			wantType: ObjOfsDelta,
			wantSize: 5,
		},
		{
			name:         "ref delta (type 7, size 10)",
			header:       append([]byte{0x7a}, bytes.Repeat([]byte{0xaa}, 20)...),
			wantType:     ObjRefDelta,
			wantSize:     10,
			wantRefDelta: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := bytes.NewReader(tc.header)
			h, err := readObjectHeader(r)
			if err != nil {
				t.Fatalf("readObjectHeader: %v", err)
			}
			if h.Type != tc.wantType {
				t.Errorf("Type = %d, want %d", h.Type, tc.wantType)
			}
			if h.UnpackedSize != tc.wantSize {
				t.Errorf("UnpackedSize = %d, want %d", h.UnpackedSize, tc.wantSize)
			}
			if tc.wantRefDelta && h.RefDeltaBase == ([20]byte{}) {
				t.Error("expected RefDeltaBase to be populated")
			}
		})
	}
}

func TestReadObjectHeaderUint64(t *testing.T) {
	// Ensure we don't get negative/wrapped values for large sizes.
	// Construct a header that sets bit 63 in the size field.
	// First byte: type=blob (011), low 4 bits of size = 0x0f
	// Then continuation bytes that set high bits.
	b := []byte{0x3f} // 0011 1111  -> type 3, size low = 0xf
	// Add enough continuation to set bit 63 (very large theoretical object)
	for i := 0; i < 10; i++ {
		b = append(b, 0xff)
	}
	b = append(b, 0x00) // terminate

	r := bytes.NewReader(b)
	h, err := readObjectHeader(r)
	if err != nil {
		t.Fatal(err)
	}
	if h.UnpackedSize == 0 {
		t.Error("size became zero (overflow or parse error)")
	}
	// With uint64 we should have a huge positive value, not negative.
	if int64(h.UnpackedSize) < 0 {
		t.Errorf("UnpackedSize interpreted as negative: %d", int64(h.UnpackedSize))
	}
}

// Note: A full round-trip StreamPackfile test with a valid zlib-compressed
// PACK requires either checking in a small fixture or using git to generate
// one at test time. The header parser (the security-critical part for size
// limits) is covered by TestReadObjectHeader above.

func TestStreamPackfileRejectsBadMagic(t *testing.T) {
	bad := []byte("JUNK\x00\x00\x00\x02\x00\x00\x00\x01")
	err := StreamPackfile(bytes.NewReader(bad), func(ObjectHeader) error { return nil })
	if err == nil || !bytes.Contains([]byte(err.Error()), []byte("invalid signature")) {
		t.Errorf("expected invalid signature error, got %v", err)
	}
}
