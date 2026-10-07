// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package checkpoint

//go:generate go tool kanon -type=PolicyName,Log,Witness,Group,Policy -validate=valid -canonical

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"strconv"

	"go.thesmos.sh/core/crypto/sign"
	"go.thesmos.sh/core/note"
)

const (
	// The keywords that start the lines of a policy file.
	keywordLog     = "log"
	keywordWitness = "witness"
	keywordGroup   = "group"
	keywordQuorum  = "quorum"

	// thresholdAny is the threshold of a group that one member satisfies,
	// and thresholdAll the threshold of a group that every member must
	// satisfy.
	thresholdAny = "any"
	thresholdAll = "all"

	// comment starts the first item of a comment line.
	comment = '#'

	// space and tab separate the items of a line. A tab is the only octet
	// below 0x20 other than newline that a policy file contains.
	space = ' '
	tab   = '\t'

	// del is the octet 0x7F, which a policy file does not contain.
	del = 0x7f

	// keySeparator separates the name, the key ID and the base64 of a
	// verifier key.
	keySeparator = '+'

	// stackRules is the number of rules whose children
	// [Policy.QuorumRule] collects on the stack.
	stackRules = 32

	// logChildren is the number of children that a log adds at most to the
	// rules of a [Verifier]: its leaf in the log rule of its origin, and
	// the log rule and the quorum under the root of its origin.
	logChildren = 3
)

// errChanged stops the parse of the bytes of a policy file at the first
// line that differs from the line at its position in the Policy, or that
// has no position in it. [Policy.UnmarshalText] then parses the string of
// the file. No function returns it.
var errChanged = errors.New("checkpoint: the policy file differs from the Policy")

// PolicyName is the name of a witness or a group in a tlog-policy: a
// non-empty sequence of the octets 0x21 to 0x7E and 0x80 to 0xFF.
// Witnesses and groups share one namespace. A policy compares names as
// octets, so a name need not be UTF-8, and two encodings of one character
// are two names.
//
// kanon encodes a PolicyName as a string. The kanon codec of a struct with
// a PolicyName field returns an error that wraps [ErrPolicy] for a name
// that is not Valid, on encode and on decode.
type PolicyName string

// QuorumNone is the predefined quorum of a policy that requires no
// cosignature. No witness or group can have this name.
const QuorumNone PolicyName = "none"

// Valid reports whether n is a name that a policy file can contain.
//
// # Allocation contract
//
// Zero-alloc.
func (n PolicyName) Valid() bool {
	if n == "" {
		return false
	}

	for i := range len(n) {
		if n[i] <= space || n[i] == del {
			return false
		}
	}

	return true
}

// valid returns nil for a Valid name, and an error that wraps [ErrPolicy]
// for any other. The generated ValidateKanon calls it.
func (n PolicyName) valid() error {
	if !n.Valid() {
		return fmt.Errorf("%w: %q is not a name of a policy", ErrPolicy, n)
	}

	return nil
}

// Log is a line "log <vkey> [<url>]" of a policy file. A log signs the
// checkpoints whose origin is its key name.
type Log struct {
	// URL is the optional URL. tlog-policy leaves its meaning to the
	// application, and this package does not read it.
	URL string

	// Key is the log's verifier key.
	Key note.Key
}

// Valid reports whether l has a Valid key.
//
// # Allocation contract
//
// Zero-alloc.
func (l Log) Valid() bool {
	return l.Key.Valid()
}

// Witness is a line "witness <name> <vkey> [<url>]" of a policy file.
type Witness struct {
	// Name is the witness's name in the policy.
	Name PolicyName

	// URL is the optional URL. tlog-policy leaves its meaning to the
	// application, and this package does not read it.
	URL string

	// Key is the witness's verifier key.
	Key note.Key
}

// Valid reports whether w has a Valid name other than [QuorumNone] and a
// Valid key.
//
// # Allocation contract
//
// Zero-alloc.
func (w Witness) Valid() bool {
	return definable(w.Name) && w.Key.Valid()
}

// Group is a line "group <name> <threshold> <member>..." of a policy
// file. A group counts when at least Threshold of its members count.
type Group struct {
	// Name is the group's name in the policy.
	Name PolicyName

	// Members are the names of the group's witnesses and groups.
	Members []PolicyName

	// Threshold is the number of members that must count: the number of
	// the line, 1 for "any", and len(Members) for "all".
	Threshold int
}

