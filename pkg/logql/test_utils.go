package logql

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/cortexproject/cortex/pkg/querier/astmapper"
	"github.com/grafana/loki/pkg/iter"
	"github.com/grafana/loki/pkg/logproto"
	"github.com/prometheus/prometheus/pkg/labels"
	"github.com/prometheus/prometheus/promql"
)

func NewMockQuerier(shards int, streams []logproto.Stream) MockQuerier {
	return MockQuerier{
		shards:  shards,
		streams: streams,
	}
}

// Shard aware mock querier
type MockQuerier struct {
	shards  int
	streams []logproto.Stream
}

func (q MockQuerier) Select(_ context.Context, req SelectParams) (iter.EntryIterator, error) {
	expr, err := req.LogSelector()
	if err != nil {
		return nil, err
	}
	filter, err := expr.Filter()
	if err != nil {
		return nil, err
	}

	matchers := expr.Matchers()

	var matched []*logproto.Stream

outer:
	for _, stream := range q.streams {
		ls := mustParseLabels(stream.Labels)
		for _, matcher := range matchers {
			if matcher.Name == astmapper.ShardLabel {
				shard, err := astmapper.ParseShard(matcher.Value)
				if err != nil {
					return nil, err
				}

				if !(ls.Hash()%uint64(q.shards) == uint64(shard.Shard)) {
					continue outer
				}
			} else if !matcher.Matches(ls.Get(matcher.Name)) {
				continue outer
			}
		}
		matched = append(matched, &stream)
	}

	// apply the LineFilter
	filtered := make([]*logproto.Stream, 0, len(matched))
	if filter == TrueFilter {
		filtered = matched
	} else {
		for _, s := range matched {
			var entries []logproto.Entry
			for _, entry := range s.Entries {
				if filter.Filter([]byte(entry.Line)) {
					entries = append(entries, entry)
				}
			}

			if len(entries) > 0 {
				filtered = append(filtered, &logproto.Stream{
					Labels:  s.Labels,
					Entries: entries,
				})
			}
		}

	}

	return iter.NewTimeRangedIterator(
		iter.NewStreamsIterator(context.Background(), filtered, req.Direction),
		req.Start,
		req.End,
	), nil
}

// create nStreams of nEntries with labelNames each where each label value
// with the exception of the "index" label is modulo'd into a shard
func randomStreams(nStreams, nEntries, nShards int, labelNames []string) (streams []*logproto.Stream) {
	for i := 0; i < nStreams; i++ {
		// labels
		stream := &logproto.Stream{}
		ls := labels.Labels{{Name: "index", Value: fmt.Sprintf("%d", i)}}

		for _, lName := range labelNames {
			// I needed a way to hash something to uint64
			// in order to get some form of random label distribution
			shard := append(ls, labels.Label{
				Name:  lName,
				Value: fmt.Sprintf("%d", i),
			}).Hash() % uint64(nShards)

			ls = append(ls, labels.Label{
				Name:  lName,
				Value: fmt.Sprintf("%d", shard),
			})
		}
		for j := 0; j < nEntries; j++ {
			stream.Entries = append(stream.Entries, logproto.Entry{
				Timestamp: time.Unix(0, int64(j*int(time.Millisecond))),
				Line:      fmt.Sprintf("line number: %d", j),
			})
		}

		stream.Labels = ls.String()
		streams = append(streams, stream)
	}
	return streams

}

func mustParseLabels(s string) labels.Labels {
	labels, err := promql.ParseMetric(s)
	if err != nil {
		log.Fatalf("Failed to parse %s", s)
	}

	return labels
}
