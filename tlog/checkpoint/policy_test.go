// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package checkpoint_test

import (
	"maps"
	"math/bits"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/crypto/sign"
	"go.thesmos.sh/core/crypto/sign/ed25519"
	"go.thesmos.sh/core/crypto/sign/mldsa"
	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/note"
	"go.thesmos.sh/core/tlog/checkpoint"
)

// The fixture values of the cases of Policy.
const (
	// ageKeyserverFile is the witness policy of filippo.io/torchwood
	// v0.10.0, cmd/age-keyserver/witness_policy.txt.
	ageKeyserverFile = "age-keyserver-policy.txt"

	// exampleLog is the key name of the log of the policies of the tests.
	exampleLog = "example.com/log"

	// parsePolicyAllocs is the number of allocations of ParsePolicy for the
	// example of tlog-policy, with six witnesses and three groups, and with
	// one log or two: the string of the file, the slices of logs, witnesses
	// and groups, the members, and the public keys.
	parsePolicyAllocs = 6

	// changedPolicyAllocs is the number of allocations of UnmarshalText of a
	// policy that differs from the Policy in one line, into a Policy with
	// room for its lines: the string of the file, the members, and the
	// public keys.
	changedPolicyAllocs = 3
)

// exampleWitnesses are the witnesses of the example of tlog-policy, by
// their name in the policy, with their key names.
var exampleWitnesses = map[checkpoint.PolicyName]note.Name{
	"X1": "example.com/x1", "X2": "example.com/x2", "X3": "example.com/x3",
	"Y1": "example.com/y1", "Y2": "example.com/y2", "Y3": "example.com/y3",
}

// The witnesses of the two groups of the example of tlog-policy, in the
// order of the file.
var (
	xNames = []checkpoint.PolicyName{"X1", "X2", "X3"}
	yNames = []checkpoint.PolicyName{"Y1", "Y2", "Y3"}
)