// Valid reports whether g has a Valid name other than [QuorumNone], Valid
// members other than QuorumNone that differ from each other, and a
// threshold from 1 to the number of members, so a Valid group has a
// member.
//
// # Allocation contract
//
// Zero-alloc. It compares each pair of members.
func (g Group) Valid() bool {
	if !definable(g.Name) || g.Threshold < 1 || g.Threshold > len(g.Members) {
		return false
	}

	for i, m := range g.Members {
		if !definable(m) || slices.Contains(g.Members[:i], m) {
			return false
		}
	}

	return true
}

// faultKind is a rule of tlog-policy that a policy breaks.
type faultKind uint8

// The rules of tlog-policy that a policy breaks.
const (
	// noFault is the kind of a policy that breaks no rule.
	noFault faultKind = 0

	// faultLog is a log without a Valid key.
	faultLog faultKind = 1

	// faultWitness is a witness that is not Valid.
	faultWitness faultKind = 2

	// faultGroup is a group that is not Valid.
	faultGroup faultKind = 3

	// faultDefinedTwice is a name that two witnesses or groups define.
	faultDefinedTwice faultKind = 4

	// faultUndefinedMember is a member that no earlier line defines.
	faultUndefinedMember faultKind = 5

	// faultMemberTwice is a name that two groups list.
	faultMemberTwice faultKind = 6

	// faultSharedKey is a public key of two logs or witnesses.
	faultSharedKey faultKind = 7

	// faultQuorum is a quorum that no earlier line defines.
	faultQuorum faultKind = 8
)

// fault is the first rule of tlog-policy that a policy breaks, with the
// names that its error reports. It is a value, so that [Policy.Valid]
// allocates nothing, and only err allocates.
type fault struct {
	// name is the witness, the group or the quorum of the rule.
	name PolicyName

	// member is the member of faultUndefinedMember and faultMemberTwice.
	member PolicyName

	// key is the key name of faultSharedKey.
	key note.Name

	// kind is the rule.
	kind faultKind
}

// err returns the error of f, which wraps [ErrPolicy], or nil for noFault.
func (f fault) err() error {
	switch f.kind {
	case faultLog:
		return fmt.Errorf("%w: a log with an invalid key", ErrPolicy)
	case faultWitness:
		return fmt.Errorf("%w: the witness %q needs a valid name other than none and a valid key", ErrPolicy, f.name)
	case faultGroup:
		return fmt.Errorf("%w: the group %q needs a valid name other than none, distinct members other than "+
			"none, and a threshold from 1 to the number of members", ErrPolicy, f.name)
	case faultDefinedTwice:
		return fmt.Errorf("%w: %q is defined twice", ErrPolicy, f.name)
	case faultUndefinedMember:
		return fmt.Errorf("%w: the group %q lists %q, which no earlier line defines", ErrPolicy, f.name, f.member)
	case faultMemberTwice:
		return fmt.Errorf("%w: %q is a member of two groups", ErrPolicy, f.member)
	case faultSharedKey:
		return fmt.Errorf("%w: the public key of %s appears on two lines", ErrPolicy, f.key)
	case faultQuorum:
		return fmt.Errorf("%w: the quorum %q names no witness or group of an earlier line", ErrPolicy, f.name)
	default:
		return nil
	}
}

