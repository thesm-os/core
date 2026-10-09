// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package note_test

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"flag"
	"os"
	"slices"
	"strings"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/note"
)

// Verifier keys of other implementations, which ParseKey must accept and
// AppendText must write back byte for byte.
const (
	// exampleKey is the verifier key of the example of signed-note.
	exampleKey = "example.com/foo+530d903a+AekyeRrm56hApGFkyQR4ZCbV54Id2LKaANYcrnKv3U2k"

	// exampleName is the key name of exampleKey.
	exampleName = "example.com/foo"

	// exampleTypeAndKey is the base64 of the type and the Ed25519 public
	// key of exampleKey.
	exampleTypeAndKey = "AekyeRrm56hApGFkyQR4ZCbV54Id2LKaANYcrnKv3U2k"

	// peterKey and enochKey are the verifier keys of the tests of
	// golang.org/x/mod v0.41.0, sumdb/note/note_test.go.
	peterKey = "PeterNeumann+c74f20a3+ARpc2QcUPDhMQegwxbzhKqiBfsVkmqq/LDE4izWy10TW"
	enochKey = "EnochRoot+af0cfe78+ATtqJ7zOtqQtYqOo0CpvDXNlMhV3HeJDpjrASKGLWdop"

	// plusKey and cosignatureKey are verifier keys of types 0x01 and 0x04
	// of the tests of transparency-dev/formats v0.1.1,
	// note/note_cosigv1_test.go. The base64 of plusKey contains a '+'.
	plusKey        = "TEST+7997405c+AQcC+FTVKf0jlTdHDY3rbevmnKxxPjigCXlVtGe6RIr6"
	cosignatureKey = "remora.n621.de+da77ade7+BOvN63jn/bLvkieywe8R6UYAtVtNbZpXh34x7onlmtw2"

	// mldsaKeyFile contains the verifier key of type 0x06 of the ML-DSA-44
	// log of filippo.io/torchwood v0.10.0, cmd/litewitness/testdata/mldsa.txt.
	mldsaKeyFile = "testdata/mldsa44.vkey"

	// wrongIDKeyFile contains the verifier key of type 0x06 of the tests of
	// transparency-dev/formats v0.1.1, note/note_cosigv1_test.go. Its key ID
	// is not the derivation of signed-note, which that module does not
	// check.
	wrongIDKeyFile = "testdata/mldsa44-wrong-id.vkey"
)

// The fixture values of the cases of Key.
const (
	// keyIDBytes is the length of a key ID, which a KeyID starts with and a
	// signature line encodes before the signature.
	keyIDBytes = 4

	// mldsa44Bytes is the length of an ML-DSA-44 public key.
	mldsa44Bytes = 1312

	// chunkBytes is the number of bytes whose padded base64 is 256
	// characters, the number that ParseKey decodes at a time, and ends in
	// two padding characters.
	chunkBytes = 190

	// flagSet and keyFlag name the flag set and the flag of the case that
	// sets a Key through the flag package.
	flagSet = "checkpoint"
	keyFlag = "key"
)

// The generators of the properties of Key.
var (
	// publicKeys generates public keys of 1 to 64 bytes.
	publicKeys = prop.Bytes(prop.MinSize(1), prop.MaxSize(64))

	// keys generates valid keys of a name of names, a type of types and a
	// public key of publicKeys.
	keys = prop.Composite(func(c *prop.Case) note.Key {
		return note.Key{
			Name:      c.Draw(names, "name"),
			Type:      c.Draw(types, "type"),
			PublicKey: c.Draw(publicKeys, "public key"),
		}
	})
)

// pqKey is the verifier key of a key of pqType.
var pqKey = vkey("example.com/pq", append([]byte(pqType), publicKey()...))

