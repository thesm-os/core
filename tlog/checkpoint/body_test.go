// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package checkpoint_test

import (
	"encoding/base64"
	"os"
	"strings"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/tlog/checkpoint"
)

// The body of the example of tlog-checkpoint.
const (
	exampleOrigin = "example.com/behind-the-sofa"
	exampleSize   = 20852163
	exampleRoot   = "CsUYapGGPo4dkMgIAUqom/Xajj7h2fB2MPA3j2jxq2I="
	exampleBody   = exampleOrigin + "\n20852163\n" + exampleRoot + "\n"
)

// lineAlphabet are the characters of the lines that lines generates: a
// space, a plus, characters outside ASCII, and U+007F to U+009F.
const lineAlphabet = "ab +/例λ\u007f\u0080\u009f"

// parsedBody is the label under which the property of the canonical form
// counts a changed body that ParseBody accepts. About one changed body in
// eight parses, so 1,000 cases check about 130 of them.
const parsedBody = "a changed body that ParseBody accepts"

// The generators of the properties of Body.
var (
	// lines generates valid lines of 1 to 20 characters of lineAlphabet.
	lines = prop.String(prop.Alphabet(lineAlphabet), prop.MinSize(1), prop.MaxSize(20))

	// roots generates roots of 32, 48 or 64 bytes.
	roots = prop.Composite(func(c *prop.Case) crypto.Digest {
		size := c.Draw(prop.SampledFrom(crypto.DigestSize256, 48, 64), "size")
		d, err := crypto.DigestFromBytes(c.Draw(prop.Bytes(prop.MinSize(size), prop.MaxSize(size)), "root"))
		assert.NoError(c, err, "a root of 32, 48 or 64 bytes must be a digest")

		return d
	})

	// bodies generates valid bodies: an origin of lines, any size, a root
	// of roots, and up to two extension lines of lines.
	bodies = prop.Composite(func(c *prop.Case) checkpoint.Body {
		b := checkpoint.Body{
			Origin: checkpoint.Origin(c.Draw(lines, "origin")),
			Size:   c.Draw(prop.Of[uint64](), "size"),
			Root:   c.Draw(roots, "root"),
		}
		for _, e := range c.Draw(prop.List(lines, prop.MaxSize(2)), "extensions") {
			b.Extensions = append(b.Extensions, checkpoint.Extension(e))
		}

		return b
	})

	// changedBodies generates the texts of bodies with one byte replaced
	// by any byte.
	changedBodies = prop.Composite(func(c *prop.Case) []byte {
		text, err := c.Draw(bodies, "body").AppendText(nil)
		assert.NoError(c, err, "AppendText must accept a valid body")
		text[c.Draw(prop.Integer(0, len(text)-1), "position")] = c.Draw(prop.Of[byte](), "byte")

		return text
	})
)

