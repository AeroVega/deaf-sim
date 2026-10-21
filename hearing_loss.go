package main

import (
	"errors"
	"math"

	"github.com/faiface/beep"
)

// HearingLossProfile is a compact, educational approximation of a hearing
// profile. Thresholds are relative dB losses at the application's ten bands;
// they are not calibrated dB HL measurements.
type HearingLossProfile struct {
	Enabled         bool      `json:"enabled"`
	Type            string    `json:"type"`
	Severity        float64   `json:"severity"`
	ThresholdsDB    []float64 `json:"thresholds_db"`
	Recruitment     float64   `json:"recruitment"`
	SelectivityLoss float64   `json:"selectivity_loss"`
}

type hearingLossStreamer struct {
	streamer   beep.Streamer
	sampleRate float64
	profile    HearingLossProfile
	bands      []hearingBand
}

type hearingBand struct {
	left, right       *auditoryBandpass
	leftEnv, rightEnv float64
}

type auditoryBandpass struct {
	b0, b1, b2, a1, a2 float64
	x1, x2, y1, y2     float64
}

func newHearingLossStreamer(streamer beep.Streamer, sampleRate float64, profile HearingLossProfile) *hearingLossStreamer {
	h := &hearingLossStreamer{streamer: streamer, sampleRate: sampleRate}
	h.setProfile(profile)
	return h
}

func (h *hearingLossStreamer) setProfile(profile HearingLossProfile) {
	previousSelectivity := h.profile.SelectivityLoss
	h.profile = profile
	if len(h.bands) == len(equalizerFrequencies) && previousSelectivity == profile.SelectivityLoss {
		// Severity, thresholds, and recruitment are applied from the existing
		// filter state. Rebuilding the filters here would introduce a transient
		// discontinuity when the user moves the live severity control.
		return
	}
	h.bands = make([]hearingBand, len(equalizerFrequencies))
	bandwidth := equalizerBandwidth
	if profile.SelectivityLoss > 0 {
		bandwidth += 0.8 * clamp(profile.SelectivityLoss, 0, 1)
	}
	for i, frequency := range equalizerFrequencies {
		h.bands[i] = hearingBand{
			left:  newAuditoryBandpass(h.sampleRate, frequency, bandwidth),
			right: newAuditoryBandpass(h.sampleRate, frequency, bandwidth),
		}
	}
}

func newAuditoryBandpass(sampleRate, frequency, bandwidthOctaves float64) *auditoryBandpass {
	if sampleRate <= 0 || frequency <= 0 || frequency >= sampleRate/2 {
		return &auditoryBandpass{}
	}
	w0 := 2 * math.Pi * frequency / sampleRate
	// RBJ bandwidth parameter. Increasing the octave width models poorer
	// frequency selectivity by making neighboring auditory regions overlap.
	alpha := math.Sin(w0) * math.Sinh(math.Log(2)/2*bandwidthOctaves*w0/math.Sin(w0))
	cosW0 := math.Cos(w0)
	a0 := 1 + alpha
	return &auditoryBandpass{
		b0: alpha / a0,
		b1: 0,
		b2: -alpha / a0,
		a1: -2 * cosW0 / a0,
		a2: (1 - alpha) / a0,
	}
}

func (b *auditoryBandpass) process(x float64) float64 {
	if b == nil || b.b0 == 0 {
		return 0
	}
	y := b.b0*x + b.b1*b.x1 + b.b2*b.x2 - b.a1*b.y1 - b.a2*b.y2
	b.x2, b.x1 = b.x1, x
	b.y2, b.y1 = b.y1, y
	return y
}

