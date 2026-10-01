// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package checkpoint_test

import (
	"encoding/base64"
	"math/rand/v2"
	"os"
	"strings"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/crypto/sign"
	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/note"
	"go.thesmos.sh/core/tlog/checkpoint"
)

// The body of the example of tlog-checkpoint.
const (
	exampleOrigin = "example.com/behind-the-sofa"
	exampleSize   = 20852163
	exampleRoot   = "CsUYapGGPo4dkMgIAUqom/Xajj7h2fB2MPA3j2jxq2I="
	exampleBody   = exampleOrigin + "\n20852163\n" + exampleRoot + "\n"
)

const (
	// benchRuns is the number of calls over which a benchmark averages the
	// allocations that it checks.
	benchRuns = 100

	// properties is the number of random values of each property test.
	properties = 2000
)

// Sinks receive the results of the benchmarks, so that the compiler keeps
// every call that they measure.
var (
	sinkBool     bool
	sinkBytes    []byte
	sinkBody     checkpoint.Body
	sinkPolicy   checkpoint.Policy
	sinkRule     sign.Rule
	sinkVerifier note.Verifier
	sinkSigner   note.Signer
	sinkChecker  *checkpoint.Verifier
	errSink      error
)

// lineRunes are the characters of the lines that randomBody draws: a
// space, a plus, characters outside ASCII, and U+007F to U+009F.
var lineRunes = []rune("ab +/例λ\u007f\u0080\u009f")

// benchZeroAlloc reports the cost of call, and fails when call allocates.
func benchZeroAlloc(b *testing.B, call func()) {
	b.Helper()
	benchAllocs(b, 0, call)
}

// benchAllocs reports the cost of call, and fails when call does not
// allocate want times per call.
func benchAllocs(b *testing.B, want float64, call func()) {
	b.Helper()

	if allocs := testing.AllocsPerRun(benchRuns, call); allocs != want {
		b.Fatalf("allocates %v times per call, want %v", allocs, want)
	}

	b.ReportAllocs()
	for b.Loop() {
		call()
	}
}

// readFile returns the content of the file name of testdata.
func readFile(tb testing.TB, name string) []byte {
	tb.Helper()

	b, err := os.ReadFile("testdata/" + name)
	testkit.NoError(tb, err, "the fixture "+name+" must be readable")

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
	testkit.NoError(tb, err, "the root must have the size of a digest")

	return d
}

// encode returns the padded standard base64 of the bytes of d.
func encode(d crypto.Digest) string {
	return base64.StdEncoding.EncodeToString(d.Bytes())
}

// exampleDigest returns the root of the example of tlog-checkpoint.
func exampleDigest(tb testing.TB) crypto.Digest {
	tb.Helper()

	raw, err := base64.StdEncoding.DecodeString(exampleRoot)
	testkit.NoError(tb, err, "the fixture must decode")

	d, err := crypto.DigestFromBytes(raw)
	testkit.NoError(tb, err, "the fixture must be 32 bytes")

	return d
}

// randomLine returns a valid line of 1 to 20 characters of lineRunes.
func randomLine(r *rand.Rand) string {
	var s strings.Builder
	for range 1 + r.IntN(20) {
		s.WriteRune(lineRunes[r.IntN(len(lineRunes))])
	}

	return s.String()
}

// randomBody returns a valid body: a random origin and size, a root of 32,
// 48 or 64 random bytes, and zero to two extension lines.
func randomBody(tb testing.TB, r *rand.Rand) checkpoint.Body {
	tb.Helper()

	raw := make([]byte, []int{32, 48, 64}[r.IntN(3)])
	for i := range raw {
		raw[i] = byte(r.Uint32())
	}

	d, err := crypto.DigestFromBytes(raw)
	testkit.NoError(tb, err, "the root must have the size of a digest")

	b := checkpoint.Body{Origin: checkpoint.Origin(randomLine(r)), Size: r.Uint64() >> r.IntN(64), Root: d}
	for range r.IntN(3) {
		b.Extensions = append(b.Extensions, checkpoint.Extension(randomLine(r)))
	}

	return b
}

