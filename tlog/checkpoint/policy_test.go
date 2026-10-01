// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package checkpoint_test

import (
	"strconv"
	"strings"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/crypto/sign"
	"go.thesmos.sh/core/crypto/sign/ed25519"
	"go.thesmos.sh/core/crypto/sign/mldsa"
	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/note"
	"go.thesmos.sh/core/tlog/checkpoint"
)

// ageKeyserverFile is the witness policy of filippo.io/torchwood v0.10.0,
// cmd/age-keyserver/witness_policy.txt.
const ageKeyserverFile = "age-keyserver-policy.txt"

// exampleLog is the key name of the log of the policies of the tests.
const exampleLog = "example.com/log"

// parsePolicyAllocs is the number of allocations of ParsePolicy for the
// example of tlog-policy, with one log, six witnesses and three groups: the
// string of the file, the slices of logs, witnesses and groups, the
// members, and the public keys.
const parsePolicyAllocs = 6

// The witnesses of the example of tlog-policy, by their name in the
// policy and their key name.
var (
	xWitnesses = map[checkpoint.PolicyName]note.Name{
		"X1": "example.com/x1", "X2": "example.com/x2", "X3": "example.com/x3",
	}
	yWitnesses = map[checkpoint.PolicyName]note.Name{
		"Y1": "example.com/y1", "Y2": "example.com/y2", "Y3": "example.com/y3",
	}
)