// Policy is a C2SP tlog-policy: the logs whose checkpoints a verifier
// accepts, the witnesses and groups whose cosignatures count, and the
// quorum of cosignatures that a checkpoint needs.
//
// [ParsePolicy] returns a Valid Policy. A Policy that a caller builds is
// checked by [Policy.Valid], [Policy.QuorumRule] and [Verifier.Reset].
//
// # Encoding
//
// ParsePolicy and [Policy.UnmarshalText] read the text form that
// tlog-policy specifies. kanon generates the canonical codecs of Policy,
// [Log], [Witness] and [Group], the form of a policy in a kanon record,
// with the field numbers of their declaration order: Quorum 1, Logs 2,
// Witnesses 3 and Groups 4 of Policy, URL 1 and Key 2 of Log, Name 1, URL
// 2 and Key 3 of Witness, and Name 1, Members 2 and Threshold 3 of Group.
// A decode checks each name with [PolicyName.Valid] and each key with the
// codec of [note.Key], and none of the rules of [Policy.Valid], which
// Verifier.Reset and Policy.QuorumRule check.
type Policy struct {
	// Quorum is the name of the witness or group whose cosignatures a
	// checkpoint needs, or [QuorumNone].
	Quorum PolicyName

	// Logs are the log lines, in the order of the file. A policy file may
	// omit them, as tlog-policy permits for an application that knows
	// its logs from another source. Such an application appends them
	// before it builds a [Verifier].
	Logs []Log

	// Witnesses are the witness lines, in the order of the file.
	Witnesses []Witness

	// Groups are the group lines, in the order of the file. A group lists
	// witnesses and the groups before it.
	Groups []Group
}

// ParsePolicy parses a tlog-policy file into a new Policy, as
// [Policy.UnmarshalText] parses it.
//
// Returns the errors of UnmarshalText, with the zero Policy.
//
// # Allocation contract
//
// Allocates the string of text once, and takes every name, URL and key
// name of the Policy from it. Allocates the slices of logs, witnesses and
// groups at their length, one slice for the members of every group, and
// one buffer for the public keys of every key: six allocations for a
// policy with groups. A type without an assigned byte allocates once more
// for each run of keys of that type. UnmarshalText into a Policy that
// contains the policy of text is the path without these allocations.
func ParsePolicy(text []byte) (Policy, error) {
	var p Policy
	if err := p.UnmarshalText(text); err != nil {
		return Policy{}, err
	}

	return p, nil
}

// UnmarshalText sets p to the policy of the tlog-policy file text. It
// implements [encoding.TextUnmarshaler]. It enforces these rules of
// tlog-policy:
//
//   - The file contains only the octets 0x09, 0x0A, 0x20 to 0x7E and 0x80
//     to 0xFF.
//   - Spaces and tabs separate the items of a line. An empty line, and a
//     line whose first item starts with '#', is ignored. A '#' after the
//     first item is part of an item.
//   - A log line has 2 or 3 items, a witness line 3 or 4, a group line at
//     least 4, and a quorum line exactly 2.
//   - Each witness or group name is defined once, and "none" is reserved.
//   - A group lists only witnesses and groups of earlier lines, and a name
//     is a member at most once in the file.
//   - A threshold is "any", "all", or ASCII digits whose value is from 1
//     to the number of members.
//   - The file has exactly one quorum line, which names a witness or a
//     group of an earlier line, or "none".
//   - Each public key appears on one log or witness line at most.
//
// The last line may omit its newline.
//
// UnmarshalText first compares each line of text with the line at its
// position in p, and keeps p as it is when every line is equal, which a
// reload of an unchanged file is. A file whose lines repeat only the
// first lines of p sets p to those lines. Otherwise it parses text as
// [ParsePolicy] does, into the slices of p when they have room.
//
// Returns [ErrPolicy], classified [errs.Invalid], for each rule that text
// breaks, with the number of the line that breaks it, and for a file
// without a quorum line. After an error p has no line and no quorum, and
// keeps its memory for the next UnmarshalText.
//
// # Allocation contract
//
// Zero-alloc when text repeats the policy that p contains, or its first
// lines. It compares each key with the verifier key that it writes into a
// pooled buffer. Otherwise allocates what ParsePolicy allocates, apart
// from the slices of p that have room.
func (p *Policy) UnmarshalText(text []byte) error {
	err := parsePolicy(p, text, false)
	if errors.Is(err, errChanged) {
		err = parsePolicy(p, string(text), true)
	}

	if err != nil {
		p.Quorum, p.Logs, p.Witnesses, p.Groups = "", p.Logs[:0], p.Witnesses[:0], p.Groups[:0]

		return err
	}

	return nil
}

// Valid reports whether every log, witness and group of p is Valid,
// whether each name is defined once, whether each group lists only
// witnesses and the groups before it, whether each name is a member at
// most once and each public key appears once, and whether the quorum is
// a witness, a group, or [QuorumNone]. It compares each definition with
// the definitions before it.
//
// # Allocation contract
//
// Zero-alloc.
func (p *Policy) Valid() bool {
	return p.validate().kind == noFault
}

