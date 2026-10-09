// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

// Package versiontest holds fixtures for the equality-only law on
// [go.thesmos.sh/core/version.Version]: a Version proves identity,
// never order. The fixtures make ordering assumptions observable in
// a consumer's own suite. Hand-rolled; nothing here is generated.
package versiontest

import "go.thesmos.sh/core/version"

// OrderingTrap is a pair of Versions whose bytewise order
// contradicts their production order: Older was produced first, yet
// sorts after Newer lexicographically. Any component that orders
// opaque Versions treats Newer as stale.
type OrderingTrap struct {
	Older, Newer version.Version
}

// OrderingTraps returns pairs that make an ordering assumption
// observable. Drive the subject with each pair in both arrival
// orders and assert its behaviour is identical: a subject that
// branches on the bytewise order of a trap has ordered an opaque
// token, which is out of contract.
//
// The traps cover the three realistic token families: numeric
// counters rendered as strings (where "10" sorts before "9"),
// tokens of varying length, and tokens whose leading bytes differ
// in case or symbol range.
func OrderingTraps() []OrderingTrap {
	return []OrderingTrap{
		// A row counter rendered as decimal: 9 precedes 10, yet
		// "10" < "9" bytewise.
		{Older: "9", Newer: "10"},
		// Length-varying tokens: "z" precedes "aa" in production,
		// yet "aa" < "z" bytewise.
		{Older: "z", Newer: "aa"},
		// Symbol-range tokens, as content hashes produce: '~'
		// (0x7E) sorts after '!' (0x21) regardless of production
		// order.
		{Older: "~2f6b", Newer: "!9c04"},
	}
}