func TestPolicy(t *testing.T) {
	t.Parallel()

	t.Run("PolicyName", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			give checkpoint.PolicyName
			want bool
		}{
			{name: "reports true for a name of ASCII letters and digits", give: "X1", want: true},
			{name: "reports true for the octets 0x21 and 0x7E", give: "!~", want: true},
			{name: "reports true for the octets 0x80 and 0xFF", give: "\x80\xff", want: true},
			{name: "reports true for a name that starts with a number sign", give: "#a", want: true},
			{name: "reports true for none", give: checkpoint.QuorumNone, want: true},
			{name: "reports false for the empty name", give: "", want: false},
			{name: "reports false for a name with a space", give: "a b", want: false},
			{name: "reports false for a name with a tab", give: "a\tb", want: false},
			{name: "reports false for a name with the octet 0x1F", give: "a\x1f", want: false},
			{name: "reports false for a name with the octet 0x7F", give: "a\x7f", want: false},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				testkit.Equal(
					t,
					tt.give.Valid(),
					tt.want,
					"Valid must report whether a policy file can contain the name",
				)
			})
		}
	})

	t.Run("Log", func(t *testing.T) {
		t.Parallel()

		t.Run("reports true for a log with a Valid key", func(t *testing.T) {
			t.Parallel()
			testkit.True(t, checkpoint.Log{Key: logKey(t, exampleLog)}.Valid(), "Valid must accept the log")
		})

		t.Run("reports false for the zero Log", func(t *testing.T) {
			t.Parallel()
			testkit.False(t, checkpoint.Log{}.Valid(), "Valid must refuse a log without a key")
		})
	})

	t.Run("Witness", func(t *testing.T) {
		t.Parallel()

		key := witnessKey(t, "example.com/x1")

		tests := []struct {
			name string
			give checkpoint.Witness
			want bool
		}{
			{
				name: "reports true for a witness with a name and a key",
				give: checkpoint.Witness{Name: "X1", Key: key},
				want: true,
			},
			{
				name: "reports false for the name none",
				give: checkpoint.Witness{Name: checkpoint.QuorumNone, Key: key},
				want: false,
			},
			{name: "reports false for an invalid name", give: checkpoint.Witness{Name: "X 1", Key: key}, want: false},
			{name: "reports false for a key that is not Valid", give: checkpoint.Witness{Name: "X1"}, want: false},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				testkit.Equal(t, tt.give.Valid(), tt.want, "Valid must report whether the witness is complete")
			})
		}
	})

	t.Run("Group", func(t *testing.T) {
		t.Parallel()

		members := []checkpoint.PolicyName{"X1", "X2", "X3"}

		tests := []struct {
			name string
			give checkpoint.Group
			want bool
		}{
			{
				name: "reports true for 2 of 3 members",
				give: checkpoint.Group{Name: "X", Members: members, Threshold: 2},
				want: true,
			},
			{
				name: "reports true for the threshold 1",
				give: checkpoint.Group{Name: "X", Members: members, Threshold: 1},
				want: true,
			},
			{
				name: "reports true for a threshold of every member",
				give: checkpoint.Group{Name: "X", Members: members, Threshold: 3},
				want: true,
			},
			{
				name: "reports false for the threshold 0",
				give: checkpoint.Group{Name: "X", Members: members, Threshold: 0},
				want: false,
			},
			{
				name: "reports false for a threshold above the members",
				give: checkpoint.Group{Name: "X", Members: members, Threshold: 4},
				want: false,
			},
			{
				name: "reports false for a group without members",
				give: checkpoint.Group{Name: "X", Threshold: 1},
				want: false,
			},
			{
				name: "reports false for the name none",
				give: checkpoint.Group{Name: checkpoint.QuorumNone, Members: members, Threshold: 1},
				want: false,
			},
			{
				name: "reports false for an invalid name",
				give: checkpoint.Group{Name: "X Y", Members: members, Threshold: 1},
				want: false,
			},
			{
				name: "reports false for the member none",
				give: checkpoint.Group{
					Name:      "X",
					Members:   []checkpoint.PolicyName{"X1", checkpoint.QuorumNone},
					Threshold: 1,
				},
				want: false,
			},
			{
				name: "reports false for an invalid member",
				give: checkpoint.Group{Name: "X", Members: []checkpoint.PolicyName{"X1", ""}, Threshold: 1},
				want: false,
			},
			{
				name: "reports false for a member listed twice",
				give: checkpoint.Group{Name: "X", Members: []checkpoint.PolicyName{"X1", "X2", "X1"}, Threshold: 1},
				want: false,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				testkit.Equal(t, tt.give.Valid(), tt.want, "Valid must report whether the group is complete")
			})
		}
	})

	t.Run("ParsePolicy", func(t *testing.T) {
		t.Parallel()

		w1 := witnessKey(t, "example.com/w1").String()
		w2 := witnessKey(t, "example.com/w2").String()
		lg := logKey(t, exampleLog).String()

		t.Run("returns the policy of the example of tlog-policy", func(t *testing.T) {
			t.Parallel()
			got, err := checkpoint.ParsePolicy([]byte(examplePolicyText(t)))
			testkit.NoError(t, err, "ParsePolicy must accept the example")
			testkit.Equal(t, got, *examplePolicy(t), "ParsePolicy must return every line of the example")
		})

		t.Run("returns the keys of a type without an assigned byte", func(t *testing.T) {
			t.Parallel()
			text := "log " + lg + "\nwitness a " + hybridKey(t, "example.com/a").String() + "\nwitness b " +
				hybridKey(t, "example.com/b").String() + "\nquorum a\n"
			got, err := checkpoint.ParsePolicy([]byte(text))
			testkit.NoError(t, err, "ParsePolicy must accept the keys")
			testkit.Equal(t, got.Witnesses[0].Key, hybridKey(t, "example.com/a"),
				"ParsePolicy must return the key of a")
			testkit.Equal(t, got.Witnesses[1].Key, hybridKey(t, "example.com/b"),
				"ParsePolicy must return the key of b")
		})

		t.Run("returns the keys of a type of one byte after a key of a type without an assigned byte",
			func(t *testing.T) {
				t.Parallel()
				text := "witness a " + hybridKey(t, "example.com/a").String() + "\nwitness b " + w2 + "\nquorum a\n"
				got, err := checkpoint.ParsePolicy([]byte(text))
				testkit.NoError(t, err, "ParsePolicy must accept the keys")
				testkit.Equal(t, got.Witnesses[1].Key, witnessKey(t, "example.com/w2"),
					"ParsePolicy must return the key of b")
			})

		t.Run("returns the policy of the age-keyserver of torchwood v0.10.0", func(t *testing.T) {
			t.Parallel()
			got, err := checkpoint.ParsePolicy(readFile(t, ageKeyserverFile))
			testkit.NoError(t, err, "ParsePolicy must accept the policy of torchwood")
			testkit.Len(t, got.Logs, 1, "the policy has one log")
			testkit.Equal(t, got.Logs[0].Key.Name, note.Name("keyserver.geomys.org"), "the log is the keyserver")
			testkit.Len(t, got.Witnesses, 3, "the policy has three witnesses")
			testkit.Equal(t, got.Witnesses[1].Name, checkpoint.PolicyName("Mullvad"), "the second witness is Mullvad")
			testkit.Equal(t, got.Witnesses[1].URL, "https://witness.stagemole.eu/", "a witness keeps its URL")
			testkit.Equal(t, got.Groups, []checkpoint.Group{{
				Name: "public", Members: []checkpoint.PolicyName{"TrustFabric", "Mullvad", "Geomys"}, Threshold: 2,
			}}, "the policy has the group public")
			testkit.Equal(t, got.Quorum, checkpoint.PolicyName("public"), "the quorum is the group public")
		})

		t.Run("returns the policy of the vectors that torchwood v0.10.0 verified", func(t *testing.T) {
			t.Parallel()
			got, err := checkpoint.ParsePolicy(readFile(t, policyFile))
			testkit.NoError(t, err, "ParsePolicy must accept the policy of the vectors")
			testkit.Len(t, got.Witnesses, 4, "the policy has four witnesses")
			testkit.Equal(t, got.Witnesses[3].Key.Type, checkpoint.TypeMLDSA44Cosignature, "PQ has type 0x06")
			testkit.Equal(t, got.Quorum, checkpoint.PolicyName("hybrid"), "the quorum is the group hybrid")
		})

		t.Run("ignores comments, empty lines and the whitespace around items", func(t *testing.T) {
			t.Parallel()
			text := "# a comment\n\n \t\n  # an indented comment\n\tlog \t" + lg + "  \nquorum none\n"
			got, err := checkpoint.ParsePolicy([]byte(text))
			testkit.NoError(t, err, "ParsePolicy must accept the policy")
			testkit.Equal(t, got, checkpoint.Policy{
				Quorum: checkpoint.QuorumNone, Logs: []checkpoint.Log{{Key: logKey(t, exampleLog)}},
			}, "ParsePolicy must return the log and the quorum")
		})

		t.Run("keeps a number sign after the first item", func(t *testing.T) {
			t.Parallel()
			got, err := checkpoint.ParsePolicy([]byte("log " + lg + " #url\nquorum none\n"))
			testkit.NoError(t, err, "ParsePolicy must accept the policy")
			testkit.Equal(t, got.Logs[0].URL, "#url", "a number sign after the first item starts an item")
		})

		t.Run("accepts a last line without a newline", func(t *testing.T) {
			t.Parallel()
			got, err := checkpoint.ParsePolicy([]byte("log " + lg + "\nquorum none"))
			testkit.NoError(t, err, "ParsePolicy must accept the last line without a newline")
			testkit.Equal(t, got.Quorum, checkpoint.QuorumNone, "ParsePolicy must read the last line")
		})

		t.Run("returns the URL of a witness", func(t *testing.T) {
			t.Parallel()
			got, err := checkpoint.ParsePolicy([]byte("witness a " + w1 + " https://w.example/\nquorum a\n"))
			testkit.NoError(t, err, "ParsePolicy must accept the policy")
			testkit.Equal(t, got.Witnesses[0].URL, "https://w.example/", "ParsePolicy must return the URL")
		})

		t.Run("returns the thresholds 1 and of every member", func(t *testing.T) {
			t.Parallel()
			text := "witness a " + w1 + "\nwitness b " + w2 + "\ngroup g 1 a\ngroup h 2 b g\nquorum h\n"
			got, err := checkpoint.ParsePolicy([]byte(text))
			testkit.NoError(t, err, "ParsePolicy must accept the thresholds")
			testkit.Equal(t, got.Groups[0].Threshold, 1, "the threshold 1 must parse")
			testkit.Equal(t, got.Groups[1].Threshold, 2, "a threshold of every member must parse")
		})

		t.Run("accepts lines after the quorum line", func(t *testing.T) {
			t.Parallel()
			got, err := checkpoint.ParsePolicy([]byte("quorum none\nwitness a " + w1 + "\n"))
			testkit.NoError(t, err, "ParsePolicy must accept a witness after the quorum")
			testkit.Len(t, got.Witnesses, 1, "ParsePolicy must return the witness")
		})

		sameKey := note.Key{
			Name:      "example.com/w1",
			Type:      note.TypeEd25519,
			PublicKey: witnessKey(t, "example.com/w1").PublicKey,
		}

		tests := []struct {
			name string
			give string
			line string
		}{
			{
				name: "returns ErrPolicy for the octet 0x01",
				give: "log " + lg + "\x01\nquorum none\n",
				line: "on line 1",
			},
			{
				name: "returns ErrPolicy for the octet 0x7F",
				give: "quorum none\nwitness a\x7f " + w1 + "\n",
				line: "on line 2",
			},
			{name: "returns ErrPolicy for a carriage return", give: "quorum none\r\n", line: "on line 1"},
			{name: "returns ErrPolicy for an unknown keyword", give: "\nlogs " + lg + "\n", line: "on line 2"},
			{name: "returns ErrPolicy for a log line of 1 item", give: "log\n", line: "on line 1"},
			{
				name: "returns ErrPolicy for a log line of 4 items",
				give: "log " + lg + " https://l.example/ x\n",
				line: "on line 1",
			},
			{
				name: "returns ErrPolicy for a log line with a malformed key",
				give: "log example.com/log+00000000+AQ==\n",
				line: "on line 1",
			},
			{name: "returns ErrPolicy for a witness line of 2 items", give: "witness a\n", line: "on line 1"},
			{
				name: "returns ErrPolicy for a witness line of 5 items",
				give: "witness a " + w1 + " https://w.example/ x\n",
				line: "on line 1",
			},
			{
				name: "returns ErrPolicy for a witness line with a malformed key",
				give: "witness a b+00000000+BA==\n",
				line: "on line 1",
			},
			{
				name: "returns ErrPolicy for a group line of 3 items",
				give: "witness a " + w1 + "\ngroup g 1\n",
				line: "on line 2",
			},
			{name: "returns ErrPolicy for a quorum line of 1 item", give: "quorum\n", line: "on line 1"},
			{name: "returns ErrPolicy for a quorum line of 3 items", give: "quorum none x\n", line: "on line 1"},
			{name: "returns ErrPolicy for a witness named none", give: "witness none " + w1 + "\n", line: "on line 1"},
			{
				name: "returns ErrPolicy for a group named none",
				give: "witness a " + w1 + "\ngroup none 1 a\n",
				line: "on line 2",
			},
			{
				name: "returns ErrPolicy for the member none",
				give: "witness a " + w1 + "\ngroup g 1 a none\n",
				line: "on line 2",
			},
			{
				name: "returns ErrPolicy for a witness defined twice",
				give: "witness a " + w1 + "\nwitness a " + w2 + "\n",
				line: "on line 2",
			},
			{
				name: "returns ErrPolicy for a group with the name of a witness",
				give: "witness a " + w1 + "\ngroup a 1 a\n",
				line: "on line 2",
			},
			{
				name: "returns ErrPolicy for a member that no line defines",
				give: "witness a " + w1 + "\ngroup g 1 b\n",
				line: "on line 2",
			},
			{
				name: "returns ErrPolicy for a member that a later line defines",
				give: "group g 1 a\nwitness a " + w1 + "\n",
				line: "on line 1",
			},
			{
				name: "returns ErrPolicy for a group that lists itself",
				give: "witness a " + w1 + "\ngroup g 1 g\n",
				line: "on line 2",
			},
			{
				name: "returns ErrPolicy for a member of two groups",
				give: "witness a " + w1 + "\ngroup g 1 a\ngroup h 1 a\n", line: "on line 3",
			},
			{
				name: "returns ErrPolicy for a member listed twice in a group",
				give: "witness a " + w1 + "\ngroup g 1" + strings.Repeat(" a", 2) + "\n", line: "on line 2",
			},
			{
				name: "returns ErrPolicy for the threshold 0",
				give: "witness a " + w1 + "\ngroup g 0 a\n",
				line: "on line 2",
			},
			{
				name: "returns ErrPolicy for a threshold above the members",
				give: "witness a " + w1 + "\ngroup g 2 a\n",
				line: "on line 2",
			},
			{
				name: "returns ErrPolicy for a threshold that is not a number",
				give: "witness a " + w1 + "\ngroup g x a\n",
				line: "on line 2",
			},
			{
				name: "returns ErrPolicy for a threshold with a plus sign",
				give: "witness a " + w1 + "\ngroup g +1 a\n",
				line: "on line 2",
			},
			{
				name: "returns ErrPolicy for a negative threshold",
				give: "witness a " + w1 + "\ngroup g -1 a\n",
				line: "on line 2",
			},
			{name: "returns ErrPolicy for a quorum that no line defines", give: "quorum a\n", line: "on line 1"},
			{
				name: "returns ErrPolicy for a quorum that a later line defines",
				give: "quorum a\nwitness a " + w1 + "\n",
				line: "on line 1",
			},
			{name: "returns ErrPolicy for a second quorum line", give: "quorum none\nquorum none\n", line: "on line 2"},
			{
				name: "returns ErrPolicy for a log key on two lines",
				give: "log " + lg + "\nlog " + lg + "\n",
				line: "on line 2",
			},
			{
				name: "returns ErrPolicy for a witness key on two lines",
				give: "witness a " + w1 + "\nwitness b " + w1 + "\n",
				line: "on line 2",
			},
			{
				name: "returns ErrPolicy for the public key of a witness on a log line",
				give: "witness a " + w1 + "\nlog " + sameKey.String() + "\n", line: "on line 2",
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				got, err := checkpoint.ParsePolicy([]byte(tt.give))
				testkit.ErrorIs(t, err, checkpoint.ErrPolicy, "ParsePolicy must refuse the policy")
				testkit.Equal(t, errs.Classify(err), errs.Invalid, "the error must classify as Invalid")
				testkit.Contains(t, err.Error(), tt.line, "the error must name the line")
				testkit.Equal(t, got, checkpoint.Policy{}, "ParsePolicy must return the zero Policy with an error")
			})
		}

		quorumTests := []struct {
			name string
			give string
		}{
			{name: "returns ErrPolicy for an empty file", give: ""},
			{name: "returns ErrPolicy for a file without a quorum line", give: "# a comment\nlog " + lg + "\n"},
		}
		for _, tt := range quorumTests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				got, err := checkpoint.ParsePolicy([]byte(tt.give))
				testkit.ErrorIs(t, err, checkpoint.ErrPolicy, "ParsePolicy must refuse a file without a quorum")
				testkit.Equal(t, got, checkpoint.Policy{}, "ParsePolicy must return the zero Policy with an error")
			})
		}
	})

	t.Run("UnmarshalText", func(t *testing.T) {
		t.Parallel()

		w1 := witnessKey(t, "example.com/w1").String()
		w2 := witnessKey(t, "example.com/w2").String()
		lg := logKey(t, exampleLog).String()

		t.Run("sets p to the policy of the example of tlog-policy", func(t *testing.T) {
			t.Parallel()
			var p checkpoint.Policy
			testkit.NoError(t, p.UnmarshalText([]byte(examplePolicyText(t))), "UnmarshalText must accept the example")
			testkit.Equal(t, &p, examplePolicy(t), "UnmarshalText must set every line of the example")
		})

		t.Run("reuses the memory of p for the text of the policy that p holds", func(t *testing.T) {
			t.Parallel()
			text := []byte(examplePolicyText(t))
			var p checkpoint.Policy
			testkit.NoError(t, p.UnmarshalText(text), "UnmarshalText must accept the example")
			log, witness, group := &p.Logs[0], &p.Witnesses[0], &p.Groups[0]
			logKey, key := &p.Logs[0].Key.PublicKey[0], &p.Witnesses[5].Key.PublicKey[0]
			member := &p.Groups[2].Members[1]
			testkit.NoError(t, p.UnmarshalText(text), "UnmarshalText must accept the example again")
			testkit.True(t, &p.Logs[0] == log, "UnmarshalText must reuse the slice of logs")
			testkit.True(t, &p.Witnesses[0] == witness, "UnmarshalText must reuse the slice of witnesses")
			testkit.True(t, &p.Groups[0] == group, "UnmarshalText must reuse the slice of groups")
			testkit.True(t, &p.Logs[0].Key.PublicKey[0] == logKey, "UnmarshalText must reuse the public key of a log")
			testkit.True(t, &p.Witnesses[5].Key.PublicKey[0] == key, "UnmarshalText must reuse each public key")
			testkit.True(t, &p.Groups[2].Members[1] == member, "UnmarshalText must reuse the members of each group")
			testkit.Equal(t, &p, examplePolicy(t), "UnmarshalText must set every line of the example")
		})

		t.Run("reuses the memory of p for a text of the first lines of the policy that p holds", func(t *testing.T) {
			t.Parallel()
			var p checkpoint.Policy
			text := "log " + lg + " https://l.example/\nwitness a " + w1 + "\nwitness b " + w2 + "\nquorum a\n"
			testkit.NoError(t, p.UnmarshalText([]byte(text)), "UnmarshalText must accept the policy")
			key := &p.Witnesses[0].Key.PublicKey[0]
			first := "log " + lg + " https://l.example/\nwitness a " + w1 + "\nquorum a\n"
			testkit.NoError(t, p.UnmarshalText([]byte(first)), "UnmarshalText must accept the first lines")
			testkit.True(t, &p.Witnesses[0].Key.PublicKey[0] == key, "UnmarshalText must reuse the public key of a")
			equalPolicy(t, &p, mustParsePolicy(t, []byte(first)))
		})

		t.Run("sets p to a policy that differs from the policy of p in one line", func(t *testing.T) {
			t.Parallel()
			var p checkpoint.Policy
			testkit.NoError(t, p.UnmarshalText([]byte(examplePolicyText(t))), "UnmarshalText must accept the example")
			example := examplePolicyText(t)
			y3 := "witness Y3 " + witnessKey(t, yWitnesses["Y3"]).String()
			for _, text := range []string{
				strings.Replace(example, "group X-witnesses 2", "group X-witnesses 3", 1),
				strings.Replace(example, "group Y-witnesses any Y1 Y2 Y3", "group Y-witnesses any Y1 Y3 Y2", 1),
				strings.Replace(example, "group Y-witnesses any Y1 Y2 Y3", "group Y-witnesses any Y1 Y2", 1),
				strings.ReplaceAll(example, "Y-witnesses", "Y-group"),
				strings.Replace(strings.Replace(example, "witness Y3 ", "witness Y4 ", 1), " Y2 Y3\n", " Y2 Y4\n", 1),
				strings.Replace(example, "\n\n", "\nwitness Z "+w1+"\n", 1),
				strings.Replace(example, y3, y3+" https://y3.example/", 1),
				strings.Replace(example, "quorum X-and-Y", "quorum X-witnesses", 1),
				strings.Replace(example, "log "+lg, "log "+lg+" https://l.example/", 1),
				strings.Replace(example, "log "+lg, "log "+logKey(t, "example.com/other").String(), 1),
			} {
				testkit.NoError(t, p.UnmarshalText([]byte(text)), "UnmarshalText must accept "+text)
				equalPolicy(t, &p, mustParsePolicy(t, []byte(text)))
				testkit.NoError(t, p.UnmarshalText([]byte(example)), "UnmarshalText must accept the example")
				equalPolicy(t, &p, examplePolicy(t))
			}
		})

		t.Run("sets p to a policy whose group has more members than the group of p", func(t *testing.T) {
			t.Parallel()
			var p checkpoint.Policy
			text := "witness a " + w1 + "\nwitness b " + w2 + "\ngroup g 1 a\nquorum g\n"
			testkit.NoError(t, p.UnmarshalText([]byte(text)), "UnmarshalText must accept the policy")
			more := "witness a " + w1 + "\nwitness b " + w2 + "\ngroup g 1 a b\nquorum g\n"
			testkit.NoError(t, p.UnmarshalText([]byte(more)), "UnmarshalText must accept the group of two members")
			testkit.Equal(t, p.Groups[0].Members, []checkpoint.PolicyName{"a", "b"},
				"UnmarshalText must set both members")
		})

		t.Run("sets p to a policy with a key at a position where p has another key", func(t *testing.T) {
			t.Parallel()
			var p checkpoint.Policy
			text := "witness a " + w1 + "\nquorum a\n"
			testkit.NoError(t, p.UnmarshalText([]byte(text)), "UnmarshalText must accept the policy")
			other := "witness a " + w2 + "\nquorum a\n"
			testkit.NoError(t, p.UnmarshalText([]byte(other)), "UnmarshalText must accept the other key")
			testkit.Equal(t, p.Witnesses[0].Key, witnessKey(t, "example.com/w2"), "UnmarshalText must set the key")
		})

		t.Run("sets p to each policy in turn", func(t *testing.T) {
			t.Parallel()
			texts := [][]byte{
				[]byte(examplePolicyText(t)),
				readFile(t, ageKeyserverFile),
				readFile(t, policyFile),
				[]byte("log " + logKey(t, exampleLog).String() + " https://l.example/\nquorum none\n"),
				[]byte(oneGroupPolicyText(t)),
				[]byte(examplePolicyText(t)),
			}
			var p checkpoint.Policy
			for _, text := range texts {
				testkit.NoError(t, p.UnmarshalText(text), "UnmarshalText must accept the policy")
				equalPolicy(t, &p, mustParsePolicy(t, text))
			}
		})

		emptied := []struct {
			name string
			give string
		}{
			{name: "empties p for a second quorum line", give: examplePolicyText(t) + "quorum none\n"},
			{
				name: "empties p for a text whose lines repeat p up to a line that breaks a rule",
				give: examplePolicyText(t) + "witness X1 " + w1 + "\n",
			},
		}
		for _, tt := range emptied {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				p := examplePolicy(t)
				err := p.UnmarshalText([]byte(tt.give))
				testkit.ErrorIs(t, err, checkpoint.ErrPolicy, "UnmarshalText must refuse the text")
				testkit.Equal(t, errs.Classify(err), errs.Invalid, "the error must classify as Invalid")
				testkit.Equal(t, p.Quorum, checkpoint.PolicyName(""), "UnmarshalText must remove the quorum")
				testkit.Len(t, p.Logs, 0, "UnmarshalText must remove the logs")
				testkit.Len(t, p.Witnesses, 0, "UnmarshalText must remove the witnesses")
				testkit.Len(t, p.Groups, 0, "UnmarshalText must remove the groups")
			})
		}

		t.Run("parses into the memory of the policy that an error emptied", func(t *testing.T) {
			t.Parallel()
			text := []byte(examplePolicyText(t))
			var p checkpoint.Policy
			testkit.NoError(t, p.UnmarshalText(text), "UnmarshalText must accept the example")
			witness := &p.Witnesses[0]
			testkit.ErrorIs(t, p.UnmarshalText([]byte("quorum a\n")), checkpoint.ErrPolicy,
				"UnmarshalText must refuse an undefined quorum")
			testkit.NoError(t, p.UnmarshalText(text), "UnmarshalText must accept the example again")
			testkit.True(t, &p.Witnesses[0] == witness, "UnmarshalText must reuse the slice of witnesses")
			testkit.Equal(t, &p, examplePolicy(t), "UnmarshalText must set every line of the example")
		})
	})

	t.Run("Valid", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			give *checkpoint.Policy
			name string
			want bool
		}{
			{name: "reports true for the example of tlog-policy", give: examplePolicy(t), want: true},
			{
				name: "reports true for the policy of the age-keyserver",
				give: mustParsePolicy(t, readFile(t, ageKeyserverFile)),
				want: true,
			},
			{
				name: "reports true for the policy of the vectors",
				give: mustParsePolicy(t, readFile(t, policyFile)),
				want: true,
			},
			{name: "reports false for the zero Policy", give: &checkpoint.Policy{}, want: false},
			{name: "reports false for a log that is not Valid", give: changed(t, func(p *checkpoint.Policy) {
				p.Logs = append(p.Logs, checkpoint.Log{})
			}), want: false},
			{name: "reports false for a witness that is not Valid", give: changed(t, func(p *checkpoint.Policy) {
				p.Witnesses[0].Name = checkpoint.QuorumNone
			}), want: false},
			{name: "reports false for a group that is not Valid", give: changed(t, func(p *checkpoint.Policy) {
				p.Groups[0].Threshold = 0
			}), want: false},
			{name: "reports false for a witness name defined twice", give: changed(t, func(p *checkpoint.Policy) {
				p.Witnesses[1].Name = p.Witnesses[0].Name
			}), want: false},
			{name: "reports false for a group with the name of a witness", give: changed(t, func(p *checkpoint.Policy) {
				p.Groups[2].Name = "X1"
			}), want: false},
			{name: "reports false for a group that lists a later group", give: changed(t, func(p *checkpoint.Policy) {
				p.Groups[0], p.Groups[2] = p.Groups[2], p.Groups[0]
			}), want: false},
			{name: "reports false for a name that two groups list", give: changed(t, func(p *checkpoint.Policy) {
				p.Groups[1].Members = append(p.Groups[1].Members, "X1")
			}), want: false},
			{name: "reports false for a public key of two witnesses", give: changed(t, func(p *checkpoint.Policy) {
				p.Witnesses[1].Key = p.Witnesses[0].Key
			}), want: false},
			{name: "reports false for a quorum that no line defines", give: changed(t, func(p *checkpoint.Policy) {
				p.Quorum = "Z"
			}), want: false},
			{name: "reports true for the quorum none", give: changed(t, func(p *checkpoint.Policy) {
				p.Quorum = checkpoint.QuorumNone
			}), want: true},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				testkit.Equal(t, tt.give.Valid(), tt.want, "Valid must report whether the policy keeps every rule")
			})
		}
	})

	t.Run("QuorumRule", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a rule that two X witnesses and one Y witness satisfy", func(t *testing.T) {
			t.Parallel()
			tree := quorumTree(t, examplePolicy(t), resolver())
			testkit.NoError(t, cosign(t, cosignedText, "X1", "X3", "Y2").Check(tree), "two X and one Y must satisfy it")
		})

		t.Run("returns a rule that three Y witnesses and one X witness do not satisfy", func(t *testing.T) {
			t.Parallel()
			tree := quorumTree(t, examplePolicy(t), resolver())
			err := cosign(t, cosignedText, "Y1", "Y2", "Y3", "X1").Check(tree)
			testkit.ErrorIs(t, err, sign.ErrThreshold, "one X witness must not satisfy the group of X")
		})

		t.Run("returns a rule of a group of more than 32 members", func(t *testing.T) {
			t.Parallel()
			var s strings.Builder
			for i := range 40 {
				name := "w" + strconv.Itoa(i)
				s.WriteString("witness " + name + " " + witnessKey(t, note.Name(name)).String() + "\n")
			}
			s.WriteString("group g 40")
			for i := range 40 {
				s.WriteString(" w" + strconv.Itoa(i))
			}
			s.WriteString("\nquorum g\n")
			tree := quorumTree(t, mustParsePolicy(t, []byte(s.String())), resolver())
			signers := make([]note.Signer, 0, 40)
			for i := range 40 {
				signers = append(signers, witness(t, note.Name("w"+strconv.Itoa(i))))
			}
			n, err := note.Sign(t.Context(), []byte(cosignedText), signers...)
			testkit.NoError(t, err, "note.Sign must cosign the text")
			testkit.NoError(t, n.Check(tree), "the 40 witnesses must satisfy the group")
			n.Signatures = n.Signatures[1:]
			testkit.ErrorIs(t, n.Check(tree), sign.ErrThreshold, "39 witnesses must not satisfy the group")
		})

		t.Run("returns a rule that an AllOf rule of two log keys combines with", func(t *testing.T) {
			t.Parallel()
			r := resolver()
			r[hybridType(t)] = note.Text(mldsa.Resolver(mldsa.MLDSA44, ""))
			var (
				rules sign.Rules
				keys  note.Keyring
			)
			keys.Reset(r)
			q, err := examplePolicy(t).QuorumRule(&rules, &keys)
			testkit.NoError(t, err, "QuorumRule must build the quorum of the example")
			ec, pq := logSigner(t, exampleLog), hybridSigner(t, exampleLog)
			ecV, err := r.Verifier(ec.Key())
			testkit.NoError(t, err, "the resolver must build the Verifier of the Ed25519 key")
			pqV, err := r.Verifier(pq.Key())
			testkit.NoError(t, err, "the resolver must build the Verifier of the ML-DSA key")
			tree, err := sign.NewPolicyTree(sign.AtLeast("note", 2, sign.AllOf(exampleLog, pqV, ecV), q))
			testkit.NoError(t, err, "NewPolicyTree must accept the hybrid log and the quorum")

			quorum := []note.Signer{
				witness(t, xWitnesses["X1"]),
				witness(t, xWitnesses["X2"]),
				witness(t, yWitnesses["Y1"]),
			}
			both, err := note.Sign(t.Context(), []byte(cosignedText), append([]note.Signer{ec, pq}, quorum...)...)
			testkit.NoError(t, err, "note.Sign must sign the checkpoint")
			testkit.NoError(t, both.Check(tree), "both log keys and the quorum must satisfy the tree")

			one, err := note.Sign(t.Context(), []byte(cosignedText), append([]note.Signer{ec}, quorum...)...)
			testkit.NoError(t, err, "note.Sign must sign the checkpoint")
			testkit.ErrorIs(t, one.Check(tree), sign.ErrThreshold, "one of the two log keys must not satisfy the tree")
		})

		t.Run("returns ErrPolicy for the quorum none", func(t *testing.T) {
			t.Parallel()
			_, err := quorumRule(mustParsePolicy(t, []byte("quorum none\n")), resolver())
			testkit.ErrorIs(t, err, checkpoint.ErrPolicy, "QuorumRule must refuse the quorum none")
			testkit.Equal(t, errs.Classify(err), errs.Invalid, "the error must classify as Invalid")
		})

		t.Run("returns ErrPolicy for a Policy that is not Valid", func(t *testing.T) {
			t.Parallel()
			_, err := quorumRule(&checkpoint.Policy{}, resolver())
			testkit.ErrorIs(t, err, checkpoint.ErrPolicy, "QuorumRule must refuse the zero Policy")
		})

		t.Run("returns the error of the Keyring", func(t *testing.T) {
			t.Parallel()
			_, err := quorumRule(examplePolicy(t), note.Resolver{note.TypeEd25519: note.Text(ed25519.Resolve)})
			testkit.ErrorIs(t, err, note.ErrUnknownType, "QuorumRule must return the error of the Keyring")
		})

		t.Run("returns the error of the Keyring for a witness of a nested group", func(t *testing.T) {
			t.Parallel()
			r := resolver()
			p := examplePolicy(t)
			p.Witnesses[5].Key = hybridKey(t, "example.com/y3")
			_, err := quorumRule(p, r)
			testkit.ErrorIs(t, err, note.ErrUnknownType, "QuorumRule must return the error for the key of Y3")
		})
	})
}

