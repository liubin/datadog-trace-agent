package model

import (
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

type sortableSublayers []SublayerValue

func (ss sortableSublayers) Len() int      { return len(ss) }
func (ss sortableSublayers) Swap(i, j int) { ss[i], ss[j] = ss[j], ss[i] }
func (ss sortableSublayers) Less(i, j int) bool {
	return ss[i].Metric < ss[j].Metric || (ss[i].Metric == ss[j].Metric && ss[i].Tag.Value < ss[j].Tag.Value)
}

func TestSublayerNested(t *testing.T) {
	assert := assert.New(t)

	// real trace
	now := time.Now().UnixNano()
	tr := Trace{
		Span{TraceID: 1, SpanID: 1, ParentID: 0, Start: now + 42, Duration: 1000000000, Service: "mcnulty", Type: "web"},
		Span{TraceID: 1, SpanID: 2, ParentID: 1, Start: now + 100, Duration: 200000000, Service: "mcnulty", Type: "sql"},
		Span{TraceID: 1, SpanID: 3, ParentID: 2, Start: now + 150, Duration: 199999000, Service: "master-db", Type: "sql"},
		Span{TraceID: 1, SpanID: 4, ParentID: 1, Start: now + 500000000, Duration: 500000, Service: "redis", Type: "redis"},
		Span{TraceID: 1, SpanID: 5, ParentID: 1, Start: now + 700000000, Duration: 700000, Service: "mcnulty", Type: ""},
	}

	sublayers := ComputeSublayers(&tr)

	sortedSublayers := sortableSublayers(sublayers)
	sort.Sort(sortedSublayers)

	assert.Equal(sortableSublayers{
		SublayerValue{Metric: "_sublayers.duration.by_service", Tag: Tag{"sublayer_service", "master-db"}, Value: 199999000},
		SublayerValue{Metric: "_sublayers.duration.by_service", Tag: Tag{"sublayer_service", "mcnulty"}, Value: 1000000000 - 199999000 - 500000},
		SublayerValue{Metric: "_sublayers.duration.by_service", Tag: Tag{"sublayer_service", "redis"}, Value: 500000},
		SublayerValue{Metric: "_sublayers.duration.by_type", Tag: Tag{"sublayer_type", "redis"}, Value: 500000},
		SublayerValue{Metric: "_sublayers.duration.by_type", Tag: Tag{"sublayer_type", "sql"}, Value: 200000000},
		SublayerValue{Metric: "_sublayers.duration.by_type", Tag: Tag{"sublayer_type", "web"}, Value: 1000000000 - 200000000 - 500000},
		SublayerValue{Metric: "_sublayers.span_count", Value: 5},
	},
		sortedSublayers,
	)

	// pin on trace's root
	root := tr.GetRoot()
	SetSublayersOnSpan(root, sublayers)

	expectedMetrics := map[string]float64{
		"_sublayers.span_count":                                     5,
		"_sublayers.duration.by_type.sublayer_type:web":             1000000000 - 200000000 - 500000,
		"_sublayers.duration.by_type.sublayer_type:sql":             200000000,
		"_sublayers.duration.by_type.sublayer_type:redis":           500000,
		"_sublayers.duration.by_service.sublayer_service:mcnulty":   1000000000 - 199999000 - 500000,
		"_sublayers.duration.by_service.sublayer_service:master-db": 199999000,
		"_sublayers.duration.by_service.sublayer_service:redis":     500000,
	}

	// assert sublayers result in original trace
	for _, s := range tr {
		if s.ParentID == 0 {
			assert.Equal(len(expectedMetrics), len(s.Metrics),
				"not getting expected amount of sublayer metrics",
			)

			for k, v := range s.Metrics {
				v2, ok := expectedMetrics[k]
				if !assert.True(ok,
					"not expecting this sublayer metric %s",
					k,
				) {
					continue
				}

				assert.Equal(v2, v, "metric %s has wrong value", k)
			}

		} else {
			assert.Nil(s.Metrics)
		}
	}
}

func BenchmarkSublayerThru(b *testing.B) {
	// real trace
	tr := Trace{
		Span{TraceID: 1, SpanID: 1, ParentID: 0, Start: 42, Duration: 1000000000, Service: "mcnulty", Type: "web"},
		Span{TraceID: 1, SpanID: 2, ParentID: 1, Start: 100, Duration: 200000000, Service: "mcnulty", Type: "sql"},
		Span{TraceID: 1, SpanID: 3, ParentID: 2, Start: 150, Duration: 199999000, Service: "master-db", Type: "sql"},
		Span{TraceID: 1, SpanID: 4, ParentID: 1, Start: 500000000, Duration: 500000, Service: "redis", Type: "redis"},
		Span{TraceID: 1, SpanID: 5, ParentID: 1, Start: 700000000, Duration: 700000, Service: "mcnulty", Type: ""},
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		ComputeSublayers(&tr)
	}
}
