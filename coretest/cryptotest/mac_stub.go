// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package cryptotest

import (
	stdhmac "crypto/hmac"
	"crypto/subtle"
	"testing"

	"go.thesmos.sh/core/crypto"
)

// stdlibMAC is a [crypto.MAC] companion backed by stdlib
// [crypto/hmac]. Used as the inner delegate for
// [NewStdlibMACStub] — the generated [MACStub] wraps it via
// [MACStubDelegateTo] to keep the recorder / fault-injector
// layer on top.
type stdlibMAC struct {
	spec StdlibMACSpec
	tb   testing.TB
}

func (m *stdlibMAC) ID() crypto.ID { return m.spec.ID }

func (m *stdlibMAC) Algorithm() crypto.Algorithm { return m.spec.Algorithm }

func (m *stdlibMAC) Size() int { return m.spec.Size }

func (m *stdlibMAC) Sign(data []byte) crypto.Digest {
	h := stdhmac.New(m.spec.NewHash, m.spec.Key)
	_, _ = h.Write(data)
	return digestFromBytes(h.Sum(nil))
}

func (m *stdlibMAC) Verify(data, expected []byte) bool {
	if len(expected) != m.spec.Size {
		return false
	}
	h := stdhmac.New(m.spec.NewHash, m.spec.Key)
	_, _ = h.Write(data)
	return subtle.ConstantTimeCompare(h.Sum(nil), expected) == 1
}

func (m *stdlibMAC) NewStream() crypto.Stream {
	return NewStdlibStreamStub(m.tb, stdhmac.New(m.spec.NewHash, m.spec.Key))
}

// NewStdlibMACStub returns a [MACStub] delegating to a stdlib-
// backed companion described by spec. The stub layer adds
// recording / fault-injection hooks; the inner companion
// supplies real behaviour from [crypto/hmac] keyed with the
// supplied [StdlibMACSpec.Key].
func NewStdlibMACStub(tb testing.TB, spec StdlibMACSpec) *MACStub {
	return NewMACStub(tb,
		MACStubDelegateTo(&stdlibMAC{spec: spec, tb: tb}),
	)
}