func TestBody(t *testing.T) {
	t.Parallel()

	t.Run("Origin", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			give checkpoint.Origin
			want bool
		}{
			{name: "reports true for the origin of the example of tlog-checkpoint", give: exampleOrigin, want: true},
			{name: "reports true for an origin with a space and a plus", give: "a b+c", want: true},
			{name: "reports true for U+007F and U+0080 to U+009F", give: "a\u007f\u0080\u009f", want: true},
			{name: "reports false for the empty origin", give: "", want: false},
			{name: "reports false for an origin that is not valid UTF-8", give: "a\xff", want: false},
			{name: "reports false for an origin with a newline", give: "a\nb", want: false},
			{name: "reports false for an origin with U+001F", give: "a\x1f", want: false},
			{name: "reports false for an origin with a NUL", give: "\x00", want: false},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				testkit.Equal(t, tt.give.Valid(), tt.want, "Valid must report whether the origin is a line")
			})
		}
	})

	t.Run("Extension", func(t *testing.T) {
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
				testkit.Equal(t, tt.give.Valid(), tt.want, "Valid must report whether the extension is a line")
			})
		}
	})

	t.Run("ParseBody", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the body of the example of tlog-checkpoint", func(t *testing.T) {
			t.Parallel()
			got, err := checkpoint.ParseBody([]byte(exampleBody))
			testkit.NoError(t, err, "ParseBody must accept the example")
			want := checkpoint.Body{Origin: exampleOrigin, Size: exampleSize, Root: exampleDigest(t)}
			testkit.Equal(t, got, want, "ParseBody must return the origin, the size and the root")
		})

		t.Run("returns the extension lines in order", func(t *testing.T) {
			t.Parallel()
			got, err := checkpoint.ParseBody([]byte(exampleBody + "first\nsecond line\n"))
			testkit.NoError(t, err, "ParseBody must accept extension lines")
			testkit.Equal(t, got.Extensions, []checkpoint.Extension{"first", "second line"},
				"ParseBody must return each extension line without its newline")
		})

		t.Run("returns the size 0", func(t *testing.T) {
			t.Parallel()
			got, err := checkpoint.ParseBody([]byte("a\n0\n" + exampleRoot + "\n"))
			testkit.NoError(t, err, "ParseBody must accept the size 0")
			testkit.Equal(t, got.Size, uint64(0), "ParseBody must return the size 0")
		})

		t.Run("returns the largest uint64", func(t *testing.T) {
			t.Parallel()
			got, err := checkpoint.ParseBody([]byte("a\n18446744073709551615\n" + exampleRoot + "\n"))
			testkit.NoError(t, err, "ParseBody must accept the largest uint64")
			testkit.Equal(t, got.Size, ^uint64(0), "ParseBody must return the largest uint64")
		})

		t.Run("returns a root of 48 bytes", func(t *testing.T) {
			t.Parallel()
			got, err := checkpoint.ParseBody([]byte("a\n1\n" + encode(root(t, 48)) + "\n"))
			testkit.NoError(t, err, "ParseBody must accept a root of 48 bytes")
			testkit.Equal(t, got.Root, root(t, 48), "ParseBody must return the root of 48 bytes")
		})

		t.Run("returns a root of 64 bytes", func(t *testing.T) {
			t.Parallel()
			got, err := checkpoint.ParseBody([]byte("a\n1\n" + encode(root(t, 64)) + "\n"))
			testkit.NoError(t, err, "ParseBody must accept a root of 64 bytes")
			testkit.Equal(t, got.Root, root(t, 64), "ParseBody must return the root of 64 bytes")
		})

		t.Run("returns a body that does not alias text", func(t *testing.T) {
			t.Parallel()
			text := []byte(exampleBody + "ext\n")
			got, err := checkpoint.ParseBody(text)
			testkit.NoError(t, err, "ParseBody must accept the body")
			clear(text)
			testkit.Equal(
				t,
				got.Origin,
				checkpoint.Origin(exampleOrigin),
				"a change to text must not change the origin",
			)
			testkit.Equal(t, got.Extensions, []checkpoint.Extension{"ext"}, "a change to text must not change a line")
		})

		t.Run("accepts exactly the texts that AppendText writes", func(t *testing.T) {
			t.Parallel()
			r := testkit.SeededRand(t)
			for range properties {
				b := randomBody(t, r)
				text, err := b.AppendText(nil)
				testkit.NoError(t, err, "AppendText must accept a valid body")
				got, err := checkpoint.ParseBody(text)
				testkit.NoError(t, err, "ParseBody must accept "+string(text))
				testkit.Equal(t, got, b, "ParseBody must return the body that AppendText wrote")

				text[r.IntN(len(text))] = byte(r.Uint32())
				if changed, err := checkpoint.ParseBody(text); err == nil {
					again, err := changed.AppendText(nil)
					testkit.NoError(t, err, "AppendText must accept a body that ParseBody returns")
					testkit.Equal(t, string(again), string(text), "ParseBody must accept only what AppendText writes")
				}
			}
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
				testkit.ErrorIs(t, err, checkpoint.ErrBody, "ParseBody must refuse the text")
				testkit.Equal(t, errs.Classify(err), errs.Invalid, "the error must classify as Invalid")
				testkit.Equal(t, got, checkpoint.Body{}, "ParseBody must return the zero Body with an error")
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
				testkit.Equal(t, tt.give.Valid(), tt.want, "Valid must report whether the body is complete")
			})
		}
	})

	t.Run("AppendText", func(t *testing.T) {
		t.Parallel()

		t.Run("appends the text of the example of tlog-checkpoint", func(t *testing.T) {
			t.Parallel()
			b := checkpoint.Body{Origin: exampleOrigin, Size: exampleSize, Root: exampleDigest(t)}
			got, err := b.AppendText([]byte("body:"))
			testkit.NoError(t, err, "AppendText must accept a valid body")
			testkit.Equal(t, string(got), "body:"+exampleBody, "AppendText must append the three lines")
		})

		t.Run("appends each extension line", func(t *testing.T) {
			t.Parallel()
			b := checkpoint.Body{Origin: exampleOrigin, Size: exampleSize, Root: exampleDigest(t)}
			b.Extensions = []checkpoint.Extension{"first", "second line"}
			got, err := b.AppendText(nil)
			testkit.NoError(t, err, "AppendText must accept extension lines")
			testkit.Equal(t, string(got), exampleBody+"first\nsecond line\n", "AppendText must append the lines")
		})

		t.Run("returns dst unchanged and ErrBody for a Body that is not Valid", func(t *testing.T) {
			t.Parallel()
			got, err := checkpoint.Body{Origin: exampleOrigin}.AppendText([]byte("body:"))
			testkit.ErrorIs(t, err, checkpoint.ErrBody, "AppendText must refuse a body without a root")
			testkit.Equal(t, errs.Classify(err), errs.Invalid, "the error must classify as Invalid")
			testkit.Equal(t, string(got), "body:", "AppendText must return dst unchanged")
		})
	})

	t.Run("MarshalText", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the text that AppendText appends", func(t *testing.T) {
			t.Parallel()
			got, err := checkpoint.Body{Origin: exampleOrigin, Size: exampleSize, Root: exampleDigest(t)}.MarshalText()
			testkit.NoError(t, err, "MarshalText must accept a valid body")
			testkit.Equal(t, string(got), exampleBody, "MarshalText must return the text")
		})

		t.Run("returns a text whose capacity is its length", func(t *testing.T) {
			t.Parallel()
			r := testkit.SeededRand(t)
			for range properties {
				got, err := randomBody(t, r).MarshalText()
				testkit.NoError(t, err, "MarshalText must accept a valid body")
				testkit.Equal(t, cap(got), len(got), "MarshalText must allocate the text at its length")
			}
		})

		t.Run("returns nil and ErrBody for a Body that is not Valid", func(t *testing.T) {
			t.Parallel()
			got, err := checkpoint.Body{}.MarshalText()
			testkit.ErrorIs(t, err, checkpoint.ErrBody, "MarshalText must refuse the zero Body")
			testkit.True(t, got == nil, "MarshalText must return nil with an error")
		})
	})

	t.Run("UnmarshalText", func(t *testing.T) {
		t.Parallel()

		t.Run("sets b to the body of the text", func(t *testing.T) {
			t.Parallel()
			var b checkpoint.Body
			testkit.NoError(t, b.UnmarshalText([]byte(exampleBody)), "UnmarshalText must accept the example")
			testkit.Equal(t, b, checkpoint.Body{Origin: exampleOrigin, Size: exampleSize, Root: exampleDigest(t)},
				"UnmarshalText must set the body")
		})

		t.Run("sets b to the body of each text in turn", func(t *testing.T) {
			t.Parallel()
			r := testkit.SeededRand(t)
			var b checkpoint.Body
			for range properties {
				want := randomBody(t, r)
				text, err := want.AppendText(nil)
				testkit.NoError(t, err, "AppendText must accept a valid body")
				testkit.NoError(t, b.UnmarshalText(text), "UnmarshalText must accept "+string(text))
				testkit.Equal(t, b.Origin, want.Origin, "UnmarshalText must set the origin")
				testkit.Equal(t, b.Size, want.Size, "UnmarshalText must set the size")
				testkit.Equal(t, b.Root, want.Root, "UnmarshalText must set the root")
				testkit.Equal(t, len(b.Extensions), len(want.Extensions), "UnmarshalText must set every extension line")
				for i, e := range want.Extensions {
					testkit.Equal(t, b.Extensions[i], e, "UnmarshalText must set each extension line")
				}
			}
		})

		t.Run("sets b to the body of another origin", func(t *testing.T) {
			t.Parallel()
			var b checkpoint.Body
			testkit.NoError(t, b.UnmarshalText([]byte(exampleBody+"first\n")), "UnmarshalText must accept the body")
			testkit.NoError(t, b.UnmarshalText([]byte("other\n1\n"+exampleRoot+"\nfirst\nsecond\n")),
				"UnmarshalText must accept the body")
			testkit.Equal(t, b, checkpoint.Body{
				Origin: "other", Size: 1, Root: exampleDigest(t), Extensions: []checkpoint.Extension{"first", "second"},
			}, "UnmarshalText must set the origin and each line")
		})

		t.Run("reuses the slice of extension lines of b", func(t *testing.T) {
			t.Parallel()
			var b checkpoint.Body
			testkit.NoError(
				t,
				b.UnmarshalText([]byte(exampleBody+"first\nsecond\n")),
				"UnmarshalText must accept the body",
			)
			first := &b.Extensions[0]
			testkit.NoError(
				t,
				b.UnmarshalText([]byte(exampleBody+"first\nother\n")),
				"UnmarshalText must accept the body",
			)
			testkit.True(t, &b.Extensions[0] == first, "UnmarshalText must reuse the slice of extension lines")
			testkit.Equal(t, b.Extensions, []checkpoint.Extension{"first", "other"}, "UnmarshalText must set each line")
		})

		t.Run("sets Extensions to an empty slice with the capacity of b for a body without extension lines",
			func(t *testing.T) {
				t.Parallel()
				var b checkpoint.Body
				testkit.NoError(t, b.UnmarshalText([]byte(exampleBody+"first\n")), "UnmarshalText must accept the body")
				testkit.NoError(t, b.UnmarshalText([]byte(exampleBody)), "UnmarshalText must accept the body")
				testkit.Len(t, b.Extensions, 0, "UnmarshalText must set no extension line")
				testkit.Equal(t, cap(b.Extensions), 1, "UnmarshalText must keep the capacity of b")
			})

		t.Run("returns the error of ParseBody and leaves b unchanged", func(t *testing.T) {
			t.Parallel()
			want := checkpoint.Body{
				Origin: exampleOrigin, Size: exampleSize, Root: exampleDigest(t),
				Extensions: []checkpoint.Extension{"line"},
			}
			b := want
			err := b.UnmarshalText([]byte(exampleBody + "\n"))
			testkit.ErrorIs(t, err, checkpoint.ErrBody, "UnmarshalText must refuse a text that is not a body")
			testkit.Equal(t, b, want, "UnmarshalText must leave b unchanged")
		})
	})
}

