// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package telemetry_test

import (
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"

	"go.thesmos.sh/core/telemetry"
)

// linkKey is the key of the attribute of a link of the cases.
const linkKey = "test.link"

// remote and other are span contexts that a propagator extracted from
// other processes.
var (
	remote = telemetry.SpanContext{
		TraceID: "4bf92f3577b34da6a3ce929d0e0e4736",
		SpanID:  "00f067aa0ba902b7",
		Sampled: true,
	}
	other = telemetry.SpanContext{
		TraceID: "0af7651916cd43dd8448eb211c80319c",
		SpanID:  "b7ad6b7169203331",
	}
)

// links are the links of the cases: one to the span of remote with an
// attribute, and one to the span of other.
var links = []telemetry.Link{
	{SpanContext: remote, Attributes: []telemetry.Attr{telemetry.AttrInt(linkKey, 1)}},
	{SpanContext: other},
}

// options are the options of the allocation ceilings: a kind, a remote
// parent and links.
var options = []telemetry.SpanOption{
	telemetry.WithSpanKind(telemetry.SpanKindServer),
	telemetry.WithRemoteParent(remote),
	telemetry.WithLinks(links),
}

func TestSpanOption(t *testing.T) {
	t.Parallel()

	t.Run("ApplySpanOptions", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			give []telemetry.SpanOption
			want telemetry.SpanKind
		}{
			{
				name: "returns SpanKindInternal for no options",
				want: telemetry.SpanKindInternal,
			},
			{
				name: "returns SpanKindInternal for an option of SpanKindUnspecified",
				give: []telemetry.SpanOption{telemetry.WithSpanKind(telemetry.SpanKindUnspecified)},
				want: telemetry.SpanKindInternal,
			},
			{
				name: "returns the kind of WithSpanKind",
				give: []telemetry.SpanOption{telemetry.WithSpanKind(telemetry.SpanKindConsumer)},
				want: telemetry.SpanKindConsumer,
			},
			{
				name: "returns the kind of the last option that sets one",
				give: []telemetry.SpanOption{
					telemetry.WithSpanKind(telemetry.SpanKindClient),
					telemetry.WithSpanKind(telemetry.SpanKindServer),
					telemetry.WithSpanKind(telemetry.SpanKindUnspecified),
				},
				want: telemetry.SpanKindServer,
			},
			{
				name: "returns the kind of an option before WithRemoteParent",
				give: []telemetry.SpanOption{
					telemetry.WithSpanKind(telemetry.SpanKindServer),
					telemetry.WithRemoteParent(remote),
				},
				want: telemetry.SpanKindServer,
			},
			{
				name: "returns the kind of an option before WithLinks",
				give: []telemetry.SpanOption{
					telemetry.WithSpanKind(telemetry.SpanKindServer),
					telemetry.WithLinks(links),
				},
				want: telemetry.SpanKindServer,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				assert.Equal(t, telemetry.ApplySpanOptions(tt.give), tt.want,
					"ApplySpanOptions must return the kind that the options set")
			})
		}
	})

	t.Run("RemoteParent", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name   string
			give   []telemetry.SpanOption
			want   telemetry.SpanContext
			wantOK bool
		}{
			{
				name: "reports false for no options",
			},
			{
				name: "reports false for WithSpanKind",
				give: []telemetry.SpanOption{telemetry.WithSpanKind(telemetry.SpanKindServer)},
			},
			{
				name: "reports false for a parent with an empty TraceID",
				give: []telemetry.SpanOption{telemetry.WithRemoteParent(telemetry.SpanContext{SpanID: remote.SpanID})},
			},
			{
				name: "reports false for WithLinks",
				give: []telemetry.SpanOption{telemetry.WithLinks(links)},
			},
			{
				name:   "returns the parent of WithRemoteParent",
				give:   []telemetry.SpanOption{telemetry.WithRemoteParent(remote)},
				want:   remote,
				wantOK: true,
			},
			{
				name: "returns the parent of the last option that sets one",
				give: []telemetry.SpanOption{
					telemetry.WithRemoteParent(other),
					telemetry.WithRemoteParent(remote),
					telemetry.WithRemoteParent(telemetry.SpanContext{}),
				},
				want:   remote,
				wantOK: true,
			},
			{
				name: "returns the parent of an option before WithSpanKind",
				give: []telemetry.SpanOption{
					telemetry.WithRemoteParent(remote),
					telemetry.WithSpanKind(telemetry.SpanKindServer),
				},
				want:   remote,
				wantOK: true,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				got, ok := telemetry.RemoteParent(tt.give)
				expect.Equal(t, ok, tt.wantOK, "RemoteParent must report whether an option set a parent")
				expect.Equal(t, got, tt.want, "RemoteParent must return the parent that the options set")
			})
		}
	})

	t.Run("Links", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			give []telemetry.SpanOption
			want []telemetry.Link
		}{
			{
				name: "returns nil for no options",
			},
			{
				name: "returns nil for WithSpanKind",
				give: []telemetry.SpanOption{telemetry.WithSpanKind(telemetry.SpanKindServer)},
			},
			{
				name: "returns nil for WithLinks of an empty slice",
				give: []telemetry.SpanOption{telemetry.WithLinks([]telemetry.Link{})},
			},
			{
				name: "returns the slice of WithLinks",
				give: []telemetry.SpanOption{telemetry.WithLinks(links)},
				want: links,
			},
			{
				name: "returns the slice of the last option that sets links",
				give: []telemetry.SpanOption{
					telemetry.WithLinks(links[1:]),
					telemetry.WithLinks(links),
					telemetry.WithLinks(nil),
				},
				want: links,
			},
			{
				name: "returns the links of an option before WithRemoteParent",
				give: []telemetry.SpanOption{
					telemetry.WithLinks(links),
					telemetry.WithRemoteParent(remote),
				},
				want: links,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				assert.Equal(t, telemetry.Links(tt.give), tt.want, "Links must return the slice that the options set",
					assert.ByIdentity())
			})
		}
	})
}

