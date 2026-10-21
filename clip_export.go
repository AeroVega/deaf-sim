package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/faiface/beep"
	"github.com/faiface/beep/wav"
)

const (
	minClipDuration            = 10 * time.Second
	maxClipDuration            = 30 * time.Second
	comparisonTransitionLength = 20 * time.Millisecond
)

type limitedStreamer struct {
	streamer  beep.Streamer
	remaining int
}

func (s *limitedStreamer) Stream(samples [][2]float64) (int, bool) {
	if s.remaining <= 0 {
		return 0, false
	}
	if len(samples) > s.remaining {
		samples = samples[:s.remaining]
	}
	n, ok := s.streamer.Stream(samples)
	if n > s.remaining {
		n = s.remaining
	}
	s.remaining -= n
	if n == 0 {
		return 0, false
	}
	if !ok || s.remaining == 0 {
		return n, true
	}
	return n, true
}

func (s *limitedStreamer) Err() error { return s.streamer.Err() }

type concatStreamer struct {
	streams []beep.Streamer
	index   int
}

func (s *concatStreamer) Stream(samples [][2]float64) (int, bool) {
	for s.index < len(s.streams) {
		n, ok := s.streams[s.index].Stream(samples)
		if n > 0 {
			if !ok {
				s.index++
			}
			return n, true
		}
		s.index++
	}
	return 0, false
}

func (s *concatStreamer) Err() error {
	if s.index < len(s.streams) {
		return s.streams[s.index].Err()
	}
	return nil
}

type crossfadeStreamer struct {
	dry       beep.Streamer
	processed beep.Streamer
	remaining int
	position  int
	total     int
}

func (s *crossfadeStreamer) Stream(samples [][2]float64) (int, bool) {
	if s.remaining <= 0 {
		return 0, false
	}
	count := len(samples)
	if count > s.remaining {
		count = s.remaining
	}
	drySamples := make([][2]float64, count)
	processedSamples := make([][2]float64, count)
	dryN, _ := s.dry.Stream(drySamples)
	processedN, _ := s.processed.Stream(processedSamples)
	n := dryN
	if processedN < n {
		n = processedN
	}
	for i := 0; i < n; i++ {
		alpha := float64(s.position+i) / float64(maxInt(1, s.total-1))
		if alpha < 0 {
			alpha = 0
		}
		if alpha > 1 {
			alpha = 1
		}
		samples[i][0] = drySamples[i][0]*(1-alpha) + processedSamples[i][0]*alpha
		samples[i][1] = drySamples[i][1]*(1-alpha) + processedSamples[i][1]*alpha
	}
	s.position += n
	s.remaining -= n
	if n == 0 {
		return 0, false
	}
	return n, true
}

func (s *crossfadeStreamer) Err() error {
	if err := s.dry.Err(); err != nil {
		return err
	}
	return s.processed.Err()
}

func discardFrames(streamer beep.Streamer, frames int) error {
	if frames <= 0 {
		return nil
	}
	buffer := make([][2]float64, 512)
	remaining := frames
	for remaining > 0 {
		chunk := remaining
		if chunk > len(buffer) {
			chunk = len(buffer)
		}
		n, ok := streamer.Stream(buffer[:chunk])
		if n <= 0 {
			if err := streamer.Err(); err != nil {
				return err
			}
			return errors.New("audio ended before the selected clip start")
		}
		remaining -= n
		if !ok && remaining > 0 {
			return errors.New("audio ended before the selected clip start")
		}
	}
	return nil
}

func validateClipSelection(format beep.Format, totalFrames, startFrame, endFrame int) error {
	if format.SampleRate <= 0 {
		return errors.New("the source recording has an invalid sample rate")
	}
	if totalFrames < format.SampleRate.N(minClipDuration) {
		return fmt.Errorf("the recording must be at least %s long to export", formatTime(minClipDuration))
	}
	if startFrame < 0 || endFrame > totalFrames {
		return errors.New("the selected clip is outside the recording")
	}
	if endFrame <= startFrame {
		return errors.New("choose a clip with a positive duration")
	}
	selectedFrames := endFrame - startFrame
	if selectedFrames < format.SampleRate.N(minClipDuration) {
		return fmt.Errorf("the exported clip must be at least %s long", formatTime(minClipDuration))
	}
	if selectedFrames > format.SampleRate.N(maxClipDuration) {
		return fmt.Errorf("the exported clip cannot be longer than %s", formatTime(maxClipDuration))
	}
	return nil
}