func TestPolicy(t *testing.T) {
	t.Parallel()

	w1 := witness(t, "example.com/w1").Key().String()
	w2 := witness(t, "example.com/w2").Key().String()
	lg := logSigner(t, exampleLog).Key().String()

	t.Run("PolicyName", func(t *testing.T) {
		t.Parallel()

		t.Run("Valid", func(t *testing.T) {
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
					assert.Equal(t, tt.give.Valid(), tt.want,
						"Valid must report whether a policy file can contain the name")
				})
			}
		})
	})

	t.Run("Log", func(t *testing.T) {
		t.Parallel()

		t.Run("Valid", func(t *testing.T) {
			t.Parallel()

			t.Run("reports true for a log with a Valid key", func(t *testing.T) {
				t.Parallel()
				assert.True(t, checkpoint.Log{Key: logSigner(t, exampleLog).Key()}.Valid(), "Valid must accept the log")
			})

			t.Run("reports false for the zero Log", func(t *testing.T) {
				t.Parallel()
				assert.False(t, checkpoint.Log{}.Valid(), "Valid must refuse a log without a key")
			})
		})
	})

	t.Run("Witness", func(t *testing.T) {
		t.Parallel()

		t.Run("Valid", func(t *testing.T) {
			t.Parallel()

			key := witness(t, "example.com/x1").Key()

			tests := []struct {
				name string
				give checkpoint.Witness
				want bool
			}{
				{
					name: "reports true for a witness with a name and a key",
					give: checkpoint.Witness{Name: "X1", Key: key}, want: true,
				},
				{
					name: "reports false for the name none",
					give: checkpoint.Witness{Name: checkpoint.QuorumNone, Key: key}, want: false,
				},
				{
					name: "reports false for an invalid name",
					give: checkpoint.Witness{Name: "X 1", Key: key}, want: false,
				},
				{name: "reports false for a key that is not Valid", give: checkpoint.Witness{Name: "X1"}, want: false},
			}
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					t.Parallel()
					assert.Equal(t, tt.give.Valid(), tt.want, "Valid must report whether the witness is complete")
				})
			}
		})
	})

	t.Run("Group", func(t *testing.T) {
		t.Parallel()

		t.Run("Valid", func(t *testing.T) {
			t.Parallel()

			members := []checkpoint.PolicyName{"X1", "X2", "X3"}

			tests := []struct {
				name string
				give checkpoint.Group
				want bool
			}{
				{
					name: "reports true for 2 of 3 members",
					give: checkpoint.Group{Name: "X", Members: members, Threshold: 2}, want: true,
				},
				{
					name: "reports true for the threshold 1",
					give: checkpoint.Group{Name: "X", Members: members, Threshold: 1}, want: true,
				},
				{
					name: "reports true for a threshold of every member",
					give: checkpoint.Group{Name: "X", Members: members, Threshold: 3}, want: true,
				},
				{
					name: "reports false for the threshold 0",
					give: checkpoint.Group{Name: "X", Members: members, Threshold: 0}, want: false,
				},
				{
					name: "reports false for a threshold above the members",
					give: checkpoint.Group{Name: "X", Members: members, Threshold: 4}, want: false,
				},
				{
					name: "reports false for a group without members",
					give: checkpoint.Group{Name: "X", Threshold: 1}, want: false,
				},
				{
					name: "reports false for the name none",
					give: checkpoint.Group{Name: checkpoint.QuorumNone, Members: members, Threshold: 1}, want: false,
				},
				{
					name: "reports false for an invalid name",
					give: checkpoint.Group{Name: "X Y", Members: members, Threshold: 1}, want: false,
				},
				{
					name: "reports false for the member none",
					give: checkpoint.Group{
						Name: "X", Members: []checkpoint.PolicyName{"X1", checkpoint.QuorumNone}, Threshold: 1,
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
					assert.Equal(t, tt.give.Valid(), tt.want, "Valid must report whether the group is complete")
				})
			}
		})
	})

	t.Run("ParsePolicy", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the policy of the example of tlog-policy", func(t *testing.T) {
			t.Parallel()
			got, err := checkpoint.ParsePolicy([]byte(examplePolicyText(t)))
			assert.NoError(t, err, "ParsePolicy must accept the example")
			assert.Equal(t, got, *examplePolicy(t), "ParsePolicy must return every line of the example")
		})

		t.Run("returns the keys of a type without an assigned byte", func(t *testing.T) {
			t.Parallel()
			a, b := hybridSigner(t, "example.com/a").Key(), hybridSigner(t, "example.com/b").Key()
			got, err := checkpoint.ParsePolicy([]byte("log " + lg + "\nwitness a " + a.String() + "\nwitness b " +
				b.String() + "\nquorum a\n"))
			assert.NoError(t, err, "ParsePolicy must accept the keys")
			assert.Length(t, got.Witnesses, 2, "ParsePolicy must return both witnesses")
			expect.Equal(t, got.Witnesses[0].Key, a, "ParsePolicy must return the key of a")
			expect.Equal(t, got.Witnesses[1].Key, b, "ParsePolicy must return the key of b")
		})

		t.Run("returns the key of a type of one byte after a key of a type without an assigned byte",
			func(t *testing.T) {
				t.Parallel()
				text := "witness a " + hybridSigner(t, "example.com/a").Key().String() + "\nwitness b " + w2 +
					"\nquorum a\n"
				got, err := checkpoint.ParsePolicy([]byte(text))
				assert.NoError(t, err, "ParsePolicy must accept the keys")
				assert.Length(t, got.Witnesses, 2, "ParsePolicy must return both witnesses")
				assert.Equal(t, got.Witnesses[1].Key, witness(t, "example.com/w2").Key(),
					"ParsePolicy must return the key of b")
			})

		t.Run("returns the policy of the age-keyserver of torchwood v0.10.0", func(t *testing.T) {
			t.Parallel()
			got, err := checkpoint.ParsePolicy(readFile(t, ageKeyserverFile))
			assert.NoError(t, err, "ParsePolicy must accept the policy of torchwood")
			assert.Length(t, got.Logs, 1, "the policy has one log")
			assert.Length(t, got.Witnesses, 3, "the policy has three witnesses")
			expect.Equal(t, got.Logs[0].Key.Name, note.Name("keyserver.geomys.org"), "the log must be the keyserver")
			expect.Equal(t, got.Witnesses[1].Name, checkpoint.PolicyName("Mullvad"),
				"the second witness must be Mullvad")
			expect.Equal(t, got.Witnesses[1].URL, "https://witness.stagemole.eu/", "a witness must keep its URL")
			expect.Equal(t, got.Groups, []checkpoint.Group{{
				Name: "public", Members: []checkpoint.PolicyName{"TrustFabric", "Mullvad", "Geomys"}, Threshold: 2,
			}}, "the policy must have the group public")
			expect.Equal(t, got.Quorum, checkpoint.PolicyName("public"), "the quorum must be the group public")
		})

		t.Run("returns the policy of the vectors that torchwood v0.10.0 verified", func(t *testing.T) {
			t.Parallel()
			got, err := checkpoint.ParsePolicy(readFile(t, policyFile))
			assert.NoError(t, err, "ParsePolicy must accept the policy of the vectors")
			assert.Length(t, got.Witnesses, 4, "the policy has four witnesses")
			expect.Equal(t, got.Witnesses[3].Key.Type, checkpoint.TypeMLDSA44Cosignature, "PQ must have type 0x06")
			expect.Equal(t, got.Quorum, checkpoint.PolicyName("hybrid"), "the quorum must be the group hybrid")
		})

		t.Run("returns the policy of a text with comments, empty lines and whitespace around items",
			func(t *testing.T) {
				t.Parallel()
				got, err := checkpoint.ParsePolicy([]byte("# a comment\n\n \t\n  # an indented comment\n\tlog \t" +
					lg + "  \nquorum none\n"))
				assert.NoError(t, err, "ParsePolicy must accept the policy")
				assert.Equal(t, got, checkpoint.Policy{
					Quorum: checkpoint.QuorumNone, Logs: []checkpoint.Log{{Key: logSigner(t, exampleLog).Key()}},
				}, "ParsePolicy must return the log and the quorum alone")
			})

		t.Run("returns the URL of an item that starts with a number sign", func(t *testing.T) {
			t.Parallel()
			got, err := checkpoint.ParsePolicy([]byte("log " + lg + " #url\nquorum none\n"))
			assert.NoError(t, err, "ParsePolicy must accept the policy")
			assert.Length(t, got.Logs, 1, "ParsePolicy must return the log")
			assert.Equal(t, got.Logs[0].URL, "#url", "a number sign after the first item must start an item")
		})

		t.Run("returns the quorum of a last line without a newline", func(t *testing.T) {
			t.Parallel()
			got, err := checkpoint.ParsePolicy([]byte("log " + lg + "\nquorum none"))
			assert.NoError(t, err, "ParsePolicy must accept the last line without a newline")
			assert.Equal(t, got.Quorum, checkpoint.QuorumNone, "ParsePolicy must read the last line")
		})

		t.Run("returns the URL of a witness", func(t *testing.T) {
			t.Parallel()
			got, err := checkpoint.ParsePolicy([]byte("witness a " + w1 + " https://w.example/\nquorum a\n"))
			assert.NoError(t, err, "ParsePolicy must accept the policy")
			assert.Length(t, got.Witnesses, 1, "ParsePolicy must return the witness")
			assert.Equal(t, got.Witnesses[0].URL, "https://w.example/", "ParsePolicy must return the URL")
		})

		t.Run("returns the witness of a line after the quorum line", func(t *testing.T) {
			t.Parallel()
			got, err := checkpoint.ParsePolicy([]byte("quorum none\nwitness a " + w1 + "\n"))
			assert.NoError(t, err, "ParsePolicy must accept a witness after the quorum")
			assert.Length(t, got.Witnesses, 1, "ParsePolicy must return the witness")
		})

		thresholds := []struct {
			name string
			give string
			want int
		}{
			{name: "returns the threshold 1 for any", give: "any", want: 1},
			{name: "returns the number of members for all", give: "all", want: 2},
			{name: "returns the threshold 1 for the item 1", give: "1", want: 1},
			{name: "returns the number of members for the item of that number", give: "2", want: 2},
		}
		for _, tt := range thresholds {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				got, err := checkpoint.ParsePolicy([]byte("witness a " + w1 + "\nwitness b " + w2 + "\ngroup g " +
					tt.give + " a b\nquorum g\n"))
				assert.NoError(t, err, "ParsePolicy must accept the threshold")
				assert.Length(t, got.Groups, 1, "ParsePolicy must return the group")
				assert.Equal(t, got.Groups[0].Threshold, tt.want, "ParsePolicy must return the threshold")
			})
		}

		malformed := []string{"example.com/log+00000000+AQ==", "b+00000000+BA=="}
		keyErrors := make([]string, len(malformed))
		for i, vkey := range malformed {
			_, err := note.ParseKey(vkey)
			assert.HasError(t, err, "ParseKey must refuse the malformed key "+vkey)
			keyErrors[i] = err.Error()
		}

		refused := []struct {
			name   string
			give   string
			detail string
			line   int
		}{
			{
				name:   "returns ErrPolicy for the octet 0x01",
				give:   "log " + lg + "\x01\nquorum none\n",
				detail: "the octet 0x01, outside 0x09, 0x20 to 0x7E and 0x80 to 0xFF",
				line:   1,
			},
			{
				name:   "returns ErrPolicy for the octet 0x7F",
				give:   "quorum none\nwitness a\x7f " + w1 + "\n",
				detail: "the octet 0x7f, outside 0x09, 0x20 to 0x7E and 0x80 to 0xFF",
				line:   2,
			},
			{
				name:   "returns ErrPolicy for a carriage return",
				give:   "quorum none\r\n",
				detail: "the octet 0x0d, outside 0x09, 0x20 to 0x7E and 0x80 to 0xFF",
				line:   1,
			},
			{
				name:   "returns ErrPolicy for an unknown keyword",
				give:   "\nlogs " + lg + "\n",
				detail: `the unknown keyword "logs"`,
				line:   2,
			},
			{
				name:   "returns ErrPolicy for a log line of 1 item",
				give:   "log\n",
				detail: "a log line has 2 or 3 items",
				line:   1,
			},
			{
				name:   "returns ErrPolicy for a log line of 4 items",
				give:   "log " + lg + " https://l.example/ x\n",
				detail: "a log line has 2 or 3 items",
				line:   1,
			},
			{
				name:   "returns ErrPolicy for a log line with a malformed key",
				give:   "log " + malformed[0] + "\n",
				detail: keyErrors[0],
				line:   1,
			},
			{
				name:   "returns ErrPolicy for a witness line of 2 items",
				give:   "witness a\n",
				detail: "a witness line has 3 or 4 items",
				line:   1,
			},
			{
				name:   "returns ErrPolicy for a witness line of 5 items",
				give:   "witness a " + w1 + " https://w.example/ x\n",
				detail: "a witness line has 3 or 4 items",
				line:   1,
			},
			{
				name:   "returns ErrPolicy for a witness line with a malformed key",
				give:   "witness a " + malformed[1] + "\n",
				detail: keyErrors[1],
				line:   1,
			},
			{
				name:   "returns ErrPolicy for a group line of 3 items",
				give:   "witness a " + w1 + "\ngroup g 1\n",
				detail: "a group line has 4 items or more",
				line:   2,
			},
			{
				name:   "returns ErrPolicy for a quorum line of 1 item",
				give:   "quorum\n",
				detail: "a quorum line has 2 items",
				line:   1,
			},
			{
				name:   "returns ErrPolicy for a quorum line of 3 items",
				give:   "quorum none x\n",
				detail: "a quorum line has 2 items",
				line:   1,
			},
			{
				name:   "returns ErrPolicy for a witness named none",
				give:   "witness none " + w1 + "\n",
				detail: `the witness "none" needs a valid name other than none and a valid key`,
				line:   1,
			},
			{
				name: "returns ErrPolicy for a group named none",
				give: "witness a " + w1 + "\ngroup none 1 a\n",
				detail: `the group "none" needs a valid name other than none, distinct members other than none, ` +
					"and a threshold from 1 to the number of members",
				line: 2,
			},
			{
				name: "returns ErrPolicy for the member none",
				give: "witness a " + w1 + "\ngroup g 1 a none\n",
				detail: `the group "g" needs a valid name other than none, distinct members other than none, ` +
					"and a threshold from 1 to the number of members",
				line: 2,
			},
			{
				name:   "returns ErrPolicy for a witness defined twice",
				give:   "witness a " + w1 + "\nwitness a " + w2 + "\n",
				detail: `"a" is defined twice`,
				line:   2,
			},
			{
				name:   "returns ErrPolicy for a group with the name of a witness",
				give:   "witness a " + w1 + "\ngroup a 1 a\n",
				detail: `"a" is defined twice`,
				line:   2,
			},
			{
				name:   "returns ErrPolicy for a member that no line defines",
				give:   "witness a " + w1 + "\ngroup g 1 b\n",
				detail: `the group "g" lists "b", which no earlier line defines`,
				line:   2,
			},
			{
				name:   "returns ErrPolicy for a member that a later line defines",
				give:   "group g 1 a\nwitness a " + w1 + "\n",
				detail: `the group "g" lists "a", which no earlier line defines`,
				line:   1,
			},
			{
				name:   "returns ErrPolicy for a group that lists itself",
				give:   "witness a " + w1 + "\ngroup g 1 g\n",
				detail: `the group "g" lists "g", which no earlier line defines`,
				line:   2,
			},
			{
				name:   "returns ErrPolicy for a member of two groups",
				give:   "witness a " + w1 + "\ngroup g 1 a\ngroup h 1 a\n",
				detail: `"a" is a member of two groups`,
				line:   3,
			},
			{
				name: "returns ErrPolicy for a member listed twice in a group",
				give: "witness a " + w1 + "\ngroup g 1" + strings.Repeat(" a", 2) + "\n",
				detail: `the group "g" needs a valid name other than none, distinct members other than none, ` +
					"and a threshold from 1 to the number of members",
				line: 2,
			},
			{
				name:   "returns ErrPolicy for the threshold 0",
				give:   "witness a " + w1 + "\ngroup g 0 a\n",
				detail: `the threshold "0" is not any, all, or a number from 1 to 1`,
				line:   2,
			},
			{
				name:   "returns ErrPolicy for a threshold above the members",
				give:   "witness a " + w1 + "\ngroup g 2 a\n",
				detail: `the threshold "2" is not any, all, or a number from 1 to 1`,
				line:   2,
			},
			{
				name:   "returns ErrPolicy for a threshold above the largest uint64",
				give:   "witness a " + w1 + "\ngroup g 18446744073709551616 a\n",
				detail: `the threshold "18446744073709551616" is not any, all, or a number from 1 to 1`,
				line:   2,
			},
			{
				name:   "returns ErrPolicy for a threshold that is not a number",
				give:   "witness a " + w1 + "\ngroup g x a\n",
				detail: `the threshold "x" is not any, all, or a number from 1 to 1`,
				line:   2,
			},
			{
				name:   "returns ErrPolicy for a threshold with a plus sign",
				give:   "witness a " + w1 + "\ngroup g +1 a\n",
				detail: `the threshold "+1" is not any, all, or a number from 1 to 1`,
				line:   2,
			},
			{
				name:   "returns ErrPolicy for a negative threshold",
				give:   "witness a " + w1 + "\ngroup g -1 a\n",
				detail: `the threshold "-1" is not any, all, or a number from 1 to 1`,
				line:   2,
			},
			{
				name:   "returns ErrPolicy for a quorum that no line defines",
				give:   "quorum a\n",
				detail: `the quorum "a" names no witness or group of an earlier line`,
				line:   1,
			},
			{
				name:   "returns ErrPolicy for a quorum that a later line defines",
				give:   "quorum a\nwitness a " + w1 + "\n",
				detail: `the quorum "a" names no witness or group of an earlier line`,
				line:   1,
			},
			{
				name:   "returns ErrPolicy for a second quorum line",
				give:   "quorum none\nquorum none\n",
				detail: "a second quorum line",
				line:   2,
			},
			{
				name:   "returns ErrPolicy for a log key on two lines",
				give:   "log " + lg + "\nlog " + lg + "\n",
				detail: "the public key of example.com/log appears on two lines",
				line:   2,
			},
			{
				name:   "returns ErrPolicy for a witness key on two lines",
				give:   "witness a " + w1 + "\nwitness b " + w1 + "\n",
				detail: "the public key of example.com/w1 appears on two lines",
				line:   2,
			},
			{
				name:   "returns ErrPolicy for the public key of a witness on a log line",
				give:   "witness a " + w1 + "\nlog " + logSigner(t, "example.com/w1").Key().String() + "\n",
				detail: "the public key of example.com/w1 appears on two lines",
				line:   2,
			},
		}
		for _, tt := range refused {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				got, err := checkpoint.ParsePolicy([]byte(tt.give))
				assert.ErrorIs(t, err, checkpoint.ErrPolicy, "ParsePolicy must refuse the policy")
				expect.Equal(t, err.Error(), checkpoint.ErrPolicy.Error()+": "+tt.detail+", on line "+
					strconv.Itoa(tt.line), "the error must state the rule and the line")
				expect.Equal(t, errs.Classify(err), errs.Invalid, "the error must classify as Invalid")
				expect.Equal(t, got, checkpoint.Policy{}, "ParsePolicy must return the zero Policy with an error")
			})
		}

		unfinished := []struct {
			name string
			give string
		}{
			{name: "returns ErrPolicy for an empty file", give: ""},
			{name: "returns ErrPolicy for a file without a quorum line", give: "# a comment\nlog " + lg + "\n"},
		}
		for _, tt := range unfinished {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				got, err := checkpoint.ParsePolicy([]byte(tt.give))
				assert.ErrorIs(t, err, checkpoint.ErrPolicy, "ParsePolicy must refuse a file without a quorum")
				expect.Equal(t, err.Error(), checkpoint.ErrPolicy.Error()+": no quorum line",
					"the error must state the missing quorum line")
				expect.Equal(t, got, checkpoint.Policy{}, "ParsePolicy must return the zero Policy with an error")
			})
		}
	})

	t.Run("UnmarshalText", func(t *testing.T) {
		t.Parallel()

		example := examplePolicyText(t)
		y3 := "witness Y3 " + witness(t, exampleWitnesses["Y3"]).Key().String()

		t.Run("sets p to the policy of the example of tlog-policy", func(t *testing.T) {
			t.Parallel()
			var p checkpoint.Policy
			assert.NoError(t, p.UnmarshalText([]byte(example)), "UnmarshalText must accept the example")
			assert.Equal(t, &p, examplePolicy(t), "UnmarshalText must set every line of the example")
		})

		t.Run("reuses the memory of p for the text of the policy of p", func(t *testing.T) {
			t.Parallel()
			var p checkpoint.Policy
			assert.NoError(t, p.UnmarshalText([]byte(example)), "UnmarshalText must accept the example")
			logs, witnesses, groups := p.Logs, p.Witnesses, p.Groups
			logKey, witnessKey, members := p.Logs[0].Key.PublicKey, p.Witnesses[5].Key.PublicKey, p.Groups[2].Members
			assert.NoError(t, p.UnmarshalText([]byte(example)), "UnmarshalText must accept the example again")
			expect.Equal(t, p.Logs, logs, "UnmarshalText must reuse the slice of logs", expect.ByIdentity())
			expect.Equal(t, p.Witnesses, witnesses, "UnmarshalText must reuse the slice of witnesses",
				expect.ByIdentity())
			expect.Equal(t, p.Groups, groups, "UnmarshalText must reuse the slice of groups", expect.ByIdentity())
			expect.Equal(t, p.Logs[0].Key.PublicKey, logKey, "UnmarshalText must reuse the public key of a log",
				expect.ByIdentity())
			expect.Equal(t, p.Witnesses[5].Key.PublicKey, witnessKey, "UnmarshalText must reuse each public key",
				expect.ByIdentity())
			expect.Equal(t, p.Groups[2].Members, members, "UnmarshalText must reuse the members of each group",
				expect.ByIdentity())
		})

		t.Run("reuses the memory of p for a text of the first lines of the policy of p", func(t *testing.T) {
			t.Parallel()
			var p checkpoint.Policy
			text := "log " + lg + " https://l.example/\nwitness a " + w1 + "\nwitness b " + w2 + "\nquorum a\n"
			assert.NoError(t, p.UnmarshalText([]byte(text)), "UnmarshalText must accept the policy")
			key := p.Witnesses[0].Key.PublicKey
			first := "log " + lg + " https://l.example/\nwitness a " + w1 + "\nquorum a\n"
			assert.NoError(t, p.UnmarshalText([]byte(first)), "UnmarshalText must accept the first lines")
			expect.Equal(t, p.Witnesses[0].Key.PublicKey, key, "UnmarshalText must reuse the public key of a",
				expect.ByIdentity())
			expect.Equal(t, p, *mustParsePolicy(t, []byte(first)), "UnmarshalText must set the first lines",
				expect.EquateEmpty())
		})

		changes := []struct {
			name string
			from string
			give string
		}{
			{
				name: "sets p to a policy with another threshold of a group", from: example,
				give: strings.Replace(example, "group X-witnesses 2", "group X-witnesses 3", 1),
			},
			{
				name: "sets p to a policy with the members of a group in another order", from: example,
				give: strings.Replace(example, "group Y-witnesses any Y1 Y2 Y3", "group Y-witnesses any Y1 Y3 Y2", 1),
			},
			{
				name: "sets p to a policy with a member fewer in a group", from: example,
				give: strings.Replace(example, "group Y-witnesses any Y1 Y2 Y3", "group Y-witnesses any Y1 Y2", 1),
			},
			{
				name: "sets p to a policy with another name of a group", from: example,
				give: strings.ReplaceAll(example, "Y-witnesses", "Y-group"),
			},
			{
				name: "sets p to a policy with another name of a witness", from: example,
				give: strings.Replace(strings.Replace(example, "witness Y3 ", "witness Y4 ", 1),
					" Y2 Y3\n", " Y2 Y4\n", 1),
			},
			{
				name: "sets p to a policy with a witness more", from: example,
				give: strings.Replace(example, "\n\n", "\nwitness Z "+w1+"\n", 1),
			},
			{
				name: "sets p to a policy with the URL of a witness", from: example,
				give: strings.Replace(example, y3, y3+" https://y3.example/", 1),
			},
			{
				name: "sets p to a policy with another quorum", from: example,
				give: strings.Replace(example, "quorum X-and-Y", "quorum X-witnesses", 1),
			},
			{
				name: "sets p to a policy with the URL of the log", from: example,
				give: strings.Replace(example, "log "+lg, "log "+lg+" https://l.example/", 1),
			},
			{
				name: "sets p to a policy with another key of the log", from: example,
				give: strings.Replace(example, "log "+lg, "log "+logSigner(t, "example.com/other").Key().String(), 1),
			},
			{
				name: "sets p to a policy with another name of a witness that no group lists",
				from: "witness a " + w1 + "\nwitness b " + w2 + "\nquorum a\n",
				give: "witness a " + w1 + "\nwitness c " + w2 + "\nquorum a\n",
			},
			{
				name: "sets p to a policy with another name of a group that no group lists",
				from: "witness a " + w1 + "\nwitness b " + w2 + "\ngroup g 1 a\ngroup h 1 b\nquorum g\n",
				give: "witness a " + w1 + "\nwitness b " + w2 + "\ngroup g 1 a\ngroup k 1 b\nquorum g\n",
			},
			{
				name: "sets p to a policy whose group has more members than the group of p",
				from: "witness a " + w1 + "\nwitness b " + w2 + "\ngroup g 1 a\nquorum g\n",
				give: "witness a " + w1 + "\nwitness b " + w2 + "\ngroup g 1 a b\nquorum g\n",
			},
			{
				name: "sets p to a policy with another key at the position of a key of p",
				from: "witness a " + w1 + "\nquorum a\n",
				give: "witness a " + w2 + "\nquorum a\n",
			},
		}
		for _, tt := range changes {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				p := mustParsePolicy(t, []byte(tt.from))
				assert.NoError(t, p.UnmarshalText([]byte(tt.give)), "UnmarshalText must accept the policy")
				assert.Equal(t, *p, *mustParsePolicy(t, []byte(tt.give)), "UnmarshalText must set the changed line",
					assert.EquateEmpty())
			})
		}

		t.Run("sets p to the policy of each text in turn", func(t *testing.T) {
			t.Parallel()
			texts := make([]string, 0, 5+2*len(changes))
			texts = append(texts, example, string(readFile(t, ageKeyserverFile)), string(readFile(t, policyFile)),
				"log "+lg+" https://l.example/\nquorum none\n", oneGroupPolicyText(t))
			for _, tt := range changes {
				texts = append(texts, tt.from, tt.give)
			}

			prop.ForAll(t, "UnmarshalText must set p to the policy of each text in turn", func(c *prop.Case) {
				var p checkpoint.Policy
				for _, text := range c.Draw(prop.List(prop.SampledFrom(texts...), prop.MinSize(1), prop.MaxSize(6)),
					"texts") {
					assert.NoError(c, p.UnmarshalText([]byte(text)), "UnmarshalText must accept the policy")
					assert.Equal(c, p, *mustParsePolicy(c, []byte(text)),
						"UnmarshalText must set the policy of the text", assert.EquateEmpty())
				}
			})
		})

		emptied := []struct {
			name string
			give string
		}{
			{name: "empties p for a second quorum line", give: example + "quorum none\n"},
			{
				name: "empties p for a text whose lines repeat p up to a line that breaks a rule",
				give: example + "witness X1 " + w1 + "\n",
			},
		}
		for _, tt := range emptied {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				p := examplePolicy(t)
				err := p.UnmarshalText([]byte(tt.give))
				assert.ErrorIs(t, err, checkpoint.ErrPolicy, "UnmarshalText must refuse the text")
				expect.Equal(t, errs.Classify(err), errs.Invalid, "the error must classify as Invalid")
				expect.Equal(t, p.Quorum, checkpoint.PolicyName(""), "UnmarshalText must remove the quorum")
				expect.Empty(t, p.Logs, "UnmarshalText must remove the logs")
				expect.Empty(t, p.Witnesses, "UnmarshalText must remove the witnesses")
				expect.Empty(t, p.Groups, "UnmarshalText must remove the groups")
			})
		}

		t.Run("parses into the memory of the policy that an error emptied", func(t *testing.T) {
			t.Parallel()
			var p checkpoint.Policy
			assert.NoError(t, p.UnmarshalText([]byte(example)), "UnmarshalText must accept the example")
			witnesses := p.Witnesses
			assert.ErrorIs(t, p.UnmarshalText([]byte("quorum a\n")), checkpoint.ErrPolicy,
				"UnmarshalText must refuse an undefined quorum")
			assert.NoError(t, p.UnmarshalText([]byte(example)), "UnmarshalText must accept the example again")
			expect.Equal(t, p.Witnesses, witnesses, "UnmarshalText must reuse the slice of witnesses",
				expect.ByIdentity())
			expect.Equal(t, &p, examplePolicy(t), "UnmarshalText must set every line of the example")
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
				give: mustParsePolicy(t, readFile(t, ageKeyserverFile)), want: true,
			},
			{
				name: "reports true for the policy of the vectors",
				give: mustParsePolicy(t, readFile(t, policyFile)), want: true,
			},
			{name: "reports true for the quorum none", give: changed(t, func(p *checkpoint.Policy) {
				p.Quorum = checkpoint.QuorumNone
			}), want: true},
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
			{name: "reports false for a public key of two logs", give: changed(t, func(p *checkpoint.Policy) {
				p.Logs = append(p.Logs, checkpoint.Log{Key: logSigner(t, exampleLog).Key()})
			}), want: false},
			{name: "reports false for a log key on a witness", give: changed(t, func(p *checkpoint.Policy) {
				p.Witnesses[0].Key.PublicKey = p.Logs[0].Key.PublicKey
			}), want: false},
			{name: "reports false for a public key of two witnesses", give: changed(t, func(p *checkpoint.Policy) {
				p.Witnesses[1].Key = p.Witnesses[0].Key
			}), want: false},
			{name: "reports false for a quorum that no line defines", give: changed(t, func(p *checkpoint.Policy) {
				p.Quorum = "Z"
			}), want: false},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				assert.Equal(t, tt.give.Valid(), tt.want, "Valid must report whether the policy keeps every rule")
			})
		}
	})

	t.Run("QuorumRule", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the rule of the example of tlog-policy for every set of cosigning witnesses",
			func(t *testing.T) {
				t.Parallel()
				tree := quorumTree(t, examplePolicy(t), resolver)
				names := slices.Concat(xNames, yNames)

				// Bit i of a set is the witness names[i]: the bits 0 to 2 are
				// the X witnesses, and the bits 3 to 5 the Y witnesses. The log
				// signs every note, because a note has a line at least, and the
				// quorum counts no line of the log.
				satisfies := func(set uint8) bool {
					signers := []note.Signer{logSigner(t, exampleLog)}
					for i, n := range names {
						if set&(1<<i) != 0 {
							signers = append(signers, witness(t, exampleWitnesses[n]))
						}
					}

					n, err := note.Sign(t.Context(), []byte(cosignedText), signers...)
					assert.NoError(t, err, "note.Sign must sign the text")

					return n.Check(tree) == nil
				}
				quorum := func(set uint8) bool {
					return bits.OnesCount8(set&0b000111) >= 2 && bits.OnesCount8(set&0b111000) >= 1
				}
				prop.Equal(t, satisfies, quorum, "the rule must count two X witnesses beside one Y witness",
					prop.Using(prop.Integer[uint8](0, 0b111111)), prop.Examples[uint8](0b010101, 0b111001))
			})

		t.Run("returns a rule of a group of 40 members that 40 cosignatures satisfy", func(t *testing.T) {
			t.Parallel()
			tree, n := largeGroup(t)
			assert.NoError(t, n.Check(tree), "the 40 witnesses must satisfy the group")
		})

		t.Run("returns a rule of a group of 40 members that 39 cosignatures do not satisfy", func(t *testing.T) {
			t.Parallel()
			tree, n := largeGroup(t)
			n.Signatures = n.Signatures[1:]
			assert.ErrorIs(t, n.Check(tree), sign.ErrThreshold, "39 witnesses must not satisfy the group")
		})

		hybrid := func(t *testing.T) (sign.Policy, []note.Signer) {
			t.Helper()

			r := maps.Clone(resolver)
			r[hybridType(t)] = note.Text(mldsa.Resolver(mldsa.MLDSA44, ""))
			ec, pq := logSigner(t, exampleLog), hybridSigner(t, exampleLog)
			ecV, err := r.Verifier(ec.Key())
			assert.NoError(t, err, "the resolver must build the Verifier of the Ed25519 key")
			pqV, err := r.Verifier(pq.Key())
			assert.NoError(t, err, "the resolver must build the Verifier of the ML-DSA key")

			var (
				rules sign.Rules
				keys  note.Keyring
			)

			keys.Reset(r)
			q, err := examplePolicy(t).QuorumRule(&rules, &keys)
			assert.NoError(t, err, "QuorumRule must build the quorum of the example")
			tree, err := sign.NewPolicyTree(sign.AtLeast("note", 2, sign.AllOf(exampleLog, pqV, ecV), q))
			assert.NoError(t, err, "NewPolicyTree must accept the hybrid log and the quorum")

			return tree, []note.Signer{
				ec, pq, witness(t, exampleWitnesses["X1"]), witness(t, exampleWitnesses["X2"]),
				witness(t, exampleWitnesses["Y1"]),
			}
		}

		t.Run("returns a rule that combines with an AllOf rule of both keys of a hybrid log", func(t *testing.T) {
			t.Parallel()
			tree, signers := hybrid(t)
			n, err := note.Sign(t.Context(), []byte(cosignedText), signers...)
			assert.NoError(t, err, "note.Sign must sign the checkpoint")
			assert.NoError(t, n.Check(tree), "both log keys and the quorum must satisfy the tree")
		})

		t.Run("returns a rule whose tree with an AllOf rule of both keys of a hybrid log refuses one key",
			func(t *testing.T) {
				t.Parallel()
				tree, signers := hybrid(t)
				n, err := note.Sign(t.Context(), []byte(cosignedText), append(signers[:1:1], signers[2:]...)...)
				assert.NoError(t, err, "note.Sign must sign the checkpoint")
				assert.ErrorIs(t, n.Check(tree), sign.ErrThreshold, "one of the two log keys must not satisfy the tree")
			})

		t.Run("returns ErrPolicy for the quorum none", func(t *testing.T) {
			t.Parallel()
			_, err := quorumRule(mustParsePolicy(t, []byte("quorum none\n")), resolver)
			assert.ErrorIs(t, err, checkpoint.ErrPolicy, "QuorumRule must refuse the quorum none")
			expect.Equal(t, err.Error(), checkpoint.ErrPolicy.Error()+": the quorum none has no rule",
				"the error must state the quorum none")
			expect.Equal(t, errs.Classify(err), errs.Invalid, "the error must classify as Invalid")
		})

		t.Run("returns ErrPolicy for a Policy that is not Valid", func(t *testing.T) {
			t.Parallel()
			_, err := quorumRule(&checkpoint.Policy{}, resolver)
			assert.ErrorIs(t, err, checkpoint.ErrPolicy, "QuorumRule must refuse the zero Policy")
			assert.Equal(t, err.Error(), checkpoint.ErrPolicy.Error()+`: the quorum "" names no witness or group `+
				"of an earlier line", "the error must state the first rule that the Policy breaks")
		})

		t.Run("returns the error of the Keyring", func(t *testing.T) {
			t.Parallel()
			_, err := quorumRule(examplePolicy(t), note.Resolver{note.TypeEd25519: note.Text(ed25519.Resolve)})
			assert.ErrorIs(t, err, note.ErrUnknownType, "QuorumRule must return the error of the Keyring")
		})

		t.Run("returns the error of the Keyring for a witness of a nested group", func(t *testing.T) {
			t.Parallel()
			p := examplePolicy(t)
			p.Witnesses[5].Key = hybridSigner(t, exampleWitnesses["Y3"]).Key()
			_, err := quorumRule(p, resolver)
			assert.ErrorIs(t, err, note.ErrUnknownType, "QuorumRule must return the error for the key of Y3")
		})
	})
}