func (h *hearingLossStreamer) Stream(samples [][2]float64) (int, bool) {
	n, ok := h.streamer.Stream(samples)
	if n <= 0 || !h.profile.Enabled || h.profile.Severity <= 0 {
		return n, ok
	}
	for i := 0; i < n; i++ {
		leftInput, rightInput := samples[i][0], samples[i][1]
		leftOutput, rightOutput := 0.0, 0.0
		for band := range h.bands {
			state := &h.bands[band]
			left := state.left.process(leftInput)
			right := state.right.process(rightInput)
			state.leftEnv = updateAuditoryEnvelope(state.leftEnv, math.Abs(left))
			state.rightEnv = updateAuditoryEnvelope(state.rightEnv, math.Abs(right))
			loss := h.profile.ThresholdsDB[band] * clamp(h.profile.Severity, 0, 1)
			leftOutput += left * hearingBandGain(loss, state.leftEnv, h.profile)
			rightOutput += right * hearingBandGain(loss, state.rightEnv, h.profile)
		}
		// The octave bands overlap; this normalization keeps a neutral-ish
		// broadband signal at a usable level after recombination.
		samples[i][0] = leftOutput * 1.65
		samples[i][1] = rightOutput * 1.65
	}
	return n, ok
}

func updateAuditoryEnvelope(previous, sample float64) float64 {
	coefficient := 0.002
	if sample > previous {
		coefficient = 0.015
	}
	return previous + coefficient*(sample-previous)
}

func hearingBandGain(lossDB, envelope float64, profile HearingLossProfile) float64 {
	if lossDB <= 0 {
		return 1
	}
	levelDB := 20 * math.Log10(math.Max(envelope, 1e-6))
	thresholdDB := -60 + lossDB
	// The visible EQ curve already supplies threshold elevation. Recruitment
	// therefore adds level-dependent recovery above threshold, while the
	// perceptual stage adds extra suppression below threshold. Conductive loss
	// has no recruitment; sensorineural and mixed profiles can have it.
	recruitment := clamp(profile.Recruitment, 0, 1)
	if profile.Type == "conductive" {
		recruitment = 0
	}
	levelProgress := clamp((levelDB-thresholdDB)/60, 0, 1)
	recruitmentRecovery := lossDB * recruitment * levelProgress
	extraLoss := 0.0
	if levelDB < thresholdDB {
		extraLoss = (thresholdDB - levelDB) * 0.35
	}
	return math.Pow(10, (recruitmentRecovery-extraLoss)/20)
}

func (h *hearingLossStreamer) Err() error { return h.streamer.Err() }

func (h *hearingLossStreamer) Reset() {
	if resetter, ok := h.streamer.(interface{ Reset() }); ok {
		resetter.Reset()
	}
	for i := range h.bands {
		h.bands[i].left = newAuditoryBandpass(h.sampleRate, equalizerFrequencies[i], equalizerBandwidth+0.8*clamp(h.profile.SelectivityLoss, 0, 1))
		h.bands[i].right = newAuditoryBandpass(h.sampleRate, equalizerFrequencies[i], equalizerBandwidth+0.8*clamp(h.profile.SelectivityLoss, 0, 1))
		h.bands[i].leftEnv, h.bands[i].rightEnv = 0, 0
	}
}

func validateHearingLoss(profile HearingLossProfile) error {
	if !profile.Enabled {
		return nil
	}
	if profile.Type != "conductive" && profile.Type != "sensorineural" && profile.Type != "mixed" {
		return errors.New("hearing-loss type must be conductive, sensorineural, or mixed")
	}
	if !isFinite(profile.Severity) || profile.Severity < 0 || profile.Severity > 1 {
		return errors.New("hearing-loss severity must be between 0 and 1")
	}
	if len(profile.ThresholdsDB) != len(equalizerFrequencies) {
		return errors.New("hearing-loss profile must contain ten threshold values")
	}
	for _, threshold := range profile.ThresholdsDB {
		if !isFinite(threshold) || threshold < 0 || threshold > 110 {
			return errors.New("hearing-loss thresholds must be between 0 and 110 dB")
		}
	}
	if !isFinite(profile.Recruitment) || profile.Recruitment < 0 || profile.Recruitment > 1 {
		return errors.New("hearing-loss recruitment must be between 0 and 1")
	}
	if !isFinite(profile.SelectivityLoss) || profile.SelectivityLoss < 0 || profile.SelectivityLoss > 1 {
		return errors.New("hearing-loss selectivity loss must be between 0 and 1")
	}
	return nil
}