// refusedKeys are the verifier keys that ParseKey, Key.Set and
// Key.UnmarshalText refuse with ErrKey, one for each rule of a verifier
// key, with the detail that the error adds to the text of ErrKey.
var refusedKeys = []struct {
	name   string
	give   string
	detail string
}{
	{
		name:   "returns ErrKey for a key without a plus",
		give:   exampleName,
		detail: "no '+' after the name",
	},
	{
		name:   "returns ErrKey for a key without a second plus",
		give:   exampleName + "+530d903a",
		detail: "no '+' after the key ID",
	},
	{
		name:   "returns ErrKey for an invalid name",
		give:   vkey("a b", append([]byte{1}, publicKey()...)),
		detail: `invalid name "a b"`,
	},
	{
		name:   "returns ErrKey for a key ID of 7 digits",
		give:   strings.Replace(exampleKey, "530d903a", "530d903", 1),
		detail: `key ID "530d903" is not 8 lowercase hexadecimal digits`,
	},
	{
		name:   "returns ErrKey for a key ID of uppercase digits",
		give:   strings.Replace(exampleKey, "530d903a", "530D903A", 1),
		detail: `key ID "530D903A" is not 8 lowercase hexadecimal digits`,
	},
	{
		name:   "returns ErrKey for a key ID of 10 digits",
		give:   strings.Replace(exampleKey, "530d903a", "530d903a00", 1),
		detail: `key ID "530d903a00" is not 8 lowercase hexadecimal digits`,
	},
	{
		name:   "returns ErrKey for a key ID that is not hexadecimal",
		give:   strings.Replace(exampleKey, "530d903a", "530d903g", 1),
		detail: `key ID "530d903g" is not 8 lowercase hexadecimal digits`,
	},
	{
		name:   "returns ErrKey for base64 that is not base64",
		give:   exampleKey + "!",
		detail: "the type and the public key are not padded standard base64",
	},
	{
		name:   "returns ErrKey for base64 without its padding",
		give:   strings.TrimSuffix(vkey("a", []byte{1, 2}), "="),
		detail: "the type and the public key are not padded standard base64",
	},
	{
		name:   "returns ErrKey for base64 with spare bits",
		give:   strings.Replace(vkey("a", []byte{1, 2}), "AQI=", "AQJ=", 1),
		detail: "the type and the public key are not padded standard base64",
	},
	{
		name:   "returns ErrKey for padding that ends the first 256 characters of longer base64",
		give:   vkey("a", append([]byte{1}, make([]byte, chunkBytes-1)...), []byte{1, 2, 3}),
		detail: "the type and the public key are not padded standard base64",
	},
	{
		name:   "returns ErrKey for base64 with a newline",
		give:   strings.Replace(exampleKey, "Aeky", "Ae\nky", 1),
		detail: "the type and the public key are not padded standard base64",
	},
	{
		name:   "returns ErrKey for base64 with a carriage return",
		give:   strings.Replace(exampleKey, "Aeky", "Ae\rky", 1),
		detail: "the type and the public key are not padded standard base64",
	},
	{
		name:   "returns ErrKey for no bytes after the key ID",
		give:   "a+00000000+",
		detail: "an invalid type, or no public key after the type",
	},
	{
		name:   "returns ErrKey for a lone 0xff",
		give:   vkey("a", []byte{typeOther}),
		detail: "an invalid type, or no public key after the type",
	},
	{
		name:   "returns ErrKey for a type without a public key",
		give:   vkey("a", []byte{1}),
		detail: "an invalid type, or no public key after the type",
	},
	{
		name:   "returns ErrKey for a 0xff type without a public key",
		give:   vkey("a", []byte{typeOther, 1, 'x'}),
		detail: "an invalid type, or no public key after the type",
	},
	{
		name:   "returns ErrKey for an identifier that runs past the bytes",
		give:   vkey("a", []byte{typeOther, 5, 'x', 'y'}),
		detail: "an invalid type, or no public key after the type",
	},
	{
		name:   "returns ErrKey for an empty identifier",
		give:   vkey("a", []byte{typeOther, 0, 1, 2}),
		detail: "an invalid type, or no public key after the type",
	},
	{
		name:   "returns ErrKey for a key ID that differs from the key",
		give:   strings.Replace(exampleKey, "530d903a", "530d903b", 1),
		detail: "key ID 530d903b differs from 530d903a, the key ID of the key",
	},
}