// TestSpanOptionAllocs checks the allocation contract of the options and
// of the functions that read them. MaxAllocs counts the allocations of the
// whole process, so the test does not run in parallel.
func TestSpanOptionAllocs(t *testing.T) {
	var opt telemetry.SpanOption

	t.Run("WithSpanKind", func(t *testing.T) {
		expect.MaxAllocs(t, func() { opt = telemetry.WithSpanKind(telemetry.SpanKindServer) }, 0,
			"WithSpanKind must not allocate")
		assert.Equal(t, telemetry.ApplySpanOptions([]telemetry.SpanOption{opt}), telemetry.SpanKindServer,
			"the test must measure an option of a kind")
	})

	t.Run("WithRemoteParent", func(t *testing.T) {
		expect.MaxAllocs(t, func() { opt = telemetry.WithRemoteParent(remote) }, 0,
			"WithRemoteParent must not allocate")
		_, ok := telemetry.RemoteParent([]telemetry.SpanOption{opt})
		assert.True(t, ok, "the test must measure an option of a parent")
	})

	t.Run("WithLinks", func(t *testing.T) {
		expect.MaxAllocs(t, func() { opt = telemetry.WithLinks(links) }, 0, "WithLinks must not allocate")
		assert.Length(t, telemetry.Links([]telemetry.SpanOption{opt}), len(links),
			"the test must measure an option of links")
	})

	t.Run("ApplySpanOptions", func(t *testing.T) {
		var kind telemetry.SpanKind
		expect.MaxAllocs(t, func() { kind = telemetry.ApplySpanOptions(options) }, 0,
			"ApplySpanOptions must not allocate")
		assert.Equal(t, kind, telemetry.SpanKindServer, "the test must measure an option that sets a kind")
	})

	t.Run("RemoteParent", func(t *testing.T) {
		var ok bool
		expect.MaxAllocs(t, func() { _, ok = telemetry.RemoteParent(options) }, 0, "RemoteParent must not allocate")
		assert.True(t, ok, "the test must measure an option that sets a parent")
	})

	t.Run("Links", func(t *testing.T) {
		var got []telemetry.Link
		expect.MaxAllocs(t, func() { got = telemetry.Links(options) }, 0, "Links must not allocate")
		assert.Length(t, got, len(links), "the test must measure an option that sets links")
	})
}

// BenchmarkSpanOption reports the cost of the options and of the functions
// that read them, and fails when one allocates.
func BenchmarkSpanOption(b *testing.B) {
	var opt telemetry.SpanOption

	b.Run("WithSpanKind", func(b *testing.B) {
		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			opt = telemetry.WithSpanKind(telemetry.SpanKindServer)
		}

		assert.Equal(b, telemetry.ApplySpanOptions([]telemetry.SpanOption{opt}), telemetry.SpanKindServer,
			"the benchmark must measure an option of a kind")
	})

	b.Run("WithRemoteParent", func(b *testing.B) {
		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			opt = telemetry.WithRemoteParent(remote)
		}

		_, ok := telemetry.RemoteParent([]telemetry.SpanOption{opt})
		assert.True(b, ok, "the benchmark must measure an option of a parent")
	})

	b.Run("WithLinks", func(b *testing.B) {
		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			opt = telemetry.WithLinks(links)
		}

		assert.Length(b, telemetry.Links([]telemetry.SpanOption{opt}), len(links),
			"the benchmark must measure an option of links")
	})

	b.Run("ApplySpanOptions", func(b *testing.B) {
		var kind telemetry.SpanKind

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			kind = telemetry.ApplySpanOptions(options)
		}

		assert.Equal(b, kind, telemetry.SpanKindServer, "the benchmark must measure an option that sets a kind")
	})

	b.Run("RemoteParent", func(b *testing.B) {
		var ok bool

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			_, ok = telemetry.RemoteParent(options)
		}

		assert.True(b, ok, "the benchmark must measure an option that sets a parent")
	})

	b.Run("Links", func(b *testing.B) {
		var got []telemetry.Link

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = telemetry.Links(options)
		}

		assert.Length(b, got, len(links), "the benchmark must measure an option that sets links")
	})
}
