package braintest

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math/rand/v2"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/zephyrtronium/robot/brain"
	"github.com/zephyrtronium/robot/userhash"
)

// BenchLearn runs benchmarks on the brain's speed with recording new tuples.
// The learner returned by new must be safe for concurrent use.
func BenchLearn(ctx context.Context, b *testing.B, new func(ctx context.Context, b *testing.B) brain.Learner, cleanup func(brain.Learner)) {
	b.Run("similar", func(b *testing.B) {
		l := new(ctx, b)
		if cleanup != nil {
			b.Cleanup(func() { cleanup(l) })
		}
		var msg brain.MessageMeta
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			var t int64
			toks := make([]string, 2+l.Order())
			for i := range toks {
				toks[i] = hex.EncodeToString(randbytes(make([]byte, 16)))
			}
			for pb.Next() {
				t++
				toks[len(toks)-1] = strconv.FormatInt(t, 10)
				msg = brain.MessageMeta{
					ID:   uuid.UUID(randbytes(make([]byte, len(uuid.UUID{})))),
					User: userhash.Hash(randbytes(make([]byte, len(userhash.Hash{})))),
					Tag:  "bocchi",
					Time: time.Unix(t, 0),
				}
				err := brain.Learn(ctx, l, &msg, toks)
				if err != nil {
					b.Errorf("error while learning: %v", err)
				}
			}
		})
	})
	b.Run("distinct", func(b *testing.B) {
		l := new(ctx, b)
		if cleanup != nil {
			b.Cleanup(func() { cleanup(l) })
		}
		var msg brain.MessageMeta
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			var t int64
			order := l.Order()
			toks := make([]string, 16+order)
			for i := range toks {
				toks[i] = hex.EncodeToString(randbytes(make([]byte, 16)))
			}
			for pb.Next() {
				t++
				rand.Shuffle(len(toks), func(i, j int) { toks[i], toks[j] = toks[j], toks[i] })
				msg = brain.MessageMeta{
					ID:   uuid.UUID(randbytes(make([]byte, len(uuid.UUID{})))),
					User: userhash.Hash(randbytes(make([]byte, len(userhash.Hash{})))),
					Tag:  "bocchi",
					Time: time.Unix(t, 0),
				}
				err := brain.Learn(ctx, l, &msg, toks[:2+order])
				if err != nil {
					b.Errorf("error while learning: %v", err)
				}
			}
		})
	})
}

// BenchSpeak runs benchmarks on a brain's speed with generating messages
// from tuples. The brain returned by new must be safe for concurrent use.
func BenchSpeak(ctx context.Context, b *testing.B, new func(ctx context.Context, b *testing.B) Interface, cleanup func(Interface)) {
	sizes := []int64{1e3, 1e4, 1e5}
	for _, size := range sizes {
		b.Run(fmt.Sprintf("similar-new-%d", size), func(b *testing.B) {
			br := new(ctx, b)
			if cleanup != nil {
				b.Cleanup(func() { cleanup(br) })
			}
			// First fill the brain.
			var msg brain.MessageMeta
			order := br.Order()
			toks := make([]string, 2+order)
			for i := range toks {
				toks[i] = hex.EncodeToString(randbytes(make([]byte, 16)))
			}
			for t := range size {
				toks[len(toks)-1] = strconv.FormatInt(t, 10)
				msg = brain.MessageMeta{
					ID:   uuid.UUID(randbytes(make([]byte, len(uuid.UUID{})))),
					User: userhash.Hash(randbytes(make([]byte, len(userhash.Hash{})))),
					Tag:  "bocchi",
					Time: time.Unix(t, 0),
				}
				err := brain.Learn(ctx, br, &msg, toks)
				if err != nil {
					b.Errorf("error while learning: %v", err)
				}
			}
			b.ReportAllocs()
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					if _, err := brain.Speak(ctx, br, "bocchi", ""); err != nil {
						b.Errorf("error while speaking: %v", err)
					}
				}
			})
		})
	}
	for _, size := range sizes {
		b.Run(fmt.Sprintf("distinct-new-%d", size), func(b *testing.B) {
			br := new(ctx, b)
			if cleanup != nil {
				b.Cleanup(func() { cleanup(br) })
			}
			// First fill the brain.
			var msg brain.MessageMeta
			order := br.Order()
			toks := make([]string, 16+order)
			for i := range toks {
				toks[i] = hex.EncodeToString(randbytes(make([]byte, 16)))
			}
			for t := range size {
				rand.Shuffle(len(toks), func(i, j int) { toks[i], toks[j] = toks[j], toks[i] })
				msg = brain.MessageMeta{
					ID:   uuid.UUID(randbytes(make([]byte, len(uuid.UUID{})))),
					User: userhash.Hash(randbytes(make([]byte, len(userhash.Hash{})))),
					Tag:  "bocchi",
					Time: time.Unix(t, 0),
				}
				err := brain.Learn(ctx, br, &msg, toks)
				if err != nil {
					b.Errorf("error while learning: %v", err)
				}
			}
			b.ReportAllocs()
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					if _, err := brain.Speak(ctx, br, "bocchi", ""); err != nil {
						b.Errorf("error while speaking: %v", err)
					}
				}
			})
		})
	}
	for _, size := range sizes {
		b.Run(fmt.Sprintf("distinct-prompted-%d", size), func(b *testing.B) {
			br := new(ctx, b)
			if cleanup != nil {
				b.Cleanup(func() { cleanup(br) })
			}
			// First fill the brain.
			var msg brain.MessageMeta
			order := br.Order()
			toks := make([]string, 16+order)
			for i := range toks {
				toks[i] = hex.EncodeToString(randbytes(make([]byte, 16)))
			}
			for t := range size {
				rand.Shuffle(len(toks), func(i, j int) { toks[i], toks[j] = toks[j], toks[i] })
				msg = brain.MessageMeta{
					ID:   uuid.UUID(randbytes(make([]byte, len(uuid.UUID{})))),
					User: userhash.Hash(randbytes(make([]byte, len(userhash.Hash{})))),
					Tag:  "bocchi",
					Time: time.Unix(t, 0),
				}
				err := brain.Learn(ctx, br, &msg, toks)
				if err != nil {
					b.Errorf("error while learning: %v", err)
				}
			}
			b.ReportAllocs()
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					if _, err := brain.Speak(ctx, br, "bocchi", toks[rand.IntN(len(toks)-1)]); err != nil {
						b.Errorf("error while speaking: %v", err)
					}
				}
			})
		})
	}
}

// randbytes fills a slice of at least length 16 with random data.
func randbytes(b []byte) []byte {
	binary.NativeEndian.PutUint64(b[8:], rand.Uint64())
	binary.NativeEndian.PutUint64(b, rand.Uint64())
	return b
}