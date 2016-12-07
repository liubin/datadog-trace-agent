package main

import (
	"fmt"
	"sort"
	"sync"

	log "github.com/cihub/seelog"

	"github.com/DataDog/datadog-trace-agent/config"
	"github.com/DataDog/datadog-trace-agent/model"
	"github.com/DataDog/datadog-trace-agent/statsd"
)

// Concentrator produces time bucketed statistics from a stream of raw traces.
// https://en.wikipedia.org/wiki/Knelson_concentrator
// Gets an imperial shitton of traces, and outputs pre-computed data structures
// allowing to find the gold (stats) amongst the traces.
// It also takes care of inserting the spans in a sampler.
type Concentrator struct {
	in          chan model.Trace            // incoming spans to process
	out         chan []model.StatsBucket    // outgoing payload
	buckets     map[int64]model.StatsBucket // buckets use to aggregate stats per timestamp
	aggregators []string                    // we'll always aggregate (if possible) to this finest grain
	lock        sync.Mutex                  // lock to read/write buckets

	conf *config.AgentConfig
}

// NewConcentrator initializes a new concentrator ready to be started
func NewConcentrator(in chan model.Trace, conf *config.AgentConfig) *Concentrator {
	sort.Strings(conf.ExtraAggregators)

	return &Concentrator{
		in:      in,
		out:     make(chan []model.StatsBucket),
		buckets: make(map[int64]model.StatsBucket),
		conf:    conf,
	}
}

// Run starts doing some concentrating work
func (c *Concentrator) Run() {
	for t := range c.in {
		// flush on this signal sent upstream
		if len(t) == 1 && t[0].IsFlushMarker() {
			c.out <- c.Flush()
			continue
		}

		// extract the env from the trace if any
		env := t.GetEnv()
		if env == "" {
			env = c.conf.DefaultEnv
		}

		for _, s := range t {
			err := c.HandleNewSpan(s, env)
			if err != nil {
				log.Debugf("span %v rejected by concentrator, err: %v", s, err)
			}
		}
	}

	close(c.out)
}

func (c *Concentrator) roundToBucket(ts int64) int64 {
	return ts - ts%c.conf.BucketInterval.Nanoseconds()
}

// HandleNewSpan adds to the current bucket the pointed span
func (c *Concentrator) HandleNewSpan(s model.Span, env string) error {
	c.lock.Lock()
	defer c.lock.Unlock()

	// base our timestamp calculation on the span end, and not on its beginning,
	// else we would filter all spans that are older than OldestSpanCutoff (say, 1min)
	end := s.End()
	now := model.Now()
	if now > end+c.conf.OldestSpanCutoff {
		log.Debugf("span was blocked because it is too old cutoff=%d now=%d end=%d: %v", c.conf.OldestSpanCutoff/1e9, now/1e9, end/1e9, s)
		statsd.Client.Count("trace_agent.concentrator.late_span", 1, nil, 1)
		return fmt.Errorf("rejecting late span, late by %ds", (now-end)/1e9)
	}

	bucketTs := c.roundToBucket(end)
	b, ok := c.buckets[bucketTs]
	if !ok {
		b = model.NewStatsBucket(
			bucketTs, c.conf.BucketInterval.Nanoseconds(),
		)
		c.buckets[bucketTs] = b
	}

	log.Debugf("span was accepted because it is recent enough cutoff=%d now=%d end=%d: %v", c.conf.OldestSpanCutoff/1e9, now/1e9, end/1e9, s)

	b.HandleSpan(s, env, c.conf.ExtraAggregators)
	return nil
}

// Int64Slice attaches the methods of sort.Interface to []int64.
type Int64Slice []int64

func (p Int64Slice) Len() int           { return len(p) }
func (p Int64Slice) Less(i, j int) bool { return p[i] < p[j] }
func (p Int64Slice) Swap(i, j int)      { p[i], p[j] = p[j], p[i] }
func sortInts64(a []int64)              { sort.Sort(Int64Slice(a)) }

// Flush deletes and returns complete statistic buckets
func (c *Concentrator) Flush() []model.StatsBucket {
	now := model.Now()
	lastBucketTs := c.roundToBucket(now)
	sb := []model.StatsBucket{}
	keys := []int64{}

	c.lock.Lock()
	defer c.lock.Unlock()

	// Sort buckets by timestamp
	for k := range c.buckets {
		keys = append(keys, k)
	}
	sortInts64(keys)

	for _, ts := range keys {
		bucket := c.buckets[ts]
		// flush & expire old buckets that cannot be hit anymore
		if ts < now-c.conf.OldestSpanCutoff && ts < lastBucketTs {
			log.Debugf("concentrator, bucket:%d is clear and flushed", ts)
			for _, d := range bucket.Distributions {
				statsd.Client.Histogram("trace_agent.distribution.len", float64(d.Summary.N), nil, 1)
			}
			sb = append(sb, bucket)
			delete(c.buckets, ts)
		}
	}
	log.Debugf("concentrator, flush %d stats buckets", len(sb))
	return sb
}