func BenchmarkPolicy(b *testing.B) {
	text := []byte(examplePolicyText(b))
	p := examplePolicy(b)
	r := resolver()
	name := checkpoint.PolicyName("X-witnesses")
	w := p.Witnesses[0]
	g := p.Groups[0]
	l := p.Logs[0]
	invalid := changed(b, func(p *checkpoint.Policy) { p.Quorum = "Z" })

	b.Run("PolicyName.Valid", func(b *testing.B) {
		benchZeroAlloc(b, func() { sinkBool = name.Valid() })
	})

	b.Run("Log.Valid", func(b *testing.B) {
		benchZeroAlloc(b, func() { sinkBool = l.Valid() })
	})

	b.Run("Witness.Valid", func(b *testing.B) {
		benchZeroAlloc(b, func() { sinkBool = w.Valid() })
	})

	b.Run("Group.Valid", func(b *testing.B) {
		benchZeroAlloc(b, func() { sinkBool = g.Valid() })
	})

	b.Run("ParsePolicy", func(b *testing.B) {
		benchAllocs(b, parsePolicyAllocs, func() { sinkPolicy, errSink = checkpoint.ParsePolicy(text) })
	})

	b.Run("ParsePolicy of keys of one type without an assigned byte", func(b *testing.B) {
		pq := []byte("witness a " + hybridKey(b, "example.com/a").String() + "\nwitness b " +
			hybridKey(b, "example.com/b").String() + "\nquorum a\n")
		benchAllocs(b, 4, func() { sinkPolicy, errSink = checkpoint.ParsePolicy(pq) })
	})

	b.Run("UnmarshalText", func(b *testing.B) {
		var reused checkpoint.Policy
		testkit.NoError(b, reused.UnmarshalText(text), "UnmarshalText must accept the example")
		benchZeroAlloc(b, func() { errSink = reused.UnmarshalText(text) })
	})

	b.Run("Valid", func(b *testing.B) {
		benchZeroAlloc(b, func() { sinkBool = p.Valid() })
	})

	b.Run("Valid of a Policy that is not Valid", func(b *testing.B) {
		benchZeroAlloc(b, func() { sinkBool = invalid.Valid() })
	})

	b.Run("QuorumRule", func(b *testing.B) {
		var (
			rules sign.Rules
			keys  note.Keyring
		)

		keys.Reset(r)
		_, err := p.QuorumRule(&rules, &keys)
		testkit.NoError(b, err, "QuorumRule must build the quorum of the example")
		benchZeroAlloc(b, func() {
			rules.Reset()
			keys.Reset(r)
			sinkRule, errSink = p.QuorumRule(&rules, &keys)
		})
	})
}