// TestPolicyAllocs checks the allocation contract of each function and
// method. MaxAllocs counts the allocations of the whole process, so the
// test does not run in parallel.
//
// UnmarshalText compares each key of a file with the verifier key that it
// writes into a pooled buffer. Each measurement of it starts with two
// collections, which empty the pool. The warm-up call of MaxAllocs then
// grows a buffer, and the measured calls reuse it only when UnmarshalText
// keeps the growth of the buffer and returns it to the pool.
func TestPolicyAllocs(t *testing.T) {
	text := []byte(examplePolicyText(t))
	p := examplePolicy(t)
	name := checkpoint.PolicyName("X-witnesses")
	invalid := changed(t, func(p *checkpoint.Policy) { p.Quorum = "Z" })

	t.Run("PolicyName", func(t *testing.T) {
		t.Run("Valid", func(t *testing.T) {
			var got bool
			expect.MaxAllocs(t, func() { got = name.Valid() }, 0, "Valid must not allocate")
			assert.True(t, got, "the test must measure a valid name")
		})
	})

	t.Run("Log", func(t *testing.T) {
		t.Run("Valid", func(t *testing.T) {
			var got bool
			expect.MaxAllocs(t, func() { got = p.Logs[0].Valid() }, 0, "Valid must not allocate")
			assert.True(t, got, "the test must measure a valid log")
		})
	})

	t.Run("Witness", func(t *testing.T) {
		t.Run("Valid", func(t *testing.T) {
			var got bool
			expect.MaxAllocs(t, func() { got = p.Witnesses[0].Valid() }, 0, "Valid must not allocate")
			assert.True(t, got, "the test must measure a valid witness")
		})
	})

	t.Run("Group", func(t *testing.T) {
		t.Run("Valid", func(t *testing.T) {
			var got bool
			expect.MaxAllocs(t, func() { got = p.Groups[0].Valid() }, 0, "Valid must not allocate")
			assert.True(t, got, "the test must measure a valid group")
		})
	})

	t.Run("ParsePolicy", func(t *testing.T) {
		t.Run("of the example of tlog-policy", func(t *testing.T) {
			var err error
			expect.MaxAllocs(t, func() { _, err = checkpoint.ParsePolicy(text) }, parsePolicyAllocs,
				"ParsePolicy must allocate the string, the slices of lines, the members and the public keys alone")
			assert.NoError(t, err, "the test must measure a policy that ParsePolicy accepts")
		})

		t.Run("of the example of tlog-policy with a second log", func(t *testing.T) {
			two := []byte("log " + logSigner(t, "example.com/second").Key().String() + "\n" + string(text))

			var err error
			expect.MaxAllocs(t, func() { _, err = checkpoint.ParsePolicy(two) }, parsePolicyAllocs,
				"ParsePolicy must allocate the slice of logs once for two logs")
			assert.NoError(t, err, "the test must measure a policy that ParsePolicy accepts")
		})

		t.Run("of keys of one type without an assigned byte", func(t *testing.T) {
			pq := []byte("witness a " + hybridSigner(t, "example.com/a").Key().String() + "\nwitness b " +
				hybridSigner(t, "example.com/b").Key().String() + "\nquorum a\n")

			var err error
			expect.MaxAllocs(t, func() { _, err = checkpoint.ParsePolicy(pq) }, 4,
				"ParsePolicy must allocate the type of the run of keys once")
			assert.NoError(t, err, "the test must measure a policy that ParsePolicy accepts")
		})
	})

	t.Run("UnmarshalText", func(t *testing.T) {
		t.Run("of the policy of p", func(t *testing.T) {
			var reused checkpoint.Policy
			assert.NoError(t, reused.UnmarshalText(text), "UnmarshalText must accept the example")

			var err error
			runtime.GC()
			runtime.GC()
			expect.MaxAllocs(t, func() { err = reused.UnmarshalText(text) }, 0,
				"UnmarshalText must not allocate for the policy of p")
			assert.NoError(t, err, "the test must measure a policy that UnmarshalText accepts")
		})

		t.Run("of a policy that differs from p in one line", func(t *testing.T) {
			other := []byte(strings.Replace(string(text), "group X-witnesses 2", "group X-witnesses 3", 1))

			var err error
			expect.MaxAllocsWithSetup(t, func() *checkpoint.Policy { return mustParsePolicy(t, text) },
				func(p *checkpoint.Policy) { err = p.UnmarshalText(other) }, changedPolicyAllocs,
				"UnmarshalText must allocate the string, the members and the public keys alone")
			assert.NoError(t, err, "the test must measure a policy that UnmarshalText accepts")
		})
	})

	t.Run("Valid", func(t *testing.T) {
		t.Run("of the example of tlog-policy", func(t *testing.T) {
			var got bool
			expect.MaxAllocs(t, func() { got = p.Valid() }, 0, "Valid must not allocate")
			assert.True(t, got, "the test must measure a valid policy")
		})

		t.Run("of a Policy that is not Valid", func(t *testing.T) {
			var got bool
			expect.MaxAllocs(t, func() { got = invalid.Valid() }, 0, "Valid must not allocate for its fault")
			assert.False(t, got, "the test must measure a policy that is not valid")
		})
	})

	t.Run("QuorumRule", func(t *testing.T) {
		var (
			rules sign.Rules
			keys  note.Keyring
		)

		keys.Reset(resolver)
		_, err := p.QuorumRule(&rules, &keys)
		assert.NoError(t, err, "QuorumRule must build the quorum of the example")

		runtime.GC()
		runtime.GC()
		expect.MaxAllocs(t, func() {
			rules.Reset()
			keys.Reset(resolver)
			_, err = p.QuorumRule(&rules, &keys)
		}, 0, "QuorumRule must not allocate into the memory of the rules before")
		assert.NoError(t, err, "the test must measure a quorum that QuorumRule builds")
	})
}