// QuorumRule returns the quorum of p as a [sign.Rule] in the memory of
// rules, with the [note.Verifier] of each witness key from keys: an
// [sign.AllOf] rule of its key for a witness, named after the witness, and
// an [sign.AtLeast] rule of its members with its threshold for a group,
// named after the group. A [Verifier] builds its trees from the same rule.
//
// The caller resets keys with the [note.Resolver] of its witness types,
// and rules before each tree, as [sign.Rules.Reset] documents:
//
//	var (
//		rules sign.Rules
//		keys  note.Keyring
//	)
//
//	keys.Reset(r)
//	quorum, err := p.QuorumRule(&rules, &keys)
//
// A caller combines the rule with a log rule of its own, such as an AllOf
// rule of the keys of a hybrid log, every one of which signs.
//
// Returns [ErrPolicy], classified [errs.Invalid], for a Policy that is not
// Valid and for the quorum [QuorumNone], and the error of
// [note.Keyring.Verifier] for a witness key that keys cannot resolve.
//
// # Allocation contract
//
// Zero-alloc when rules has room for the rule and keys keeps the Verifier
// of each witness key, as for a policy that the caller built before with
// the same rules and keys. Otherwise allocates what keys allocates and the
// growth of rules. It collects the children of up to 32 rules on the
// stack, and allocates them for a larger quorum.
func (p *Policy) QuorumRule(rules *sign.Rules, keys *note.Keyring) (sign.Rule, error) {
	if err := p.validate().err(); err != nil {
		return sign.Rule{}, err
	}

	if p.Quorum == QuorumNone {
		return sign.Rule{}, fmt.Errorf("%w: the quorum none has no rule", ErrPolicy)
	}

	return p.quorumRule(rules, keys)
}

// quorumRule is QuorumRule for a Valid policy whose quorum is not
// [QuorumNone].
func (p *Policy) quorumRule(rules *sign.Rules, keys *note.Keyring) (sign.Rule, error) {
	var stack [stackRules]sign.Rule

	_, quorum, err := p.appendRule(rules, keys, stack[:0], p.Quorum)

	return quorum, err
}

// appendRule returns the rule of the witness or the group name of p, a
// Valid policy, in the memory of rules. scratch contains the children of
// the groups on the path to name that their rules collect so far. A group
// collects the rules of its members after them, and appendRule returns
// scratch at its length on entry.
func (p *Policy) appendRule(
	rules *sign.Rules, keys *note.Keyring, scratch []sign.Rule, name PolicyName,
) ([]sign.Rule, sign.Rule, error) {
	isWitness := func(w Witness) bool { return w.Name == name }
	isGroup := func(g Group) bool { return g.Name == name }

	if i := slices.IndexFunc(p.Witnesses, isWitness); i != -1 {
		v, err := keys.Verifier(p.Witnesses[i].Key)
		if err != nil {
			return scratch, sign.Rule{}, err
		}

		return scratch, rules.AllOf(string(name), v), nil
	}

	g := p.Groups[slices.IndexFunc(p.Groups, isGroup)]
	start := len(scratch)

	for _, m := range g.Members {
		var (
			child sign.Rule
			err   error
		)

		if scratch, child, err = p.appendRule(rules, keys, scratch, m); err != nil {
			return scratch[:start], sign.Rule{}, err
		}

		scratch = append(scratch, child)
	}

	rule := rules.AtLeast(string(name), g.Threshold, scratch[start:]...)

	return scratch[:start], rule, nil
}

// ruleSize returns the number of keys and of children of the rules that
// a [Verifier] of p builds at most. Each log adds its key, and up to
// three children: its leaf in the log rule of its origin, and the log rule
// and the quorum under the root of its origin. The quorum adds at most
// every witness key and a child for each member of each group.
func (p *Policy) ruleSize() (keys, children int) {
	for range p.Logs {
		keys++
		children += logChildren
	}

	keys += len(p.Witnesses)

	for _, g := range p.Groups {
		children += len(g.Members)
	}

	return keys, children
}