func TestKey(t *testing.T) {
	t.Parallel()

	t.Run("ParseKey", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the key of signed-note's example", func(t *testing.T) {
			t.Parallel()
			raw, err := base64.StdEncoding.DecodeString(exampleTypeAndKey)
			assert.NoError(t, err, "the fixture must decode")

			k := mustParseKey(t, exampleKey)
			expect.Equal(t, k.Name, note.Name(exampleName), "ParseKey must return the name")
			expect.Equal(t, k.Type, note.TypeEd25519, "ParseKey must return type 0x01")
			expect.Equal(t, k.PublicKey, raw[1:], "ParseKey must return the 32 bytes after the type")
		})

		t.Run("returns keys that String writes back byte for byte", func(t *testing.T) {
			t.Parallel()
			for _, give := range []string{
				exampleKey, peterKey, enochKey, plusKey, cosignatureKey, readKey(t, mldsaKeyFile),
			} {
				expect.Equal(t, mustParseKey(t, give).String(), give, "String must write the key back byte for byte")
			}
		})

		t.Run("returns type 0x04 for the cosignature key of transparency-dev/formats", func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, mustParseKey(t, cosignatureKey).Type, note.Type("\x04"), "ParseKey must return type 0x04")
		})

		t.Run("returns type 0x06 for the ML-DSA-44 key of torchwood", func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, mustParseKey(t, readKey(t, mldsaKeyFile)).Type, note.Type("\x06"),
				"ParseKey must return type 0x06")
		})

		t.Run("returns the public key of the ML-DSA-44 key of torchwood", func(t *testing.T) {
			t.Parallel()
			assert.Length(t, mustParseKey(t, readKey(t, mldsaKeyFile)).PublicKey, mldsa44Bytes,
				"ParseKey must return the 1312 bytes of an ML-DSA-44 key")
		})

		t.Run("returns ErrKey for the ML-DSA-44 key of transparency-dev/formats whose key ID is not the derivation",
			func(t *testing.T) {
				t.Parallel()
				_, err := note.ParseKey(readKey(t, wrongIDKeyFile))
				assert.ErrorIs(t, err, note.ErrKey, "ParseKey must refuse a key ID that is not the derivation")
			})

		t.Run("returns a key of a type without an assigned byte", func(t *testing.T) {
			t.Parallel()
			k := mustParseKey(t, pqKey)
			expect.Equal(t, k.Type, pqType, "ParseKey must return the type of the length byte")
			expect.Equal(t, k.PublicKey, publicKey(), "ParseKey must return the bytes after the type")
		})

		t.Run("returns the public key of base64 that ends in two padding characters", func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, mustParseKey(t, vkey("a", []byte{1, 2, 3, 4})).PublicKey, []byte{2, 3, 4},
				"ParseKey must return the bytes after the type")
		})

		t.Run("returns the public key of base64 of 256 characters that ends in padding", func(t *testing.T) {
			t.Parallel()
			raw := append([]byte{1}, make([]byte, chunkBytes-1)...)
			assert.Equal(t, mustParseKey(t, vkey("a", raw)).PublicKey, raw[1:],
				"ParseKey must return the bytes after the type")
		})

		t.Run("returns the key that MarshalText writes", func(t *testing.T) {
			t.Parallel()
			prop.RoundTrip(t, note.Key.MarshalText, func(text []byte) (note.Key, error) {
				return note.ParseKey(string(text))
			}, "ParseKey must return the key that MarshalText writes", prop.Using(keys))
		})

		for _, tt := range refusedKeys {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				got, err := note.ParseKey(tt.give)
				assert.ErrorIs(t, err, note.ErrKey, "ParseKey must refuse the key")
				expect.Equal(t, err.Error(), note.ErrKey.Error()+": "+tt.detail, "the error must name the rule")
				expect.Equal(t, errs.Classify(err), errs.Invalid, "the error must classify as Invalid")
				expect.Equal(t, got, note.Key{}, "ParseKey must return the zero Key with an error")
			})
		}
	})

	t.Run("Valid", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			give note.Key
			want bool
		}{
			{
				name: "reports true for a complete key",
				give: note.Key{Name: "a", Type: note.TypeEd25519, PublicKey: publicKey()}, want: true,
			},
			{
				name: "reports false for an invalid name",
				give: note.Key{Name: "a b", Type: note.TypeEd25519, PublicKey: publicKey()}, want: false,
			},
			{
				name: "reports false for an invalid type",
				give: note.Key{Name: "a", Type: "\xff", PublicKey: publicKey()}, want: false,
			},
			{
				name: "reports false for an empty public key",
				give: note.Key{Name: "a", Type: note.TypeEd25519, PublicKey: []byte{}}, want: false,
			},
			{name: "reports false for the zero Key", give: note.Key{}, want: false},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				assert.Equal(t, tt.give.Valid(), tt.want, "Valid must report whether the key is complete")
			})
		}
	})

	t.Run("ID", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the key ID of signed-note's example", func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, mustParseKey(t, exampleKey).ID(), uint32(exampleKeyID),
				"ID must return the key ID that the example records")
		})

		t.Run("returns the first four bytes of SHA-256(name ‖ 0x0A ‖ type ‖ public key)", func(t *testing.T) {
			t.Parallel()
			prop.Equal(t, note.Key.ID, func(k note.Key) uint32 {
				sum := sha256.Sum256(slices.Concat([]byte(k.Name), []byte("\n"), []byte(k.Type), k.PublicKey))

				return binary.BigEndian.Uint32(sum[:keyIDBytes])
			}, "ID must return the first four bytes of the hash of the key", prop.Using(keys))
		})
	})

	t.Run("AppendText", func(t *testing.T) {
		t.Parallel()

		t.Run("appends the verifier key to b", func(t *testing.T) {
			t.Parallel()
			got, err := mustParseKey(t, exampleKey).AppendText([]byte("key: "))
			assert.NoError(t, err, "AppendText must accept a valid key")
			assert.Equal(t, string(got), "key: "+exampleKey, "AppendText must append the verifier key")
		})

		t.Run("returns ErrKey with b unchanged for a Key that is not Valid", func(t *testing.T) {
			t.Parallel()
			got, err := note.Key{Name: "a"}.AppendText([]byte("key: "))
			expect.ErrorIs(t, err, note.ErrKey, "AppendText must refuse an incomplete key")
			expect.Equal(t, errs.Classify(err), errs.Invalid, "the error must classify as Invalid")
			expect.Equal(t, string(got), "key: ", "AppendText must return b unchanged")
		})
	})

	t.Run("MarshalText", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the verifier key", func(t *testing.T) {
			t.Parallel()
			got, err := mustParseKey(t, peterKey).MarshalText()
			assert.NoError(t, err, "MarshalText must accept a valid key")
			assert.Equal(t, string(got), peterKey, "MarshalText must return the verifier key")
		})

		t.Run("returns a verifier key whose capacity is its length", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "MarshalText must return a verifier key whose capacity is its length", func(c *prop.Case) {
				got, err := c.Draw(keys, "key").MarshalText()
				assert.NoError(c, err, "MarshalText must accept a valid key")
				assert.Equal(c, cap(got), len(got), "MarshalText must allocate the verifier key at its length")
			})
		})

		t.Run("returns nil with ErrKey for a Key that is not Valid", func(t *testing.T) {
			t.Parallel()
			got, err := note.Key{}.MarshalText()
			expect.ErrorIs(t, err, note.ErrKey, "MarshalText must refuse the zero Key")
			expect.Nil(t, got, "MarshalText must return nil with an error")
		})
	})

	t.Run("UnmarshalText", func(t *testing.T) {
		t.Parallel()

		t.Run("decodes a list of verifier keys from JSON", func(t *testing.T) {
			t.Parallel()
			var got []note.Key
			err := json.Unmarshal([]byte(`["`+peterKey+`","`+enochKey+`"]`), &got)
			assert.NoError(t, err, "a list of verifier keys must decode")
			assert.Equal(t, got, []note.Key{mustParseKey(t, peterKey), mustParseKey(t, enochKey)},
				"UnmarshalText must parse each key")
		})

		t.Run("copies the public key into the public key of k", func(t *testing.T) {
			t.Parallel()
			k := mustParseKey(t, peterKey)
			pub := &k.PublicKey[0]
			assert.NoError(t, k.UnmarshalText([]byte(enochKey)), "UnmarshalText must accept the key")
			expect.Equal(t, &k.PublicKey[0], pub, "UnmarshalText must copy the public key into the one of k",
				expect.ByIdentity())
			expect.Equal(t, k, mustParseKey(t, enochKey), "UnmarshalText must set the key")
		})

		t.Run("sets k to each key in turn", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "UnmarshalText must set k to each key in turn", func(c *prop.Case) {
				var k note.Key
				for _, want := range c.Draw(prop.List(keys, prop.MinSize(1)), "keys") {
					text, err := want.MarshalText()
					assert.NoError(c, err, "MarshalText must accept a valid key")
					assert.NoError(c, k.UnmarshalText(text),
						"UnmarshalText must accept the key that MarshalText writes")
					assert.Equal(c, k, want, "UnmarshalText must set the key that MarshalText wrote")
				}
			})
		})

		for _, tt := range refusedKeys {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				k := mustParseKey(t, peterKey)
				var err error
				expect.Pure(t, func() note.Key {
					snapshot := k
					snapshot.PublicKey = slices.Clone(k.PublicKey)

					return snapshot
				}, func() { err = k.UnmarshalText([]byte(tt.give)) }, "UnmarshalText must leave k unchanged")
				assert.ErrorIs(t, err, note.ErrKey, "UnmarshalText must refuse the key")
				expect.Equal(t, err.Error(), note.ErrKey.Error()+": "+tt.detail, "the error must name the rule")
				expect.Equal(t, errs.Classify(err), errs.Invalid, "the error must classify as Invalid")
			})
		}
	})

	t.Run("Set", func(t *testing.T) {
		t.Parallel()

		t.Run("sets k to the key of vkey", func(t *testing.T) {
			t.Parallel()
			var k note.Key
			assert.NoError(t, k.Set(exampleKey), "Set must accept the key")
			assert.Equal(t, k, mustParseKey(t, exampleKey), "Set must set the key of ParseKey")
		})

		t.Run("sets the Key of a flag", func(t *testing.T) {
			t.Parallel()
			var k note.Key
			flags := flag.NewFlagSet(flagSet, flag.ContinueOnError)
			flags.Var(&k, keyFlag, "the verifier key of the log")
			assert.NoError(t, flags.Parse([]string{"-" + keyFlag, peterKey}), "the flag must take the key")
			expect.Equal(t, k, mustParseKey(t, peterKey), "Set must set the key of the flag")
			expect.Equal(t, flags.Lookup(keyFlag).Value.String(), peterKey, "the flag must print the key")
		})

		t.Run("copies the public key into the public key of k", func(t *testing.T) {
			t.Parallel()
			k := mustParseKey(t, peterKey)
			pub := &k.PublicKey[0]
			assert.NoError(t, k.Set(enochKey), "Set must accept the key")
			expect.Equal(t, &k.PublicKey[0], pub, "Set must copy the public key into the one of k", expect.ByIdentity())
			expect.Equal(t, k, mustParseKey(t, enochKey), "Set must set the key")
		})

		t.Run("sets k to each key in turn", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "Set must set k to each key in turn", func(c *prop.Case) {
				var k note.Key
				for _, want := range c.Draw(prop.List(keys, prop.MinSize(1)), "keys") {
					assert.NoError(c, k.Set(want.String()), "Set must accept the key that String writes")
					assert.Equal(c, k, want, "Set must set the key that String wrote")
				}
			})
		})

		for _, tt := range refusedKeys {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				k := mustParseKey(t, peterKey)
				var err error
				expect.Pure(t, func() note.Key {
					snapshot := k
					snapshot.PublicKey = slices.Clone(k.PublicKey)

					return snapshot
				}, func() { err = k.Set(tt.give) }, "Set must leave k unchanged")
				assert.ErrorIs(t, err, note.ErrKey, "Set must refuse the key")
				expect.Equal(t, err.Error(), note.ErrKey.Error()+": "+tt.detail, "the error must name the rule")
				expect.Equal(t, errs.Classify(err), errs.Invalid, "the error must classify as Invalid")
			})
		}
	})

	t.Run("String", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the verifier key", func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, mustParseKey(t, enochKey).String(), enochKey, "String must return the verifier key")
		})

		t.Run("returns the empty string for a Key that is not Valid", func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, note.Key{}.String(), "", "String must return the empty string for the zero Key")
		})
	})
}

