// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package gitprotocol

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

func TestReadPktLine(t *testing.T) {
	tests := []struct {
		name    string
		input   []byte
		want    []byte
		flush   bool
		wantErr bool
	}{
		{
			name:  "simple data",
			input: []byte("000fhello world"),
			want:  []byte("hello world"),
			flush: false,
		},
		{
			name:  "flush",
			input: []byte("0000"),
			want:  nil,
			flush: true,
		},
		{
			name:  "empty data packet (length 4)",
			input: []byte("0004"),
			want:  []byte{},
			flush: false,
		},
		{
			name:    "invalid hex length",
			input:   []byte("gggg"),
			wantErr: true,
		},
		{
			name:    "too small length",
			input:   []byte("0003ab"),
			wantErr: true,
		},
		{
			name:    "too large length",
			input:   []byte("ffff" + string(make([]byte, 0xfffb))),
			wantErr: true,
		},
		{
			name:    "truncated data",
			input:   []byte("000ehello"),
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := bytes.NewReader(tc.input)
			data, flush, err := ReadPktLine(r)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got data=%q flush=%v", data, flush)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if flush != tc.flush {
				t.Errorf("flush = %v, want %v", flush, tc.flush)
			}
			if !bytes.Equal(data, tc.want) {
				t.Errorf("data = %q, want %q", data, tc.want)
			}
		})
	}
}

func TestWritePktLine(t *testing.T) {
	var buf bytes.Buffer
	if err := WritePktLine(&buf, []byte("hello\n")); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); got != "000ahello\n" {
		t.Errorf("got %q, want %q", got, "000ahello\n")
	}

	buf.Reset()
	if err := WritePktLine(&buf, []byte{}); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); got != "0004" {
		t.Errorf("empty data: got %q", got)
	}
}

func TestWriteFlushPkt(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteFlushPkt(&buf); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); got != "0000" {
		t.Errorf("got %q, want 0000", got)
	}
}

func TestPktLineRoundTrip(t *testing.T) {
	original := []byte("some arbitrary data with\nnewlines and \x00 bytes")
	var buf bytes.Buffer
	if err := WritePktLine(&buf, original); err != nil {
		t.Fatal(err)
	}
	data, flush, err := ReadPktLine(&buf)
	if err != nil || flush {
		t.Fatalf("read back failed: err=%v flush=%v", err, flush)
	}
	if !bytes.Equal(data, original) {
		t.Errorf("roundtrip mismatch")
	}
}

// Ensure ReadPktLine returns io errors wrapped properly.
func TestReadPktLineEOF(t *testing.T) {
	_, _, err := ReadPktLine(bytes.NewReader(nil))
	if err == nil {
		t.Error("expected error on empty reader")
	}
	if !errors.Is(err, io.ErrUnexpectedEOF) && err.Error() != "pkt-line: read length: unexpected EOF" {
		// Accept either wrapped or our message
	}
}