// validate returns the first rule of Policy.Valid that p breaks. It
// checks the logs, the witnesses, the groups and the quorum in that
// order, each against the definitions before it, so a group lists every
// witness and the groups before it.
func (p *Policy) validate() fault {
	var seen Policy

	for i, l := range p.Logs {
		if f := seen.checkLog(l); f.kind != noFault {
			return f
		}

		seen.Logs = p.Logs[:i+1]
	}

	for i, w := range p.Witnesses {
		if f := seen.checkWitness(w); f.kind != noFault {
			return f
		}

		seen.Witnesses = p.Witnesses[:i+1]
	}

	for i, g := range p.Groups {
		if f := seen.checkGroup(g); f.kind != noFault {
			return f
		}

		seen.Groups = p.Groups[:i+1]
	}

	return seen.checkQuorum(p.Quorum)
}

// checkLog checks l, a log after the definitions of p.
func (p *Policy) checkLog(l Log) fault {
	if !l.Valid() {
		return fault{kind: faultLog}
	}

	return p.checkKey(l.Key)
}

// checkWitness checks w, a witness after the definitions of p.
func (p *Policy) checkWitness(w Witness) fault {
	if !w.Valid() {
		return fault{kind: faultWitness, name: w.Name}
	}

	if p.defines(w.Name) {
		return fault{kind: faultDefinedTwice, name: w.Name}
	}

	return p.checkKey(w.Key)
}

// checkGroup checks g, a group after the definitions of p.
func (p *Policy) checkGroup(g Group) fault {
	if !g.Valid() {
		return fault{kind: faultGroup, name: g.Name}
	}

	if p.defines(g.Name) {
		return fault{kind: faultDefinedTwice, name: g.Name}
	}

	for _, m := range g.Members {
		if !p.defines(m) {
			return fault{kind: faultUndefinedMember, name: g.Name, member: m}
		}

		if p.lists(m) {
			return fault{kind: faultMemberTwice, member: m}
		}
	}

	return fault{}
}

// checkQuorum checks q, a quorum after the definitions of p: a name that p
// defines, or [QuorumNone].
func (p *Policy) checkQuorum(q PolicyName) fault {
	if q != QuorumNone && !p.defines(q) {
		return fault{kind: faultQuorum, name: q}
	}

	return fault{}
}

// checkKey checks k, the key of a log or a witness after the definitions
// of p: its public key is the public key of no log or witness of p.
func (p *Policy) checkKey(k note.Key) fault {
	inLog := func(l Log) bool { return bytes.Equal(l.Key.PublicKey, k.PublicKey) }
	inWitness := func(w Witness) bool { return bytes.Equal(w.Key.PublicKey, k.PublicKey) }

	if slices.ContainsFunc(p.Logs, inLog) || slices.ContainsFunc(p.Witnesses, inWitness) {
		return fault{kind: faultSharedKey, key: k.Name}
	}

	return fault{}
}

// defines reports whether a witness or a group of p has the name.
func (p *Policy) defines(name PolicyName) bool {
	isWitness := func(w Witness) bool { return w.Name == name }
	isGroup := func(g Group) bool { return g.Name == name }

	return slices.ContainsFunc(p.Witnesses, isWitness) || slices.ContainsFunc(p.Groups, isGroup)
}

// lists reports whether a group of p lists the name as a member.
func (p *Policy) lists(name PolicyName) bool {
	return slices.ContainsFunc(p.Groups, func(g Group) bool { return slices.Contains(g.Members, name) })
}

// policyParser parses a policy file of type S line by line into cur, the
// lines so far, whose slices start at the memory of the slices of old,
// the Policy before the parse.
//
// Over the bytes of a file (fresh false) it compares each line with the
// line at its position in old, appends the line of old, and returns
// errChanged at the first line that differs. It makes no string and
// allocates nothing.
//
// Over the string of a file (fresh true) it builds each line: each name
// and URL is a substring of the file, and each key takes its name from the
// file and its public key from keys. members and keys are the memory that
// the size of the file requires, and each line takes the next part of
// them, so that a parse of the string allocates each of them once.
type policyParser[S ~string | ~[]byte] struct {
	// old is the Policy before the parse.
	old Policy

	// cur contains the lines so far.
	cur Policy

	// members is the memory of the members of the groups after the line
	// that the parse is at.
	members []PolicyName

	// typ is the type of the last key, which the next key reuses when its
	// type is equal, so that the keys of a type without an assigned byte
	// share one Type.
	typ note.Type

	// keys is the memory of the public keys of the lines after the line
	// that the parse is at.
	keys []byte

	// fresh reports whether the parser builds the lines of a string.
	fresh bool
}