// exportProcessedWAV renders a fresh pass through the current listening
// settings, discards frames before the selected range, and writes at most
// thirty seconds to the destination writer. The source file is never changed.
func exportProcessedWAV(destination io.Writer, sourcePath string, format beep.Format, startFrame, endFrame int, gains []float64, hearingLoss HearingLossProfile, tinnitus TinnitusPreset) error {
	if destination == nil {
		return errors.New("no export destination was provided")
	}
	if sourcePath == "" {
		return errors.New("no WAV recording is loaded")
	}
	if format.SampleRate <= 0 {
		return errors.New("the source recording has an invalid sample rate")
	}
	if err := validateHearingLoss(hearingLoss); err != nil {
		return err
	}
	if err := validateTinnitus(tinnitus); err != nil {
		return err
	}

	dryAsset, err := loadAudioAsset(sourcePath)
	if err != nil {
		return err
	}
	defer dryAsset.close()
	processedAsset, err := loadAudioAsset(sourcePath)
	if err != nil {
		return err
	}
	defer processedAsset.close()
	if len(gains) != len(processedAsset.eq.Filters) {
		return fmt.Errorf("export settings must contain %d EQ gain values", len(processedAsset.eq.Filters))
	}
	if err := validateClipSelection(format, processedAsset.source.Len(), startFrame, endFrame); err != nil {
		return err
	}

	for band, gainDB := range gains {
		processedAsset.eq.Filters[band][0].UpdateGain(gainDB)
		processedAsset.eq.Filters[band][1].UpdateGain(gainDB)
	}
	processedAsset.hearingLoss.setProfile(hearingLoss)
	processedAsset.tinnitus.components = nil
	if tinnitus.Enabled {
		processedAsset.tinnitus.components = append(processedAsset.tinnitus.components, tinnitus.Components...)
	}
	processedAsset.tinnitus.phases = make([]float64, len(processedAsset.tinnitus.components))
	processedAsset.tinnitus.noiseState = make([]float64, len(processedAsset.tinnitus.components))
	processedAsset.tinnitus.severity = clamp(tinnitus.Severity, 0, 1)

	if err := dryAsset.source.Seek(startFrame); err != nil {
		return err
	}
	if err := processedAsset.source.Seek(0); err != nil {
		return err
	}
	processedAsset.eq.Reset()
	processedAsset.hearingLoss.Reset()
	processedAsset.tinnitus.Reset()
	processedAsset.analysis.Reset()
	selectedFrames := endFrame - startFrame
	dryFrames := format.SampleRate.N(5 * time.Second)
	if dryFrames > selectedFrames {
		dryFrames = selectedFrames
	}
	processedFrames := selectedFrames - dryFrames
	if err := discardFrames(processedAsset.ctrl, startFrame+dryFrames); err != nil {
		return err
	}
	transitionFrames := format.SampleRate.N(comparisonTransitionLength)
	if transitionFrames > processedFrames {
		transitionFrames = processedFrames
	}

	temporary, err := os.CreateTemp("", "deaf-sim-export-*.wav")
	if err != nil {
		return fmt.Errorf("could not create a temporary export: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)

	streams := []beep.Streamer{
		&limitedStreamer{streamer: dryAsset.source, remaining: dryFrames},
	}
	if transitionFrames > 0 {
		streams = append(streams, &crossfadeStreamer{
			dry:       &limitedStreamer{streamer: dryAsset.source, remaining: transitionFrames},
			processed: &limitedStreamer{streamer: processedAsset.ctrl, remaining: transitionFrames},
			remaining: transitionFrames,
			total:     transitionFrames,
		})
	}
	if tailFrames := processedFrames - transitionFrames; tailFrames > 0 {
		streams = append(streams, &limitedStreamer{streamer: processedAsset.ctrl, remaining: tailFrames})
	}
	clip := &concatStreamer{streams: streams}
	if err := wav.Encode(temporary, clip, format); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("could not encode WAV: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("could not finalize WAV: %w", err)
	}

	encoded, err := os.Open(temporaryPath)
	if err != nil {
		return fmt.Errorf("could not reopen encoded WAV: %w", err)
	}
	defer encoded.Close()
	if _, err := io.Copy(destination, encoded); err != nil {
		return fmt.Errorf("could not write exported WAV: %w", err)
	}
	return nil
}
