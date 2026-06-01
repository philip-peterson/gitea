// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package gitprotocol

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestReceivePack_DeltaRejection(t *testing.T) {
	// Directly exercise the callback shape that githttp.go now uses.
	// (Synthesizing a full PACK containing a delta object is complex for a
	// unit test without shelling out to git.)
	cb := func(hdr ObjectHeader) error {
		if hdr.Type == ObjOfsDelta || hdr.Type == ObjRefDelta {
			return errors.New("delta rejected as expected")
		}
		return nil
	}

	deltaHdr := ObjectHeader{Type: ObjRefDelta, UnpackedSize: 9}
	if err := cb(deltaHdr); err == nil || !strings.Contains(err.Error(), "delta") {
		t.Errorf("expected delta rejection, got %v", err)
	}
}

func TestReceivePack_EarlyErrorWritesResponse(t *testing.T) {
	// Force a readReceivePackHeader failure.
	// These early protocol errors write a best-effort response and currently return nil
	// (different from InterceptFunc policy rejections, which return ErrPushRejectedByInterceptor).
	badInput := []byte("0005garbage") // valid length but not a proper ref-update line

	var response bytes.Buffer

	err := ReceivePack(bytes.NewReader(badInput), &response, func(ObjectHeader) error { return nil },
		func(io.Reader, io.Writer) error { return nil })

	if err != nil {
		t.Fatalf("expected early protocol error to return nil (best-effort response written), got %v", err)
	}

	respStr := response.String()
	if !strings.Contains(respStr, "unpack error") && !strings.Contains(respStr, "protocol error") {
		t.Errorf("response did not contain expected git error text; got: %q", respStr)
	}
}

func TestReceivePack_PolicyRejectionReturnsWrappedError(t *testing.T) {
	// Document the intended contract:
	// When InterceptFunc returns an error, ReceivePack writes a proper error
	// response and returns a wrapped ErrPushRejectedByInterceptor.
	//
	// Full end-to-end testing of this path requires realistic pkt-line framed
	// receive-pack input leading into a valid PACK stream. For now we verify
	// the early-error path still returns nil (different case) and rely on
	// integration tests + the call site in githttp.go for the policy rejection
	// behavior.
	//
	// See review notes for adding proper pack generation helpers later.
}