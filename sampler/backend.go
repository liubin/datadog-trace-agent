package sampler

import (
	"sync"
	"time"
)

// Backend storing any state required to run the sampling algorithms.
//
// Current implementation is only based on counters with polynomial decay.
// Its bias with steady counts is 1 * decayFactor.
// The stored scores represent approximation of the real count values (with a countScaleFactor factor).
type Backend struct {
	// Score per signature
	scores map[Signature]float64
	// Score of all traces (equals the sum of all signature scores)
	totalScore float64
	// Score of sampled traces
	sampledScore float64
	mu           sync.Mutex

	// Every decayPeriod, decay the score
	// Lower value is more reactive, but forgets quicker
	decayPeriod time.Duration
	// At every decay tick, how much we reduce/divide the score
	// Lower value is more reactive, but forgets quicker
	decayFactor float64
	// Factor to apply to move from the score to the representing number of traces per second.
	// By definition of the decay formula: countScaleFactor = (decayFactor / (decayFactor - 1)) * decayPeriod
	// It also represents by how much a spike is smoothed: if we instantly receive N times the same signature,
	// its immediate count will be increased by N / countScaleFactor.
	countScaleFactor float64

	exit chan struct{}
}

// NewBackend returns an initialized Backend
func NewBackend(decayPeriod time.Duration) *Backend {
	// With this factor, any past trace counts for less than 50% after 6*decayPeriod and >1% after 39*decayPeriod
	// We can keep it hardcoded, but having `decayPeriod` configurable should be enough?
	decayFactor := 1.125 // 9/8

	return &Backend{
		scores:           make(map[Signature]float64),
		sampledScore:     0,
		decayPeriod:      decayPeriod,
		decayFactor:      decayFactor,
		countScaleFactor: (decayFactor / (decayFactor - 1)) * decayPeriod.Seconds(),
		exit:             make(chan struct{}),
	}
}

// Run runs and block on the Sampler main loop
func (b *Backend) Run() {
	t := time.NewTicker(b.decayPeriod)
	defer t.Stop()

	for {
		select {
		case <-t.C:
			b.DecayScore()
		case <-b.exit:
			return
		}
	}
}

// Stop stops the main Run loop
func (b *Backend) Stop() {
	close(b.exit)
}

// CountSignature counts an incoming signature
func (b *Backend) CountSignature(signature Signature) {
	b.mu.Lock()
	b.scores[signature]++
	b.totalScore++
	b.mu.Unlock()
}

// CountSample counts a trace sampled by the sampler
func (b *Backend) CountSample() {
	b.mu.Lock()
	b.sampledScore++
	b.mu.Unlock()
}

// GetSignatureScore returns the score of a signature.
// It is normalized to represent a number of signatures per second.
func (b *Backend) GetSignatureScore(signature Signature) float64 {
	b.mu.Lock()
	score := b.scores[signature] / b.countScaleFactor
	b.mu.Unlock()

	return score
}

// GetSampledScore returns the global score of all sampled traces.
func (b *Backend) GetSampledScore() float64 {
	b.mu.Lock()
	score := b.sampledScore / b.countScaleFactor
	b.mu.Unlock()

	return score
}

// GetTotalScore returns the global score of all sampled traces.
func (b *Backend) GetTotalScore() float64 {
	b.mu.Lock()
	score := b.totalScore / b.countScaleFactor
	b.mu.Unlock()

	return score
}

// GetUpperSampledScore returns a certain upper bound of the global count of all sampled traces.
func (b *Backend) GetUpperSampledScore() float64 {
	// Overestimate the real score with the high limit of the backend bias.
	return b.GetSampledScore() * b.decayFactor
}

// GetCardinality returns the number of different signatures seen recently.
func (b *Backend) GetCardinality() int64 {
	b.mu.Lock()
	cardinality := int64(len(b.scores))
	b.mu.Unlock()

	return cardinality
}

// DecayScore applies the decay to the rolling counters
func (b *Backend) DecayScore() {
	b.mu.Lock()
	for sig := range b.scores {
		score := b.scores[sig]
		if score > b.decayFactor*minSignatureScoreOffset {
			b.scores[sig] /= b.decayFactor
		} else {
			// When the score is too small, we can optimize by simply dropping the entry
			delete(b.scores, sig)
		}
	}
	b.totalScore /= b.decayFactor
	b.sampledScore /= b.decayFactor
	b.mu.Unlock()
}