// quorumRule returns the quorum of p as QuorumRule returns it with a new
// Rules and a Keyring of r.
func quorumRule(p *checkpoint.Policy, r note.Resolver) (sign.Rule, error) {
	var (
		rules sign.Rules
		keys  note.Keyring
	)

	keys.Reset(r)

	return p.QuorumRule(&rules, &keys)
}

// quorumTree returns the sign.Policy of the quorum of p with the
// resolver r, and fails the test when QuorumRule or NewPolicyTree
// refuses it.
func quorumTree(tb testing.TB, p *checkpoint.Policy, r note.Resolver) sign.Policy {
	tb.Helper()

	q, err := quorumRule(p, r)
	testkit.NoError(tb, err, "QuorumRule must build the quorum")

	tree, err := sign.NewPolicyTree(q)
	testkit.NoError(tb, err, "NewPolicyTree must accept the quorum")

	return tree
}

// equalPolicy fails the test when got and want differ in a line. It
// compares the slices by their elements, so that an empty slice with
// capacity equals a nil one.
func equalPolicy(tb testing.TB, got, want *checkpoint.Policy) {
	tb.Helper()

	testkit.Equal(tb, got.Quorum, want.Quorum, "the quorums must be equal")
	testkit.Equal(tb, len(got.Logs), len(want.Logs), "the policies must have as many logs")
	testkit.Equal(tb, len(got.Witnesses), len(want.Witnesses), "the policies must have as many witnesses")
	testkit.Equal(tb, len(got.Groups), len(want.Groups), "the policies must have as many groups")

	for i := range want.Logs {
		testkit.Equal(tb, got.Logs[i], want.Logs[i], "the logs must be equal")
	}

	for i := range want.Witnesses {
		testkit.Equal(tb, got.Witnesses[i], want.Witnesses[i], "the witnesses must be equal")
	}

	for i := range want.Groups {
		testkit.Equal(tb, got.Groups[i], want.Groups[i], "the groups must be equal")
	}
}

