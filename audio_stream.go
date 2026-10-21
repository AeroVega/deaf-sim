package main

import (
	"sync/atomic"

	"github.com/faiface/beep"
)

var equalizerFrequencies = []float64{31, 63, 125, 250, 500, 1000, 2000, 4000, 8000, 16000}

const equalizerBandwidth = 1.0

// EqualizerStreamer applies the 10-band EQ cascade to every stereo frame.
type EqualizerStreamer struct {
	Streamer beep.Streamer
	Filters  [][2]*Biquad
}

// NewEqualizerStreamer builds one left/right filter pair for each ISO band.
func NewEqualizerStreamer(streamer beep.Streamer, sampleRate float64) *EqualizerStreamer {
	filters := make([][2]*Biquad, len(equalizerFrequencies))
	for i, frequency := range equalizerFrequencies {
		filters[i] = [2]*Biquad{
			NewBiquad(sampleRate, frequency, equalizerBandwidth),
			NewBiquad(sampleRate, frequency, equalizerBandwidth),
		}
	}

	return &EqualizerStreamer{Streamer: streamer, Filters: filters}
}

// Stream reads and filters the next block from the source.
func (es *EqualizerStreamer) Stream(samples [][2]float64) (n int, ok bool) {
	n, ok = es.Streamer.Stream(samples)
	if n <= 0 {
		return n, ok
	}

	for i := 0; i < n; i++ {
		left, right := samples[i][0], samples[i][1]
		for band := range es.Filters {
			left = es.Filters[band][0].Process(left)
			right = es.Filters[band][1].Process(right)
		}
		samples[i][0], samples[i][1] = left, right
	}

	return n, ok
}

// Err forwards source errors.
func (es *EqualizerStreamer) Err() error {
	return es.Streamer.Err()
}

// Reset clears all band histories for a fresh playback pass.
func (es *EqualizerStreamer) Reset() {
	for band := range es.Filters {
		es.Filters[band][0].Reset()
		es.Filters[band][1].Reset()
	}
}

// Gains returns one gain value per EQ band.
func (es *EqualizerStreamer) Gains() []float64 {
	gains := make([]float64, len(es.Filters))
	for i := range es.Filters {
		gains[i] = es.Filters[i][0].Gain()
	}
	return gains
}

// positionStreamer counts source frames for the progress display.
type positionStreamer struct {
	streamer beep.Streamer
	position int64
}

func (p *positionStreamer) Stream(samples [][2]float64) (int, bool) {
	n, ok := p.streamer.Stream(samples)
	if n > 0 {
		atomic.AddInt64(&p.position, int64(n))
	}
	return n, ok
}

func (p *positionStreamer) Err() error {
	return p.streamer.Err()
}

func (p *positionStreamer) Position() int {
	return int(atomic.LoadInt64(&p.position))
}

func (p *positionStreamer) Reset() {
	atomic.StoreInt64(&p.position, 0)
}