// parsePolicy sets p to the policy of text, a policy file in bytes or in a
// string, as policyParser parses it. It returns errChanged unwrapped, and
// every other error with the number of its line.
func parsePolicy[S ~string | ~[]byte](p *Policy, text S, fresh bool) error {
	pp := policyParser[S]{
		old:   *p,
		cur:   Policy{Logs: p.Logs[:0], Witnesses: p.Witnesses[:0], Groups: p.Groups[:0]},
		fresh: fresh,
	}

	if fresh {
		pp.size(text)
	}

	n := 0

	for rest := text; len(rest) != 0; {
		var line S

		line, rest = cutByte(rest, newline)
		n++

		if err := pp.line(line); err != nil {
			if errors.Is(err, errChanged) {
				return err
			}

			return fmt.Errorf("%w, on line %d", err, n)
		}
	}

	if pp.cur.Quorum == "" {
		return fmt.Errorf("%w: no quorum line", ErrPolicy)
	}

	*p = pp.cur

	return nil
}

// size sizes the memory of a parse of text: the slices of cur at the
// number of their lines, and the members and the public keys of the file
// at their sum. It counts each line by its keyword, and the parse refuses
// a line that it counts wrongly.
func (pp *policyParser[S]) size(text S) {
	var logs, witnesses, groups, members, keys int

	for rest := text; len(rest) != 0; {
		var line S

		line, rest = cutByte(rest, newline)
		keyword, items := nextItem(line)

		switch string(keyword) {
		case keywordLog:
			vkey, _ := nextItem(items)
			logs++
			keys += keyBytes(vkey)
		case keywordWitness:
			_, items = nextItem(items)
			vkey, _ := nextItem(items)
			witnesses++
			keys += keyBytes(vkey)
		case keywordGroup:
			//dokimi:mutate-skip sbr-delete: an item that the count does not skip adds one slot to the memory of the members, which no group takes
			_, items = nextItem(items)
			//dokimi:mutate-skip sbr-delete: an item that the count does not skip adds one slot to the memory of the members, which no group takes
			_, items = nextItem(items)
			groups++
			members += countItems(items)
		}
	}

	pp.cur.Logs = slices.Grow(pp.cur.Logs, logs)
	pp.cur.Witnesses = slices.Grow(pp.cur.Witnesses, witnesses)
	pp.cur.Groups = slices.Grow(pp.cur.Groups, groups)
	pp.members = make([]PolicyName, members)
	pp.keys = make([]byte, keys)
}

// line adds the definition of line, a line of a policy file without its
// newline, to cur, and checks it against the definitions of cur. It
// returns an error that wraps [ErrPolicy] for a line that breaks a rule of
// [Policy.UnmarshalText].
func (pp *policyParser[S]) line(line S) error {
	for i := range len(line) {
		if forbidden(line[i]) {
			return fmt.Errorf("%w: the octet %#02x, outside 0x09, 0x20 to 0x7E and 0x80 to 0xFF", ErrPolicy, line[i])
		}
	}

	keyword, items := nextItem(line)
	if len(keyword) == 0 || keyword[0] == comment {
		return nil
	}

	switch string(keyword) {
	case keywordLog:
		return pp.log(items)
	case keywordWitness:
		return pp.witness(items)
	case keywordGroup:
		return pp.group(items)
	case keywordQuorum:
		return pp.quorum(items)
	}

	return fmt.Errorf("%w: the unknown keyword %q", ErrPolicy, string(keyword))
}