// edKey returns the key of type typ named name, over the Ed25519 public key
// of ed25519Signer for name.
func edKey(tb testing.TB, name note.Name, typ note.Type) note.Key {
	tb.Helper()

	return note.Key{Name: name, Type: typ, PublicKey: ed25519Signer(tb, string(name)).PublicKey()}
}

// logKey returns the key of type 0x01 of the log named name.
func logKey(tb testing.TB, name note.Name) note.Key {
	tb.Helper()

	return edKey(tb, name, note.TypeEd25519)
}

// witnessKey returns the key of type 0x04 of the witness named name.
func witnessKey(tb testing.TB, name note.Name) note.Key {
	tb.Helper()

	return edKey(tb, name, checkpoint.TypeEd25519Cosignature)
}

// logSigner returns the note Signer of type 0x01 of the log named name.
func logSigner(tb testing.TB, name note.Name) note.Signer {
	tb.Helper()

	s, err := note.NewTextSigner(name, note.TypeEd25519, ed25519Signer(tb, string(name)))
	testkit.NoError(tb, err, "NewTextSigner must accept the Ed25519 signer")

	return s
}

// hybridType returns the type of the ML-DSA-44 key of a hybrid log.
func hybridType(tb testing.TB) note.Type {
	tb.Helper()

	typ, err := note.NewType("example.com/ml-dsa-44")
	testkit.NoError(tb, err, "NewType must accept the identifier")

	return typ
}