// TestKeyAllocs checks the allocation contract of each function and method.
// MaxAllocs counts the allocations of the whole process, so the test does
// not run in parallel.
func TestKeyAllocs(t *testing.T) {
	k := mustParseKey(t, exampleKey)

	t.Run("ParseKey", func(t *testing.T) {
		t.Run("of a type of one byte", func(t *testing.T) {
			var err error
			expect.MaxAllocs(t, func() { _, err = note.ParseKey(exampleKey) }, 1,
				"ParseKey must allocate the public key alone")
			assert.NoError(t, err, "the test must measure a key that ParseKey accepts")
		})

		t.Run("of a type without an assigned byte", func(t *testing.T) {
			var err error
			expect.MaxAllocs(t, func() { _, err = note.ParseKey(pqKey) }, 2,
				"ParseKey must allocate the public key and the type alone")
			assert.NoError(t, err, "the test must measure a key that ParseKey accepts")
		})
	})

	t.Run("Valid", func(t *testing.T) {
		var got bool
		expect.MaxAllocs(t, func() { got = k.Valid() }, 0, "Valid must not allocate")
		assert.True(t, got, "the test must measure a valid key")
	})

	t.Run("ID", func(t *testing.T) {
		var got uint32
		expect.MaxAllocs(t, func() { got = k.ID() }, 0, "ID must not allocate")
		assert.Equal(t, got, uint32(exampleKeyID), "the test must measure the key of signed-note's example")
	})

	t.Run("AppendText", func(t *testing.T) {
		buf := make([]byte, 0, len(exampleKey))

		var err error
		expect.MaxAllocs(t, func() { _, err = k.AppendText(buf[:0]) }, 0,
			"AppendText must not allocate into a buffer with room")
		assert.NoError(t, err, "the test must measure a valid key")
	})

	t.Run("MarshalText", func(t *testing.T) {
		var err error
		expect.MaxAllocs(t, func() { _, err = k.MarshalText() }, 1, "MarshalText must allocate the verifier key alone")
		assert.NoError(t, err, "the test must measure a valid key")
	})

	t.Run("Set", func(t *testing.T) {
		t.Run("of the key of k", func(t *testing.T) {
			var reused note.Key
			assert.NoError(t, reused.Set(exampleKey), "Set must accept the key")

			var err error
			expect.MaxAllocs(t, func() { err = reused.Set(exampleKey) }, 0, "Set must not allocate for the key of k")
			assert.NoError(t, err, "the test must measure a key that Set accepts")
		})

		t.Run("of the key of k of a type without an assigned byte", func(t *testing.T) {
			var reused note.Key
			assert.NoError(t, reused.Set(pqKey), "Set must accept the key")

			var err error
			expect.MaxAllocs(t, func() { err = reused.Set(pqKey) }, 0, "Set must not allocate for the key of k")
			assert.NoError(t, err, "the test must measure a key that Set accepts")
		})
	})

	t.Run("UnmarshalText", func(t *testing.T) {
		t.Run("of the key of k", func(t *testing.T) {
			text := []byte(exampleKey)

			var reused note.Key
			assert.NoError(t, reused.UnmarshalText(text), "UnmarshalText must accept the key")

			var err error
			expect.MaxAllocs(t, func() { err = reused.UnmarshalText(text) }, 0,
				"UnmarshalText must not allocate for the key of k")
			assert.NoError(t, err, "the test must measure a key that UnmarshalText accepts")
		})

		t.Run("of the key of k of a type without an assigned byte", func(t *testing.T) {
			text := []byte(pqKey)

			var reused note.Key
			assert.NoError(t, reused.UnmarshalText(text), "UnmarshalText must accept the key")

			var err error
			expect.MaxAllocs(t, func() { err = reused.UnmarshalText(text) }, 0,
				"UnmarshalText must not allocate for the key of k")
			assert.NoError(t, err, "the test must measure a key that UnmarshalText accepts")
		})
	})

	t.Run("String", func(t *testing.T) {
		var got string
		expect.MaxAllocs(t, func() { got = k.String() }, 1, "String must allocate the verifier key alone")
		assert.Equal(t, got, exampleKey, "the test must measure the key of signed-note's example")
	})
}