// log adds the log of the items "<vkey> [<url>]" to cur.
func (pp *policyParser[S]) log(items S) error {
	vkey, items := nextItem(items)
	url, items := nextItem(items)

	if extra, _ := nextItem(items); len(vkey) == 0 || len(extra) != 0 {
		return fmt.Errorf("%w: a log line has 2 or 3 items", ErrPolicy)
	}

	var l Log

	if pp.fresh {
		k, err := pp.key(vkey)
		if err != nil {
			return err
		}

		l = Log{URL: string(url), Key: k}
	} else {
		i := len(pp.cur.Logs)
		if i == len(pp.old.Logs) || string(url) != pp.old.Logs[i].URL || !sameKey(pp.old.Logs[i].Key, vkey) {
			return errChanged
		}

		l = pp.old.Logs[i]
	}

	if err := pp.cur.checkLog(l).err(); err != nil {
		return err
	}

	pp.cur.Logs = append(pp.cur.Logs, l)

	return nil
}

// witness adds the witness of the items "<name> <vkey> [<url>]" to cur.
func (pp *policyParser[S]) witness(items S) error {
	name, items := nextItem(items)
	vkey, items := nextItem(items)
	url, items := nextItem(items)

	if extra, _ := nextItem(items); len(vkey) == 0 || len(extra) != 0 {
		return fmt.Errorf("%w: a witness line has 3 or 4 items", ErrPolicy)
	}

	var w Witness

	if pp.fresh {
		k, err := pp.key(vkey)
		if err != nil {
			return err
		}

		w = Witness{Name: PolicyName(name), URL: string(url), Key: k}
	} else {
		i := len(pp.cur.Witnesses)
		if i == len(pp.old.Witnesses) || string(name) != string(pp.old.Witnesses[i].Name) ||
			string(url) != pp.old.Witnesses[i].URL || !sameKey(pp.old.Witnesses[i].Key, vkey) {

			return errChanged
		}

		w = pp.old.Witnesses[i]
	}

	if err := pp.cur.checkWitness(w).err(); err != nil {
		return err
	}

	pp.cur.Witnesses = append(pp.cur.Witnesses, w)

	return nil
}

// group adds the group of the items "<name> <threshold> <member>..." to
// cur.
func (pp *policyParser[S]) group(items S) error {
	name, items := nextItem(items)
	threshold, members := nextItem(items)

	n := countItems(members)
	if n == 0 {
		return fmt.Errorf("%w: a group line has 4 items or more", ErrPolicy)
	}

	t, ok := parseThreshold(threshold, n)
	if !ok {
		return fmt.Errorf("%w: the threshold %q is not any, all, or a number from 1 to %d",
			ErrPolicy, string(threshold), n)
	}

	var g Group

	if pp.fresh {
		g = Group{Name: PolicyName(name), Members: pp.members[:n:n], Threshold: t}
		pp.members = pp.members[n:]
		j := 0

		for m, rest := nextItem(members); len(m) != 0; m, rest = nextItem(rest) {
			g.Members[j] = PolicyName(m)
			j++
		}
	} else {
		i := len(pp.cur.Groups)
		if i == len(pp.old.Groups) || !sameGroup(pp.old.Groups[i], name, t, members) {
			return errChanged
		}

		g = pp.old.Groups[i]
	}

	if err := pp.cur.checkGroup(g).err(); err != nil {
		return err
	}

	pp.cur.Groups = append(pp.cur.Groups, g)

	return nil
}

// quorum sets the quorum of cur to the item "<name>".
func (pp *policyParser[S]) quorum(items S) error {
	name, items := nextItem(items)

	if extra, _ := nextItem(items); len(name) == 0 || len(extra) != 0 {
		return fmt.Errorf("%w: a quorum line has 2 items", ErrPolicy)
	}

	if pp.cur.Quorum != "" {
		return fmt.Errorf("%w: a second quorum line", ErrPolicy)
	}

	q := pp.old.Quorum

	if pp.fresh {
		q = PolicyName(name)
	} else if string(name) != string(q) {
		return errChanged
	}

	if err := pp.cur.checkQuorum(q).err(); err != nil {
		return err
	}

	pp.cur.Quorum = q

	return nil
}