func TestBody(t *testing.T) {
	t.Parallel()

	t.Run("Origin", func(t *testing.T) {
		t.Parallel()

		t.Run("Valid", func(t *testing.T) {
			t.Parallel()

			tests := []struct {
				name string
				give checkpoint.Origin
				want bool
			}{
				{
					name: "reports true for the origin of the example of tlog-checkpoint",
					give: exampleOrigin,
					want: true,
				},
				{name: "reports true for an origin with a space", give: "a b", want: true},
				{name: "reports true for an origin with a plus", give: "a+b", want: true},
				{name: "reports true for U+007F to U+009F", give: "a\u007f\u0080\u009f", want: true},
				{name: "reports false for the empty origin", give: "", want: false},
				{name: "reports false for an origin that is not valid UTF-8", give: "a\xff", want: false},
				{name: "reports false for an origin with a newline", give: "a\nb", want: false},
				{name: "reports false for an origin with U+001F", give: "a\x1f", want: false},
				{name: "reports false for an origin with a NUL", give: "\x00", want: false},
			}
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					t.Parallel()
					assert.Equal(t, tt.give.Valid(), tt.want, "Valid must report whether the origin is a line")
				})
			}
		})
	})

	t.Run("Extension", func(t *testing.T) {
		t.Parallel()

		t.Run("Valid", func(t *testing.T) {
			t.Parallel()

			tests := []struct {
				name string
				give checkpoint.Extension
				want bool
			}{
				{name: "reports true for a line with a space", give: "an extension line", want: true},
				{name: "reports false for the empty line", give: "", want: false},
				{name: "reports false for a line with a carriage return", give: "a\r", want: false},
			}
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					t.Parallel()
					assert.Equal(t, tt.give.Valid(), tt.want, "Valid must report whether the extension is a line")
				})
			}
		})
	})

	t.Run("ParseBody", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the body of the example of tlog-checkpoint", func(t *testing.T) {
			t.Parallel()
			got, err := checkpoint.ParseBody([]byte(exampleBody))
			assert.NoError(t, err, "ParseBody must accept the example")
			assert.Equal(t, got, checkpoint.Body{Origin: exampleOrigin, Size: exampleSize, Root: exampleDigest(t)},
				"ParseBody must return the origin, the size and the root")
		})

		t.Run("returns the extension lines in order", func(t *testing.T) {
			t.Parallel()
			got, err := checkpoint.ParseBody([]byte(exampleBody + "first\nsecond line\n"))
			assert.NoError(t, err, "ParseBody must accept extension lines")
			assert.Equal(t, got.Extensions, []checkpoint.Extension{"first", "second line"},
				"ParseBody must return each extension line without its newline")
		})

		t.Run("returns the size 0", func(t *testing.T) {
			t.Parallel()
			got, err := checkpoint.ParseBody([]byte("a\n0\n" + exampleRoot + "\n"))
			assert.NoError(t, err, "ParseBody must accept the size 0")
			assert.Equal(t, got.Size, uint64(0), "ParseBody must return the size 0")
		})

		t.Run("returns the largest uint64", func(t *testing.T) {
			t.Parallel()
			got, err := checkpoint.ParseBody([]byte("a\n18446744073709551615\n" + exampleRoot + "\n"))
			assert.NoError(t, err, "ParseBody must accept the largest uint64")
			assert.Equal(t, got.Size, ^uint64(0), "ParseBody must return the largest uint64")
		})

		t.Run("returns a root of 48 bytes", func(t *testing.T) {
			t.Parallel()
			got, err := checkpoint.ParseBody(
				[]byte("a\n1\n" + base64.StdEncoding.EncodeToString(root(t, 48).Bytes()) + "\n"),
			)
			assert.NoError(t, err, "ParseBody must accept a root of 48 bytes")
			assert.Equal(t, got.Root, root(t, 48), "ParseBody must return the root of 48 bytes")
		})

		t.Run("returns a root of 64 bytes", func(t *testing.T) {
			t.Parallel()
			got, err := checkpoint.ParseBody(
				[]byte("a\n1\n" + base64.StdEncoding.EncodeToString(root(t, 64).Bytes()) + "\n"),
			)
			assert.NoError(t, err, "ParseBody must accept a root of 64 bytes")
			assert.Equal(t, got.Root, root(t, 64), "ParseBody must return the root of 64 bytes")
		})

		t.Run("returns a body that does not alias text", func(t *testing.T) {
			t.Parallel()
			text := []byte(exampleBody + "ext\n")
			got, err := checkpoint.ParseBody(text)
			assert.NoError(t, err, "ParseBody must accept the body")
			clear(text)
			expect.Equal(t, got.Origin, checkpoint.Origin(exampleOrigin), "a change to text must not change the origin")
			expect.Equal(t, got.Extensions, []checkpoint.Extension{"ext"}, "a change to text must not change a line")
		})

		t.Run("returns the body that AppendText writes", func(t *testing.T) {
			t.Parallel()
			prop.RoundTrip(t, func(b checkpoint.Body) ([]byte, error) { return b.AppendText(nil) },
				checkpoint.ParseBody, "ParseBody must return the body that AppendText writes", prop.Using(bodies),
				assert.EquateEmpty())
		})

		t.Run("returns only bodies that AppendText writes back byte for byte", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "ParseBody must return only bodies that AppendText writes back byte for byte",
				func(c *prop.Case) {
					text := c.Draw(changedBodies, "text")
					b, err := checkpoint.ParseBody(text)
					if err != nil {
						return
					}
					c.Classify(parsedBody)

					again, err := b.AppendText(nil)
					assert.NoError(c, err, "AppendText must accept a body that ParseBody returns")
					assert.Equal(c, again, text, "AppendText must write back the body that ParseBody returned")
				}, prop.Cases(1_000), prop.Require(parsedBody, 0.05))
		})

		tests := []struct {
			name string
			give string
		}{
			{name: "returns ErrBody for an empty text", give: ""},
			{name: "returns ErrBody for a text of two lines", give: "a\n1\n"},
			{name: "returns ErrBody for a text without its last newline", give: exampleBody + "ext"},
			{name: "returns ErrBody for a text that is not valid UTF-8", give: "a\xff\n1\n" + exampleRoot + "\n"},
			{name: "returns ErrBody for a text with a carriage return", give: "a\r\n1\n" + exampleRoot + "\n"},
			{name: "returns ErrBody for a text with U+001F", give: "a\x1f\n1\n" + exampleRoot + "\n"},
			{name: "returns ErrBody for an empty origin", give: "\n1\n" + exampleRoot + "\n"},
			{name: "returns ErrBody for an empty size", give: "a\n\n" + exampleRoot + "\n"},
			{name: "returns ErrBody for a size with a leading zero", give: "a\n01\n" + exampleRoot + "\n"},
			{name: "returns ErrBody for a size of two zeros", give: "a\n00\n" + exampleRoot + "\n"},
			{name: "returns ErrBody for a size with a sign", give: "a\n+1\n" + exampleRoot + "\n"},
			{name: "returns ErrBody for a size in hexadecimal", give: "a\n0x1\n" + exampleRoot + "\n"},
			{
				name: "returns ErrBody for a size above the largest uint64",
				give: "a\n18446744073709551616\n" + exampleRoot + "\n",
			},
			{name: "returns ErrBody for a root that is not base64", give: "a\n1\n!!!!\n"},
			{name: "returns ErrBody for an empty root", give: "a\n1\n\n"},
			{
				name: "returns ErrBody for a root whose base64 has spare bits",
				give: "a\n1\n" + strings.Replace(exampleRoot, "q2I=", "q2J=", 1) + "\n",
			},
			{
				name: "returns ErrBody for a root without its padding",
				give: "a\n1\n" + strings.TrimSuffix(exampleRoot, "=") + "\n",
			},
			{
				name: "returns ErrBody for a root of 31 bytes",
				give: "a\n1\n" + base64.StdEncoding.EncodeToString(make([]byte, 31)) + "\n",
			},
			{
				name: "returns ErrBody for a root of 33 bytes",
				give: "a\n1\n" + base64.StdEncoding.EncodeToString(make([]byte, 33)) + "\n",
			},
			{
				name: "returns ErrBody for a root of 66 bytes",
				give: "a\n1\n" + base64.StdEncoding.EncodeToString(make([]byte, 66)) + "\n",
			},
			{
				name: "returns ErrBody for a root of 69 bytes",
				give: "a\n1\n" + base64.StdEncoding.EncodeToString(make([]byte, 69)) + "\n",
			},
			{name: "returns ErrBody for an empty extension line", give: exampleBody + "\n"},
			{name: "returns ErrBody for an empty line between extension lines", give: exampleBody + "a\n\nb\n"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				got, err := checkpoint.ParseBody([]byte(tt.give))
				expect.ErrorIs(t, err, checkpoint.ErrBody, "ParseBody must refuse the text")
				expect.Equal(t, errs.Classify(err), errs.Invalid, "the error must classify as Invalid")
				expect.Equal(t, got, checkpoint.Body{}, "ParseBody must return the zero Body with an error")
			})
		}
	})

	t.Run("Valid", func(t *testing.T) {
		t.Parallel()

		valid := checkpoint.Body{Origin: exampleOrigin, Size: exampleSize, Root: exampleDigest(t)}
		withExtensions := valid
		withExtensions.Extensions = []checkpoint.Extension{"first", "second"}
		emptyExtension := valid
		emptyExtension.Extensions = []checkpoint.Extension{"first", ""}
		emptyOrigin := valid
		emptyOrigin.Origin = ""
		zeroRoot := valid
		zeroRoot.Root = crypto.Digest{}

		tests := []struct {
			name string
			give checkpoint.Body
			want bool
		}{
			{name: "reports true for the body of the example", give: valid, want: true},
			{name: "reports true for a body with extension lines", give: withExtensions, want: true},
			{name: "reports false for the zero Body", give: checkpoint.Body{}, want: false},
			{name: "reports false for an empty origin", give: emptyOrigin, want: false},
			{name: "reports false for the zero root", give: zeroRoot, want: false},
			{name: "reports false for an empty extension line after a valid one", give: emptyExtension, want: false},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				assert.Equal(t, tt.give.Valid(), tt.want, "Valid must report whether the body is complete")
			})
		}
	})

	t.Run("AppendText", func(t *testing.T) {
		t.Parallel()

		t.Run("appends the text of the example of tlog-checkpoint", func(t *testing.T) {
			t.Parallel()
			b := checkpoint.Body{Origin: exampleOrigin, Size: exampleSize, Root: exampleDigest(t)}
			got, err := b.AppendText([]byte("body:"))
			assert.NoError(t, err, "AppendText must accept a valid body")
			assert.Equal(t, string(got), "body:"+exampleBody, "AppendText must append the three lines")
		})

		t.Run("appends each extension line", func(t *testing.T) {
			t.Parallel()
			b := checkpoint.Body{Origin: exampleOrigin, Size: exampleSize, Root: exampleDigest(t)}
			b.Extensions = []checkpoint.Extension{"first", "second line"}
			got, err := b.AppendText(nil)
			assert.NoError(t, err, "AppendText must accept extension lines")
			assert.Equal(t, string(got), exampleBody+"first\nsecond line\n", "AppendText must append the lines")
		})

		t.Run("returns ErrBody with dst unchanged for a Body that is not Valid", func(t *testing.T) {
			t.Parallel()
			got, err := checkpoint.Body{Origin: exampleOrigin}.AppendText([]byte("body:"))
			expect.ErrorIs(t, err, checkpoint.ErrBody, "AppendText must refuse a body without a root")
			expect.Equal(t, errs.Classify(err), errs.Invalid, "the error must classify as Invalid")
			expect.Equal(t, string(got), "body:", "AppendText must return dst unchanged")
		})
	})

	t.Run("MarshalText", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the text that AppendText appends", func(t *testing.T) {
			t.Parallel()
			got, err := checkpoint.Body{Origin: exampleOrigin, Size: exampleSize, Root: exampleDigest(t)}.MarshalText()
			assert.NoError(t, err, "MarshalText must accept a valid body")
			assert.Equal(t, string(got), exampleBody, "MarshalText must return the text")
		})

		t.Run("returns a text whose capacity is its length", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "MarshalText must return a text whose capacity is its length", func(c *prop.Case) {
				got, err := c.Draw(bodies, "body").MarshalText()
				assert.NoError(c, err, "MarshalText must accept a valid body")
				assert.Equal(c, cap(got), len(got), "MarshalText must allocate the text at its length")
			})
		})

		t.Run("returns nil with ErrBody for a Body that is not Valid", func(t *testing.T) {
			t.Parallel()
			got, err := checkpoint.Body{}.MarshalText()
			expect.ErrorIs(t, err, checkpoint.ErrBody, "MarshalText must refuse the zero Body")
			expect.Nil(t, got, "MarshalText must return nil with an error")
		})
	})

	t.Run("UnmarshalText", func(t *testing.T) {
		t.Parallel()

		t.Run("sets b to the body of the text", func(t *testing.T) {
			t.Parallel()
			var b checkpoint.Body
			assert.NoError(t, b.UnmarshalText([]byte(exampleBody)), "UnmarshalText must accept the example")
			assert.Equal(t, b, checkpoint.Body{Origin: exampleOrigin, Size: exampleSize, Root: exampleDigest(t)},
				"UnmarshalText must set the body")
		})

		t.Run("sets b to the body of each text in turn", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "UnmarshalText must set b to the body of each text in turn", func(c *prop.Case) {
				var b checkpoint.Body
				for _, want := range c.Draw(prop.List(bodies, prop.MinSize(1), prop.MaxSize(4)), "bodies") {
					text, err := want.AppendText(nil)
					assert.NoError(c, err, "AppendText must accept a valid body")
					assert.NoError(c, b.UnmarshalText(text),
						"UnmarshalText must accept the text that AppendText writes")
					assert.Equal(c, b, want, "UnmarshalText must set the body that AppendText wrote",
						assert.EquateEmpty())
				}
			})
		})

		t.Run("sets b to the body of another origin", func(t *testing.T) {
			t.Parallel()
			var b checkpoint.Body
			assert.NoError(t, b.UnmarshalText([]byte(exampleBody+"first\n")), "UnmarshalText must accept the body")
			assert.NoError(t, b.UnmarshalText([]byte("other\n1\n"+exampleRoot+"\nfirst\nsecond\n")),
				"UnmarshalText must accept the body")
			assert.Equal(t, b, checkpoint.Body{
				Origin: "other", Size: 1, Root: exampleDigest(t), Extensions: []checkpoint.Extension{"first", "second"},
			}, "UnmarshalText must set the origin and each line")
		})

		t.Run("reuses the slice of extension lines of b", func(t *testing.T) {
			t.Parallel()
			var b checkpoint.Body
			assert.NoError(t, b.UnmarshalText([]byte(exampleBody+"first\nsecond\n")),
				"UnmarshalText must accept the body")
			first := &b.Extensions[0]
			assert.NoError(t, b.UnmarshalText([]byte(exampleBody+"first\nother\n")),
				"UnmarshalText must accept the body")
			expect.Equal(t, &b.Extensions[0], first, "UnmarshalText must reuse the slice of extension lines",
				expect.ByIdentity())
			expect.Equal(t, b.Extensions, []checkpoint.Extension{"first", "other"}, "UnmarshalText must set each line")
		})

		t.Run("sets Extensions to an empty slice with the capacity of b for a body without extension lines",
			func(t *testing.T) {
				t.Parallel()
				var b checkpoint.Body
				assert.NoError(t, b.UnmarshalText([]byte(exampleBody+"first\n")), "UnmarshalText must accept the body")
				assert.NoError(t, b.UnmarshalText([]byte(exampleBody)), "UnmarshalText must accept the body")
				expect.Empty(t, b.Extensions, "UnmarshalText must set no extension line")
				expect.Equal(t, cap(b.Extensions), 1, "UnmarshalText must keep the capacity of b")
			})

		t.Run("returns the error of ParseBody with b unchanged", func(t *testing.T) {
			t.Parallel()
			want := checkpoint.Body{
				Origin: exampleOrigin, Size: exampleSize, Root: exampleDigest(t),
				Extensions: []checkpoint.Extension{"line"},
			}
			b := want
			err := b.UnmarshalText([]byte(exampleBody + "\n"))
			expect.ErrorIs(t, err, checkpoint.ErrBody, "UnmarshalText must refuse a text that is not a body")
			expect.Equal(t, b, want, "UnmarshalText must leave b unchanged")
		})
	})
}

