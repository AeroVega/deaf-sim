package main

import (
	"errors"
	"math"
	"math/rand"

	"github.com/faiface/beep"
)

// TinnitusPreset describes an educational acoustic approximation. It is not a
// model of the neural causes or subjective experience of tinnitus.
type TinnitusPreset struct {
	Enabled    bool                `json:"enabled"`
	Severity   float64             `json:"severity"`
	Components []TinnitusComponent `json:"components"`
}

type TinnitusComponent struct {
	Type             string  `json:"type"`
	FrequencyHz      float64 `json:"frequency_hz,omitempty"`
	LevelDBFS        float64 `json:"level_dbfs"`
	BandwidthOctaves float64 `json:"bandwidth_octaves,omitempty"`
	ModulationRateHz float64 `json:"modulation_rate_hz,omitempty"`
	ModulationDepth  float64 `json:"modulation_depth,omitempty"`
	Ear              string  `json:"ear,omitempty"`
}

type tinnitusStreamer struct {
	streamer    beep.Streamer
	sampleRate  float64
	components  []TinnitusComponent
	phases      []float64
	noiseState  []float64
	rng         *rand.Rand
	sampleIndex int64
	severity    float64
}

func newTinnitusStreamer(streamer beep.Streamer, sampleRate float64, preset TinnitusPreset) *tinnitusStreamer {
	t := &tinnitusStreamer{streamer: streamer, sampleRate: sampleRate, rng: rand.New(rand.NewSource(1)), severity: clamp(preset.Severity, 0, 1)}
	if preset.Enabled {
		t.components = append([]TinnitusComponent(nil), preset.Components...)
	}
	t.phases = make([]float64, len(t.components))
	t.noiseState = make([]float64, len(t.components))
	return t
}

func (t *tinnitusStreamer) Stream(samples [][2]float64) (int, bool) {
	n, ok := t.streamer.Stream(samples)
	if n <= 0 || t.sampleRate <= 0 {
		return n, ok
	}
	for i := 0; i < n; i++ {
		for c, component := range t.components {
			level := math.Pow(10, component.LevelDBFS/20) * t.severity
			mod := 1.0
			if component.ModulationRateHz > 0 && component.ModulationDepth > 0 {
				mod += clamp(component.ModulationDepth, 0, 1) * math.Sin(2*math.Pi*component.ModulationRateHz*float64(t.sampleIndex)/t.sampleRate)
			}
			value := 0.0
			switch component.Type {
			case "tone", "warble":
				freq := math.Max(1, component.FrequencyHz)
				if component.Type == "warble" {
					freq *= 1 + 0.04*math.Sin(2*math.Pi*0.7*float64(t.sampleIndex)/t.sampleRate)
				}
				value = math.Sin(t.phases[c])
				t.phases[c] += 2 * math.Pi * freq / t.sampleRate
				if t.phases[c] > 2*math.Pi {
					t.phases[c] -= 2 * math.Pi
				}
			case "noise":
				raw := t.rng.Float64()*2 - 1
				// A one-pole smoother makes the noise less like an artificial hiss;
				// bandwidth controls how quickly its spectrum changes.
				width := math.Max(0.05, component.BandwidthOctaves)
				alpha := clamp(2*math.Pi*math.Max(20, component.FrequencyHz)*width/t.sampleRate, 0.01, 1)
				t.noiseState[c] += alpha * (raw - t.noiseState[c])
				value = t.noiseState[c]
			default:
				continue
			}
			value *= level * mod
			if component.Ear == "left" {
				samples[i][0] += value
			} else if component.Ear == "right" {
				samples[i][1] += value
			} else {
				samples[i][0] += value
				samples[i][1] += value
			}
		}
		t.sampleIndex++
	}
	return n, ok
}

func (t *tinnitusStreamer) Err() error { return t.streamer.Err() }
func (t *tinnitusStreamer) Reset() {
	if resetter, ok := t.streamer.(interface{ Reset() }); ok {
		resetter.Reset()
	}
	for i := range t.phases {
		t.phases[i] = 0
		t.noiseState[i] = 0
	}
	t.sampleIndex = 0
	t.rng = rand.New(rand.NewSource(1))
}

func validateTinnitus(p TinnitusPreset) error {
	if !isFinite(p.Severity) || p.Severity < 0 || p.Severity > 1 {
		return errors.New("tinnitus severity must be between 0 and 1")
	}
	if len(p.Components) > 8 {
		return errors.New("tinnitus preset may contain at most 8 components")
	}
	for _, c := range p.Components {
		if c.Type != "tone" && c.Type != "warble" && c.Type != "noise" {
			return errors.New("tinnitus component type must be tone, warble, or noise")
		}
		if !isFinite(c.LevelDBFS) || c.LevelDBFS < MinGainDB || c.LevelDBFS > 0 {
			return errors.New("tinnitus levels must be between -110 and 0 dBFS")
		}
		if c.Type != "noise" && (!isFinite(c.FrequencyHz) || c.FrequencyHz < 20 || c.FrequencyHz > 20000) {
			return errors.New("tinnitus tone frequencies must be between 20 and 20000 Hz")
		}
		if c.Type == "noise" && (!isFinite(c.FrequencyHz) || c.FrequencyHz < 20 || c.FrequencyHz > 20000) {
			return errors.New("tinnitus noise center frequencies must be between 20 and 20000 Hz")
		}
		if !isFinite(c.ModulationDepth) || c.ModulationDepth < 0 || c.ModulationDepth > 1 {
			return errors.New("tinnitus modulation depth must be between 0 and 1")
		}
		if c.Ear != "" && c.Ear != "left" && c.Ear != "right" && c.Ear != "both" {
			return errors.New("tinnitus ear must be left, right, or both")
		}
	}
	return nil
}