// key returns the key of vkey, a verifier key in the string of a file, as
// [note.Key.Set] sets it: its name is a substring of the file, and its
// public key is the next part of keys. The key starts with the type of the
// key before it, which Set keeps when the types are equal.
//
// Returns an error that wraps [ErrPolicy] and the error of Set for a
// verifier key that Set refuses.
func (pp *policyParser[S]) key(vkey S) (note.Key, error) {
	n := keyBytes(vkey)
	k := note.Key{Type: pp.typ, PublicKey: pp.keys[:0:n]}
	pp.keys = pp.keys[n:]

	if err := k.Set(string(vkey)); err != nil {
		return note.Key{}, fmt.Errorf("%w: %w", ErrPolicy, err)
	}

	pp.typ = k.Type

	return k, nil
}

// sameKey reports whether vkey is the verifier key of k. It writes the
// verifier key of k into a pooled buffer, and compares the two.
// AppendText appends nothing for a key that is not Valid, and each caller
// refuses an empty vkey first, so such a key compares unequal without a
// check of the error of AppendText.
func sameKey[S ~string | ~[]byte](k note.Key, vkey S) bool {
	buf := buffers.Get()
	defer buffers.Put(buf)

	text, _ := k.AppendText((*buf)[:0])
	*buf = text[:0]

	return string(text) == string(vkey)
}

// sameGroup reports whether g is the group of a line with name, threshold
// t and the items members.
func sameGroup[S ~string | ~[]byte](g Group, name S, t int, members S) bool {
	if string(name) != string(g.Name) || t != g.Threshold {
		return false
	}

	j := 0

	for m, rest := nextItem(members); len(m) != 0; m, rest = nextItem(rest) {
		if j == len(g.Members) || string(m) != string(g.Members[j]) {
			return false
		}

		j++
	}

	return j == len(g.Members)
}

// keyBytes returns the number of bytes that the base64 of vkey, the part
// after its second '+', decodes to at most: the type, the public key, and
// up to two bytes of padding.
func keyBytes[S ~string | ~[]byte](vkey S) int {
	_, rest := cutByte(vkey, keySeparator)
	_, encoded := cutByte(rest, keySeparator)

	return base64.StdEncoding.DecodedLen(len(encoded))
}

// cutByte slices s around its first byte sep, and returns the bytes before
// and after it, or s and an empty tail when s has no sep.
func cutByte[S ~string | ~[]byte](s S, sep byte) (before, after S) {
	for i := range len(s) {
		if s[i] == sep {
			return s[:i], s[i:][1:]
		}
	}

	return s, s[len(s):]
}

// nextItem returns the first item of s, which spaces and tabs separate,
// and the bytes after it. It returns an empty item for s without one.
func nextItem[S ~string | ~[]byte](s S) (item, rest S) {
	for len(s) != 0 && separator(s[0]) {
		s = s[1:]
	}

	end := 0
	for end != len(s) && !separator(s[end]) {
		end++
	}

	return s[:end], s[end:]
}

// countItems returns the number of items of s.
func countItems[S ~string | ~[]byte](s S) int {
	n := 0
	for item, rest := nextItem(s); len(item) != 0; item, rest = nextItem(rest) {
		n++
	}

	return n
}

// parseThreshold returns the threshold of item for a group of n members:
// 1 for "any", n for "all", and the value of ASCII digits from 1 to n. It
// reports false for any other item.
//
// strconv.ParseUint accepts only the digits of base 10. It returns 0 for
// an item with any other byte, and the largest uint64 for a number that
// does not fit 64 bits. The bounds refuse both, so the error of ParseUint
// adds no case to them.
func parseThreshold[S ~string | ~[]byte](item S, n int) (int, bool) {
	switch string(item) {
	case thresholdAny:
		return 1, true
	case thresholdAll:
		return n, true
	}

	k, _ := strconv.ParseUint(string(item), 10, 64)
	if k < 1 || k > uint64(n) { //nolint:gosec // n counts the members of a line, so it is not negative
		return 0, false
	}

	return int(k), true //nolint:gosec // k is at most n
}

// definable reports whether n can name a witness or a group: a Valid name
// other than [QuorumNone].
func definable(n PolicyName) bool {
	return n.Valid() && n != QuorumNone
}

// separator reports whether c separates the items of a line.
func separator(c byte) bool {
	return c == space || c == tab
}

// forbidden reports whether c is an octet that a policy file does not
// contain: below 0x20 other than tab, and 0x7F. Newline ends a line, so a
// line contains none.
func forbidden(c byte) bool {
	return c < space && c != tab || c == del
}