// BenchmarkPolicy reports the cost of each function and method, and fails
// above the allocations that their contracts state.
func BenchmarkPolicy(b *testing.B) {
	text := []byte(examplePolicyText(b))
	p := examplePolicy(b)
	name := checkpoint.PolicyName("X-witnesses")
	invalid := changed(b, func(p *checkpoint.Policy) { p.Quorum = "Z" })

	b.Run("PolicyName", func(b *testing.B) {
		b.Run("Valid", func(b *testing.B) {
			var got bool

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				got = name.Valid()
			}

			assert.True(b, got, "the benchmark must measure a valid name")
		})
	})

	b.Run("Log", func(b *testing.B) {
		b.Run("Valid", func(b *testing.B) {
			var got bool

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				got = p.Logs[0].Valid()
			}

			assert.True(b, got, "the benchmark must measure a valid log")
		})
	})

	b.Run("Witness", func(b *testing.B) {
		b.Run("Valid", func(b *testing.B) {
			var got bool

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				got = p.Witnesses[0].Valid()
			}

			assert.True(b, got, "the benchmark must measure a valid witness")
		})
	})

	b.Run("Group", func(b *testing.B) {
		b.Run("Valid", func(b *testing.B) {
			var got bool

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				got = p.Groups[0].Valid()
			}

			assert.True(b, got, "the benchmark must measure a valid group")
		})
	})

	b.Run("ParsePolicy", func(b *testing.B) {
		b.Run("of the example of tlog-policy", func(b *testing.B) {
			var err error

			c := bench.Start(b).MaxAllocs(parsePolicyAllocs)
			defer c.End()

			for c.Loop() {
				_, err = checkpoint.ParsePolicy(text)
			}

			assert.NoError(b, err, "the benchmark must measure a policy that ParsePolicy accepts")
		})

		b.Run("of the example of tlog-policy with a second log", func(b *testing.B) {
			two := []byte("log " + logSigner(b, "example.com/second").Key().String() + "\n" + string(text))

			var err error

			c := bench.Start(b).MaxAllocs(parsePolicyAllocs)
			defer c.End()

			for c.Loop() {
				_, err = checkpoint.ParsePolicy(two)
			}

			assert.NoError(b, err, "the benchmark must measure a policy that ParsePolicy accepts")
		})

		b.Run("of keys of one type without an assigned byte", func(b *testing.B) {
			pq := []byte("witness a " + hybridSigner(b, "example.com/a").Key().String() + "\nwitness b " +
				hybridSigner(b, "example.com/b").Key().String() + "\nquorum a\n")

			var err error

			c := bench.Start(b).MaxAllocs(4)
			defer c.End()

			for c.Loop() {
				_, err = checkpoint.ParsePolicy(pq)
			}

			assert.NoError(b, err, "the benchmark must measure a policy that ParsePolicy accepts")
		})
	})

	b.Run("UnmarshalText", func(b *testing.B) {
		b.Run("of the policy of p", func(b *testing.B) {
			var reused checkpoint.Policy
			assert.NoError(b, reused.UnmarshalText(text), "UnmarshalText must accept the example")

			var err error

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				err = reused.UnmarshalText(text)
			}

			assert.NoError(b, err, "the benchmark must measure a policy that UnmarshalText accepts")
		})

		b.Run("of a policy that differs from p in one line", func(b *testing.B) {
			other := []byte(strings.Replace(string(text), "group X-witnesses 2", "group X-witnesses 3", 1))

			var err error

			c := bench.Start(b).MaxAllocs(changedPolicyAllocs)
			defer c.End()

			for c.Loop() {
				var reused *checkpoint.Policy
				c.Excluding(func() { reused = mustParsePolicy(b, text) })
				err = reused.UnmarshalText(other)
			}

			assert.NoError(b, err, "the benchmark must measure a policy that UnmarshalText accepts")
		})
	})

	b.Run("Valid", func(b *testing.B) {
		b.Run("of the example of tlog-policy", func(b *testing.B) {
			var got bool

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				got = p.Valid()
			}

			assert.True(b, got, "the benchmark must measure a valid policy")
		})

		b.Run("of a Policy that is not Valid", func(b *testing.B) {
			var got bool

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				got = invalid.Valid()
			}

			assert.False(b, got, "the benchmark must measure a policy that is not valid")
		})
	})

	b.Run("QuorumRule", func(b *testing.B) {
		var (
			rules sign.Rules
			keys  note.Keyring
		)

		keys.Reset(resolver)
		_, err := p.QuorumRule(&rules, &keys)
		assert.NoError(b, err, "QuorumRule must build the quorum of the example")

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			rules.Reset()
			keys.Reset(resolver)
			_, err = p.QuorumRule(&rules, &keys)
		}

		assert.NoError(b, err, "the benchmark must measure a quorum that QuorumRule builds")
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

// quorumTree returns the sign.Policy of the quorum of p with the resolver
// r, and fails the test when QuorumRule or NewPolicyTree refuses it.
func quorumTree(tb testing.TB, p *checkpoint.Policy, r note.Resolver) sign.Policy {
	tb.Helper()

	q, err := quorumRule(p, r)
	assert.NoError(tb, err, "QuorumRule must build the quorum")

	tree, err := sign.NewPolicyTree(q)
	assert.NoError(tb, err, "NewPolicyTree must accept the quorum")

	return tree
}

// largeGroup returns the tree of the quorum of a group of the 40 witnesses
// w0 to w39 with the threshold 40, more members than QuorumRule collects on
// the stack, and the note of cosignedText that each of them cosigned.
func largeGroup(tb testing.TB) (sign.Policy, *note.Note) {
	tb.Helper()

	const members = 40

	var s strings.Builder

	signers := make([]note.Signer, 0, members)

	for i := range members {
		w := witness(tb, note.Name("w"+strconv.Itoa(i)))
		s.WriteString("witness w" + strconv.Itoa(i) + " " + w.Key().String() + "\n")
		signers = append(signers, w)
	}

	s.WriteString("group g " + strconv.Itoa(members))

	for i := range members {
		s.WriteString(" w" + strconv.Itoa(i))
	}

	s.WriteString("\nquorum g\n")

	n, err := note.Sign(tb.Context(), []byte(cosignedText), signers...)
	assert.NoError(tb, err, "note.Sign must cosign the text")

	return quorumTree(tb, mustParsePolicy(tb, []byte(s.String())), resolver), &n
}

// logSigner returns the note Signer of type 0x01 of the log named name,
// over the Ed25519 key of name.
func logSigner(tb testing.TB, name note.Name) *note.TextSigner {
	tb.Helper()

	s, err := note.NewTextSigner(name, note.TypeEd25519, ed25519Signer(tb, string(name)))
	assert.NoError(tb, err, "NewTextSigner must accept the Ed25519 signer")

	return s
}

// hybridType returns the type of the ML-DSA-44 key of a hybrid log.
func hybridType(tb testing.TB) note.Type {
	tb.Helper()

	typ, err := note.NewType("example.com/ml-dsa-44")
	assert.NoError(tb, err, "NewType must accept the identifier")

	return typ
}

// hybridSigner returns the note Signer of hybridType of the ML-DSA-44 key
// of the hybrid log named name.
func hybridSigner(tb testing.TB, name note.Name) *note.TextSigner {
	tb.Helper()

	s, err := note.NewTextSigner(name, hybridType(tb), mldsaSigner(tb, mldsa.MLDSA44, string(name), ""))
	assert.NoError(tb, err, "NewTextSigner must accept the ML-DSA-44 signer")

	return s
}

// examplePolicyText returns the example of tlog-policy, with the keys of
// logSigner and witness.
func examplePolicyText(tb testing.TB) string {
	tb.Helper()

	var s strings.Builder

	s.WriteString("log " + logSigner(tb, exampleLog).Key().String() + "\n\n")

	for _, n := range xNames {
		s.WriteString("witness " + string(n) + " " + witness(tb, exampleWitnesses[n]).Key().String() + "\n")
	}

	s.WriteString("group X-witnesses 2 X1 X2 X3\n\n")

	for _, n := range yNames {
		s.WriteString("witness " + string(n) + " " + witness(tb, exampleWitnesses[n]).Key().String() + "\n")
	}

	s.WriteString("group Y-witnesses any Y1 Y2 Y3\n\n")
	s.WriteString("group X-and-Y all X-witnesses Y-witnesses\n")
	s.WriteString("quorum X-and-Y\n")

	return s.String()
}

// examplePolicy returns the Policy of examplePolicyText.
func examplePolicy(tb testing.TB) *checkpoint.Policy {
	tb.Helper()

	p := &checkpoint.Policy{Quorum: "X-and-Y", Logs: []checkpoint.Log{{Key: logSigner(tb, exampleLog).Key()}}}

	for _, n := range slices.Concat(xNames, yNames) {
		p.Witnesses = append(p.Witnesses, checkpoint.Witness{Name: n, Key: witness(tb, exampleWitnesses[n]).Key()})
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
func mustParsePolicy(tb assert.TB, text []byte) *checkpoint.Policy {
	tb.Helper()

	p, err := checkpoint.ParsePolicy(text)
	assert.NoError(tb, err, "ParsePolicy must accept the policy")

	return &p
}

// cosign returns the note of text with one cosignature from each witness of
// the example of tlog-policy that names lists.
func cosign(tb testing.TB, text string, names ...checkpoint.PolicyName) *note.Note {
	tb.Helper()

	signers := make([]note.Signer, len(names))
	for i, n := range names {
		signers[i] = witness(tb, exampleWitnesses[n])
	}

	n, err := note.Sign(tb.Context(), []byte(text), signers...)
	assert.NoError(tb, err, "note.Sign must cosign the text")

	return &n
}