// hybridKey returns the key of hybridType named name, over the ML-DSA-44
// public key of hybridSigner.
func hybridKey(tb testing.TB, name note.Name) note.Key {
	tb.Helper()

	return hybridSigner(tb, name).Key()
}

// hybridSigner returns the note Signer of the ML-DSA-44 key of the hybrid
// log named name.
func hybridSigner(tb testing.TB, name note.Name) note.Signer {
	tb.Helper()

	s, err := note.NewTextSigner(name, hybridType(tb), mldsaSigner(tb, mldsa.MLDSA44, string(name), ""))
	testkit.NoError(tb, err, "NewTextSigner must accept the ML-DSA-44 signer")

	return s
}

// examplePolicyText returns the example of tlog-policy, with the keys of
// logKey and witnessKey.
func examplePolicyText(tb testing.TB) string {
	tb.Helper()

	var s strings.Builder

	s.WriteString("log " + logKey(tb, exampleLog).String() + "\n\n")

	for _, n := range []checkpoint.PolicyName{"X1", "X2", "X3"} {
		s.WriteString("witness " + string(n) + " " + witnessKey(tb, xWitnesses[n]).String() + "\n")
	}

	s.WriteString("group X-witnesses 2 X1 X2 X3\n\n")

	for _, n := range []checkpoint.PolicyName{"Y1", "Y2", "Y3"} {
		s.WriteString("witness " + string(n) + " " + witnessKey(tb, yWitnesses[n]).String() + "\n")
	}

	s.WriteString("group Y-witnesses any Y1 Y2 Y3\n\n")
	s.WriteString("group X-and-Y all X-witnesses Y-witnesses\n")
	s.WriteString("quorum X-and-Y\n")

	return s.String()
}