// BenchmarkKey reports the cost of each function and method, and fails
// above the allocations that their contracts state.
func BenchmarkKey(b *testing.B) {
	k := mustParseKey(b, exampleKey)

	b.Run("ParseKey", func(b *testing.B) {
		b.Run("of a type of one byte", func(b *testing.B) {
			var err error

			c := bench.Start(b).MaxAllocs(1)
			defer c.End()

			for c.Loop() {
				_, err = note.ParseKey(exampleKey)
			}

			assert.NoError(b, err, "the benchmark must measure a key that ParseKey accepts")
		})

		b.Run("of a type without an assigned byte", func(b *testing.B) {
			var err error

			c := bench.Start(b).MaxAllocs(2)
			defer c.End()

			for c.Loop() {
				_, err = note.ParseKey(pqKey)
			}

			assert.NoError(b, err, "the benchmark must measure a key that ParseKey accepts")
		})
	})

	b.Run("Valid", func(b *testing.B) {
		var got bool

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = k.Valid()
		}

		assert.True(b, got, "the benchmark must measure a valid key")
	})

	b.Run("ID", func(b *testing.B) {
		var got uint32

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = k.ID()
		}

		assert.Equal(b, got, uint32(exampleKeyID), "the benchmark must measure the key of signed-note's example")
	})

	b.Run("AppendText", func(b *testing.B) {
		buf := make([]byte, 0, len(exampleKey))

		var err error

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			_, err = k.AppendText(buf[:0])
		}

		assert.NoError(b, err, "the benchmark must measure a valid key")
	})

	b.Run("MarshalText", func(b *testing.B) {
		var err error

		c := bench.Start(b).MaxAllocs(1)
		defer c.End()

		for c.Loop() {
			_, err = k.MarshalText()
		}

		assert.NoError(b, err, "the benchmark must measure a valid key")
	})

	b.Run("Set", func(b *testing.B) {
		b.Run("of the key of k", func(b *testing.B) {
			var reused note.Key
			assert.NoError(b, reused.Set(exampleKey), "Set must accept the key")

			var err error

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				err = reused.Set(exampleKey)
			}

			assert.NoError(b, err, "the benchmark must measure a key that Set accepts")
		})

		b.Run("of the key of k of a type without an assigned byte", func(b *testing.B) {
			var reused note.Key
			assert.NoError(b, reused.Set(pqKey), "Set must accept the key")

			var err error

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				err = reused.Set(pqKey)
			}

			assert.NoError(b, err, "the benchmark must measure a key that Set accepts")
		})
	})

	b.Run("UnmarshalText", func(b *testing.B) {
		b.Run("of the key of k", func(b *testing.B) {
			text := []byte(exampleKey)

			var reused note.Key
			assert.NoError(b, reused.UnmarshalText(text), "UnmarshalText must accept the key")

			var err error

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				err = reused.UnmarshalText(text)
			}

			assert.NoError(b, err, "the benchmark must measure a key that UnmarshalText accepts")
		})

		b.Run("of the key of k of a type without an assigned byte", func(b *testing.B) {
			text := []byte(pqKey)

			var reused note.Key
			assert.NoError(b, reused.UnmarshalText(text), "UnmarshalText must accept the key")

			var err error

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				err = reused.UnmarshalText(text)
			}

			assert.NoError(b, err, "the benchmark must measure a key that UnmarshalText accepts")
		})
	})

	b.Run("String", func(b *testing.B) {
		var got string

		c := bench.Start(b).MaxAllocs(1)
		defer c.End()

		for c.Loop() {
			got = k.String()
		}

		assert.Equal(b, got, exampleKey, "the benchmark must measure the key of signed-note's example")
	})
}