// TestBodyAllocs checks the allocation contract of each function and
// method. MaxAllocs counts the allocations of the whole process, so the
// test does not run in parallel.
func TestBodyAllocs(t *testing.T) {
	text := []byte(exampleBody + "an extension line\n")
	body := checkpoint.Body{
		Origin: exampleOrigin, Size: exampleSize, Root: exampleDigest(t),
		Extensions: []checkpoint.Extension{"an extension line"},
	}

	t.Run("Origin", func(t *testing.T) {
		t.Run("Valid", func(t *testing.T) {
			var got bool
			expect.MaxAllocs(t, func() { got = body.Origin.Valid() }, 0, "Valid must not allocate")
			assert.True(t, got, "the test must measure a valid origin")
		})
	})

	t.Run("Extension", func(t *testing.T) {
		t.Run("Valid", func(t *testing.T) {
			var got bool
			expect.MaxAllocs(t, func() { got = body.Extensions[0].Valid() }, 0, "Valid must not allocate")
			assert.True(t, got, "the test must measure a valid extension line")
		})
	})

	t.Run("ParseBody", func(t *testing.T) {
		t.Run("of a body with an extension line", func(t *testing.T) {
			var err error
			expect.MaxAllocs(t, func() { _, err = checkpoint.ParseBody(text) }, 2,
				"ParseBody must allocate the string of the text and the extension lines alone")
			assert.NoError(t, err, "the test must measure a body that ParseBody accepts")
		})

		t.Run("of a body without extension lines", func(t *testing.T) {
			three := []byte(exampleBody)

			var err error
			expect.MaxAllocs(t, func() { _, err = checkpoint.ParseBody(three) }, 1,
				"ParseBody must allocate the string of the text alone")
			assert.NoError(t, err, "the test must measure a body that ParseBody accepts")
		})
	})

	t.Run("Valid", func(t *testing.T) {
		var got bool
		expect.MaxAllocs(t, func() { got = body.Valid() }, 0, "Valid must not allocate")
		assert.True(t, got, "the test must measure a valid body")
	})

	t.Run("AppendText", func(t *testing.T) {
		buf := make([]byte, 0, len(text))

		var err error
		expect.MaxAllocs(t, func() { _, err = body.AppendText(buf[:0]) }, 0,
			"AppendText must not allocate into a buffer with room")
		assert.NoError(t, err, "the test must measure a valid body")
	})

	t.Run("MarshalText", func(t *testing.T) {
		var err error
		expect.MaxAllocs(t, func() { _, err = body.MarshalText() }, 1, "MarshalText must allocate the text alone")
		assert.NoError(t, err, "the test must measure a valid body")
	})

	t.Run("UnmarshalText", func(t *testing.T) {
		t.Run("of the body of b", func(t *testing.T) {
			var reused checkpoint.Body
			assert.NoError(t, reused.UnmarshalText(text), "UnmarshalText must accept the body")

			var err error
			expect.MaxAllocs(t, func() { err = reused.UnmarshalText(text) }, 0,
				"UnmarshalText must not allocate for the body of b")
			assert.NoError(t, err, "the test must measure a body that UnmarshalText accepts")
		})

		t.Run("of two bodies in turn with two other extension lines", func(t *testing.T) {
			first := []byte(exampleBody + "a first line\na second line\n")
			second := []byte(exampleBody + "another first line\nanother second line\n")

			var reused checkpoint.Body

			var err error
			expect.MaxAllocs(t, func() {
				_ = reused.UnmarshalText(first)
				err = reused.UnmarshalText(second)
			}, 2, "UnmarshalText must allocate the string of each text once")
			assert.NoError(t, err, "the test must measure a body that UnmarshalText accepts")
		})
	})
}

