package main

import (
	"math"
	"sync"
)

const (
	// The UI exposes this range. The lower internal limit keeps coefficient
	// calculations well behaved when Biquad is used independently of the UI.
	MinGainDB = -110.0
	MaxGainDB = 12.0

	internalMinGainDB = -120.0
)

// Biquad is a peaking EQ filter using the Audio EQ Cookbook coefficients.
// Coefficients and delay-line state share one lock so a gain change cannot
// interleave with a sample calculation.
type Biquad struct {
	mu sync.Mutex

	b0, b1, b2 float64
	a1, a2     float64

	x1, x2 float64
	y1, y2 float64

	sampleRate float64
	centerFreq float64
	bandwidth  float64
	active     bool
	gainDB     float64
}

// NewBiquad creates a peaking EQ filter with neutral (0 dB) gain.
func NewBiquad(sampleRate, centerFreq, bandwidth float64) *Biquad {
	b := &Biquad{
		sampleRate: sampleRate,
		centerFreq: centerFreq,
		bandwidth:  bandwidth,
		active:     sampleRate > 0 && centerFreq > 0 && centerFreq < sampleRate/2,
	}
	b.setGainLocked(0)
	return b
}

// UpdateGain changes the filter gain in dB. It is safe to call while audio is
// streaming. Values below -120 dB are clamped internally to avoid needless
// extreme coefficient ratios; the UI's minimum of -110 dB is effectively
// silent already.
func (b *Biquad) UpdateGain(gainDB float64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.setGainLocked(gainDB)
}

func (b *Biquad) setGainLocked(gainDB float64) {
	if math.IsNaN(gainDB) {
		gainDB = 0
	}
	gainDB = clamp(gainDB, internalMinGainDB, MaxGainDB)
	b.gainDB = gainDB

	if !b.active || !isFinite(b.sampleRate) || !isFinite(b.centerFreq) || b.bandwidth <= 0 {
		b.setPassthroughLocked()
		return
	}

	omega := 2 * math.Pi * b.centerFreq / b.sampleRate
	sinOmega := math.Sin(omega)
	cosOmega := math.Cos(omega)
	if math.Abs(sinOmega) < 1e-12 {
		b.setPassthroughLocked()
		return
	}

	alpha := sinOmega * math.Sinh((math.Log(2)/2)*b.bandwidth*omega/sinOmega)
	amplitude := math.Pow(10, gainDB/40)
	if !isFinite(alpha) || !isFinite(amplitude) || amplitude <= 0 {
		b.setPassthroughLocked()
		return
	}

	b0 := 1 + alpha*amplitude
	b1 := -2 * cosOmega
	b2 := 1 - alpha*amplitude
	a0 := 1 + alpha/amplitude
	a1 := -2 * cosOmega
	a2 := 1 - alpha/amplitude
	if !isFinite(a0) || math.Abs(a0) < 1e-12 {
		b.setPassthroughLocked()
		return
	}

	b.b0 = b0 / a0
	b.b1 = b1 / a0
	b.b2 = b2 / a0
	b.a1 = a1 / a0
	b.a2 = a2 / a0
	if !isFinite(b.b0) || !isFinite(b.b1) || !isFinite(b.b2) || !isFinite(b.a1) || !isFinite(b.a2) {
		b.setPassthroughLocked()
	}
}

func (b *Biquad) setPassthroughLocked() {
	b.b0 = 1
	b.b1 = 0
	b.b2 = 0
	b.a1 = 0
	b.a2 = 0
}

// Gain returns the currently applied gain in dB.
func (b *Biquad) Gain() float64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.gainDB
}

// Reset clears the filter delay line without changing its gain.
func (b *Biquad) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.x1, b.x2, b.y1, b.y2 = 0, 0, 0, 0
}

// Process filters one sample.
func (b *Biquad) Process(sample float64) float64 {
	b.mu.Lock()
	defer b.mu.Unlock()

	out := b.b0*sample + b.b1*b.x1 + b.b2*b.x2 - b.a1*b.y1 - b.a2*b.y2
	b.x2 = b.x1
	b.x1 = sample
	b.y2 = b.y1
	b.y1 = out
	return out
}

func clamp(value, min, max float64) float64 {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

func isFinite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