// readKey returns the verifier key on the line of file.
func readKey(tb testing.TB, file string) string {
	tb.Helper()

	b, err := os.ReadFile(file)
	assert.NoError(tb, err, "the fixture "+file+" must be readable")

	return strings.TrimSuffix(string(b), "\n")
}

// vkey returns the verifier key of name whose type and public key are the
// bytes of parts, with the key ID that the test computes itself, so that a
// case refuses a key only for the rule that it names. It encodes each part
// in base64 after the one before it, so the base64 of a part whose length
// is not a multiple of 3 ends in padding before the base64 of the next.
func vkey(name string, parts ...[]byte) string {
	h := sha256.New()
	h.Write([]byte(name + "\n"))

	var encoded strings.Builder
	for _, part := range parts {
		h.Write(part)
		encoded.WriteString(base64.StdEncoding.EncodeToString(part))
	}

	return name + "+" + hex.EncodeToString(h.Sum(nil)[:keyIDBytes]) + "+" + encoded.String()
}

// publicKey returns 32 bytes of public key material, whose byte i is i.
func publicKey() []byte {
	pub := make([]byte, 32)
	for i := range pub {
		pub[i] = byte(i)
	}

	return pub
}

// mustParseKey returns the key of text, and fails the test when ParseKey
// refuses it.
func mustParseKey(tb testing.TB, text string) note.Key {
	tb.Helper()

	k, err := note.ParseKey(text)
	assert.NoError(tb, err, "ParseKey must accept "+text)

	return k
}