// BenchmarkBody reports the cost of each function and method, and fails
// above the allocations that their contracts state.
func BenchmarkBody(b *testing.B) {
	text := []byte(exampleBody + "an extension line\n")
	body := checkpoint.Body{
		Origin: exampleOrigin, Size: exampleSize, Root: exampleDigest(b),
		Extensions: []checkpoint.Extension{"an extension line"},
	}

	b.Run("Origin", func(b *testing.B) {
		b.Run("Valid", func(b *testing.B) {
			var got bool

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				got = body.Origin.Valid()
			}

			assert.True(b, got, "the benchmark must measure a valid origin")
		})
	})

	b.Run("Extension", func(b *testing.B) {
		b.Run("Valid", func(b *testing.B) {
			var got bool

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				got = body.Extensions[0].Valid()
			}

			assert.True(b, got, "the benchmark must measure a valid extension line")
		})
	})

	b.Run("ParseBody", func(b *testing.B) {
		b.Run("of a body with an extension line", func(b *testing.B) {
			var err error

			c := bench.Start(b).MaxAllocs(2)
			defer c.End()

			for c.Loop() {
				_, err = checkpoint.ParseBody(text)
			}

			assert.NoError(b, err, "the benchmark must measure a body that ParseBody accepts")
		})

		b.Run("of a body without extension lines", func(b *testing.B) {
			three := []byte(exampleBody)

			var err error

			c := bench.Start(b).MaxAllocs(1)
			defer c.End()

			for c.Loop() {
				_, err = checkpoint.ParseBody(three)
			}

			assert.NoError(b, err, "the benchmark must measure a body that ParseBody accepts")
		})
	})

	b.Run("Valid", func(b *testing.B) {
		var got bool

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = body.Valid()
		}

		assert.True(b, got, "the benchmark must measure a valid body")
	})

	b.Run("AppendText", func(b *testing.B) {
		buf := make([]byte, 0, len(text))

		var err error

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			_, err = body.AppendText(buf[:0])
		}

		assert.NoError(b, err, "the benchmark must measure a valid body")
	})

	b.Run("MarshalText", func(b *testing.B) {
		var err error

		c := bench.Start(b).MaxAllocs(1)
		defer c.End()

		for c.Loop() {
			_, err = body.MarshalText()
		}

		assert.NoError(b, err, "the benchmark must measure a valid body")
	})

	b.Run("UnmarshalText", func(b *testing.B) {
		b.Run("of the body of b", func(b *testing.B) {
			var reused checkpoint.Body
			assert.NoError(b, reused.UnmarshalText(text), "UnmarshalText must accept the body")

			var err error

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				err = reused.UnmarshalText(text)
			}

			assert.NoError(b, err, "the benchmark must measure a body that UnmarshalText accepts")
		})

		b.Run("of two bodies in turn with two other extension lines", func(b *testing.B) {
			first := []byte(exampleBody + "a first line\na second line\n")
			second := []byte(exampleBody + "another first line\nanother second line\n")

			var reused checkpoint.Body

			var err error

			c := bench.Start(b).MaxAllocs(2)
			defer c.End()

			for c.Loop() {
				_ = reused.UnmarshalText(first)
				err = reused.UnmarshalText(second)
			}

			assert.NoError(b, err, "the benchmark must measure a body that UnmarshalText accepts")
		})
	})
}

// readFile returns the content of the file name of testdata.
func readFile(tb testing.TB, name string) []byte {
	tb.Helper()

	b, err := os.ReadFile("testdata/" + name)
	assert.NoError(tb, err, "the fixture "+name+" must be readable")

	return b
}

// root returns the root of n bytes whose byte i is i + 1.
func root(tb testing.TB, n int) crypto.Digest {
	tb.Helper()

	raw := make([]byte, n)
	for i := range raw {
		raw[i] = byte(i + 1)
	}

	d, err := crypto.DigestFromBytes(raw)
	assert.NoError(tb, err, "the root must have the size of a digest")

	return d
}

// exampleDigest returns the root of the example of tlog-checkpoint.
func exampleDigest(tb testing.TB) crypto.Digest {
	tb.Helper()

	raw, err := base64.StdEncoding.DecodeString(exampleRoot)
	assert.NoError(tb, err, "the fixture must decode")

	d, err := crypto.DigestFromBytes(raw)
	assert.NoError(tb, err, "the fixture must be 32 bytes")

	return d
}