func BenchmarkBody(b *testing.B) {
	text := []byte(exampleBody + "an extension line\n")
	body := checkpoint.Body{
		Origin: exampleOrigin, Size: exampleSize, Root: exampleDigest(b),
		Extensions: []checkpoint.Extension{"an extension line"},
	}

	b.Run("Origin.Valid", func(b *testing.B) {
		benchZeroAlloc(b, func() { sinkBool = body.Origin.Valid() })
	})

	b.Run("Extension.Valid", func(b *testing.B) {
		benchZeroAlloc(b, func() { sinkBool = body.Extensions[0].Valid() })
	})

	b.Run("ParseBody", func(b *testing.B) {
		benchAllocs(b, 2, func() { sinkBody, errSink = checkpoint.ParseBody(text) })
	})

	b.Run("ParseBody without extension lines", func(b *testing.B) {
		three := []byte(exampleBody)
		benchAllocs(b, 1, func() { sinkBody, errSink = checkpoint.ParseBody(three) })
	})

	b.Run("Valid", func(b *testing.B) {
		benchZeroAlloc(b, func() { sinkBool = body.Valid() })
	})

	b.Run("AppendText", func(b *testing.B) {
		buf := make([]byte, 0, len(text))
		benchZeroAlloc(b, func() { sinkBytes, errSink = body.AppendText(buf[:0]) })
	})

	b.Run("MarshalText", func(b *testing.B) {
		benchAllocs(b, 1, func() { sinkBytes, errSink = body.MarshalText() })
	})

	b.Run("UnmarshalText", func(b *testing.B) {
		var reused checkpoint.Body
		testkit.NoError(b, reused.UnmarshalText(text), "UnmarshalText must accept the body")
		benchZeroAlloc(b, func() { errSink = reused.UnmarshalText(text) })
	})

	b.Run("UnmarshalText of two bodies in turn with other extension lines", func(b *testing.B) {
		other := []byte(exampleBody + "another extension line\n")
		var reused checkpoint.Body
		benchAllocs(b, 2, func() {
			errSink = reused.UnmarshalText(text)
			errSink = reused.UnmarshalText(other)
		})
	})
}