// examplePolicy returns the Policy of examplePolicyText.
func examplePolicy(tb testing.TB) *checkpoint.Policy {
	tb.Helper()

	p := &checkpoint.Policy{Quorum: "X-and-Y", Logs: []checkpoint.Log{{Key: logKey(tb, exampleLog)}}}

	for _, n := range []checkpoint.PolicyName{"X1", "X2", "X3", "Y1", "Y2", "Y3"} {
		key := xWitnesses[n]
		if key == "" {
			key = yWitnesses[n]
		}

		p.Witnesses = append(p.Witnesses, checkpoint.Witness{Name: n, Key: witnessKey(tb, key)})
	}

	p.Groups = []checkpoint.Group{
		{Name: "X-witnesses", Members: []checkpoint.PolicyName{"X1", "X2", "X3"}, Threshold: 2},
		{Name: "Y-witnesses", Members: []checkpoint.PolicyName{"Y1", "Y2", "Y3"}, Threshold: 1},
		{Name: "X-and-Y", Members: []checkpoint.PolicyName{"X-witnesses", "Y-witnesses"}, Threshold: 2},
	}

	return p
}

// changed returns examplePolicy after change.
func changed(tb testing.TB, change func(*checkpoint.Policy)) *checkpoint.Policy {
	tb.Helper()

	p := examplePolicy(tb)
	change(p)

	return p
}

// mustParsePolicy returns the policy of text, and fails the test when
// ParsePolicy refuses it.
func mustParsePolicy(tb testing.TB, text []byte) *checkpoint.Policy {
	tb.Helper()

	p, err := checkpoint.ParsePolicy(text)
	testkit.NoError(tb, err, "ParsePolicy must accept the policy")

	return &p
}

// cosign returns the note of text with one cosignature from each witness of
// the example of tlog-policy that names lists.
func cosign(tb testing.TB, text string, names ...checkpoint.PolicyName) *note.Note {
	tb.Helper()

	signers := make([]note.Signer, len(names))
	for i, n := range names {
		key := xWitnesses[n]
		if key == "" {
			key = yWitnesses[n]
		}

		signers[i] = witness(tb, key)
	}

	n, err := note.Sign(tb.Context(), []byte(text), signers...)
	testkit.NoError(tb, err, "note.Sign must cosign the text")

	return &n
}
