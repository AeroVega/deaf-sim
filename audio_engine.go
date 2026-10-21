package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/faiface/beep"
	"github.com/faiface/beep/speaker"
	"github.com/faiface/beep/wav"
)

const defaultBufferDuration = 50 * time.Millisecond

type playbackState uint8

const (
	stateStopped playbackState = iota
	statePlaying
	statePaused
)

type audioAsset struct {
	path        string
	source      beep.StreamSeekCloser
	eq          *EqualizerStreamer
	hearingLoss *hearingLossStreamer
	tinnitus    *tinnitusStreamer
	analysis    *analysisTap
	position    *positionStreamer
	ctrl        *beep.Ctrl
	format      beep.Format
	duration    time.Duration
}

func loadAudioAsset(path string) (*audioAsset, error) {
	if path == "" {
		return nil, errors.New("no WAV file was selected")
	}
	if !equalFoldExtension(path, ".wav") {
		return nil, fmt.Errorf("unsupported audio file %q: choose a .wav file", filepath.Base(path))
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("cannot access %q: %w", path, err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("%q is a folder, not a WAV file", path)
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("cannot open %q: %w", path, err)
	}

	reader, err := prepareWAVReader(file)
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("cannot inspect %q: %w", path, err)
	}
	source, format, err := wav.Decode(reader)
	if err != nil {
		return nil, fmt.Errorf("cannot decode %q: %w", path, err)
	}
	if format.SampleRate <= 0 || format.NumChannels <= 0 {
		_ = source.Close()
		return nil, fmt.Errorf("%q has invalid audio format", path)
	}

	eq := NewEqualizerStreamer(source, float64(format.SampleRate))
	hearingLoss := newHearingLossStreamer(eq, float64(format.SampleRate), HearingLossProfile{})
	tinnitus := newTinnitusStreamer(hearingLoss, float64(format.SampleRate), TinnitusPreset{})
	analysis := newAnalysisTap(tinnitus)
	position := &positionStreamer{streamer: analysis}
	duration := time.Duration(float64(source.Len()) / float64(format.SampleRate) * float64(time.Second))

	return &audioAsset{
		path:        path,
		source:      source,
		eq:          eq,
		hearingLoss: hearingLoss,
		tinnitus:    tinnitus,
		analysis:    analysis,
		position:    position,
		ctrl:        &beep.Ctrl{Streamer: position},
		format:      format,
		duration:    duration,
	}, nil
}

// prepareWAVReader works around WAV files written with an unknown data chunk
// length (0xffffffff). That value is legal for streaming-oriented WAV/RF64
// files, but this version of beep/wav stores it in an int32 and treats the
// stream as already drained. The wrapper patches only the bytes seen by the
// decoder; the user's file is never modified.
func prepareWAVReader(file *os.File) (io.ReadSeeker, error) {
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	fileSize := info.Size()
	if fileSize < 12 {
		return file, nil
	}

	const unknownChunkSize = ^uint32(0)
	const maxSignedInt32 = int64(^uint32(0) >> 1)
	offset := int64(12)
	var chunkHeader [8]byte
	for offset+8 <= fileSize {
		if _, err := file.ReadAt(chunkHeader[:], offset); err != nil {
			return file, nil
		}
		chunkID := string(chunkHeader[:4])
		chunkSize := binary.LittleEndian.Uint32(chunkHeader[4:])
		if chunkID == "data" && chunkSize == unknownChunkSize {
			dataStart := offset + 8
			dataSize := fileSize - dataStart
			if dataSize < 0 || dataSize > maxSignedInt32 {
				return nil, errors.New("WAV data chunk is too large")
			}
			return &patchedWAVReader{
				file:        file,
				dataSizeAt:  offset + 4,
				patchedSize: uint32(dataSize),
			}, nil
		}

		nextOffset := offset + 8 + int64(chunkSize)
		if chunkSize%2 != 0 {
			nextOffset++
		}
		if nextOffset <= offset || nextOffset > fileSize {
			break
		}
		offset = nextOffset
	}
	return file, nil
}

type patchedWAVReader struct {
	file        *os.File
	dataSizeAt  int64
	patchedSize uint32
	position    int64
}

func (r *patchedWAVReader) Read(p []byte) (int, error) {
	start := r.position
	n, err := r.file.Read(p)
	for i := 0; i < n; i++ {
		absolute := start + int64(i)
		if absolute >= r.dataSizeAt && absolute < r.dataSizeAt+4 {
			shift := uint((absolute - r.dataSizeAt) * 8)
			p[i] = byte(r.patchedSize >> shift)
		}
	}
	r.position += int64(n)
	return n, err
}

func (r *patchedWAVReader) Seek(offset int64, whence int) (int64, error) {
	position, err := r.file.Seek(offset, whence)
	if err == nil {
		r.position = position
	}
	return position, err
}

func (r *patchedWAVReader) Close() error {
	return r.file.Close()
}

func (a *audioAsset) close() {
	if a != nil && a.source != nil {
		_ = a.source.Close()
	}
}

type audioSnapshot struct {
	loaded    bool
	path      string
	format    beep.Format
	position  time.Duration
	duration  time.Duration
	state     playbackState
	positionN int
	totalN    int
}

// audioEngine owns the speaker stream and the decoded WAV resource. All
// speaker mutations happen while the speaker is locked, as required by beep.
type audioEngine struct {
	mu             sync.Mutex
	asset          *audioAsset
	state          playbackState
	bufferDuration time.Duration
	closed         bool
	onFinished     func()
	playbackID     uint64
	speakerReady   bool
	speakerRate    beep.SampleRate
}

func newAudioEngine(bufferDuration time.Duration, onFinished func()) *audioEngine {
	if bufferDuration <= 0 {
		bufferDuration = defaultBufferDuration
	}
	return &audioEngine{bufferDuration: bufferDuration, onFinished: onFinished}
}

func (e *audioEngine) install(asset *audioAsset) error {
	if asset == nil {
		return errors.New("cannot install an empty audio asset")
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		asset.close()
		return errors.New("audio engine is closed")
	}

	e.detachAssetLocked()
	if !e.speakerReady || e.speakerRate != asset.format.SampleRate {
		speaker.Close()
		e.speakerReady = false
		bufferSize := asset.format.SampleRate.N(e.bufferDuration)
		if bufferSize < 1 {
			bufferSize = 1
		}
		if err := speaker.Init(asset.format.SampleRate, bufferSize); err != nil {
			asset.close()
			return fmt.Errorf("cannot initialize audio output: %w", err)
		}
		e.speakerReady = true
		e.speakerRate = asset.format.SampleRate
	}
	if !e.speakerReady {
		asset.close()
		return errors.New("audio output is not initialized")
	}

	e.asset = asset
	e.state = stateStopped
	return nil
}

func (e *audioEngine) detachAssetLocked() {
	if e.asset != nil {
		e.playbackID++
		speaker.Clear()
		speaker.Lock()
		e.asset.ctrl.Streamer = nil
		e.asset.ctrl.Paused = true
		speaker.Unlock()
		e.asset.close()
		e.asset = nil
	}
}

func (e *audioEngine) unload() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return
	}
	e.detachAssetLocked()
	e.state = stateStopped
}

func (e *audioEngine) close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return
	}
	e.closed = true
	e.detachAssetLocked()
	if e.speakerReady {
		speaker.Close()
		e.speakerReady = false
	}
	e.state = stateStopped
}

func (e *audioEngine) play() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return errors.New("audio engine is closed")
	}
	if e.asset == nil {
		return errors.New("load a WAV file before playing")
	}
	asset := e.asset
	if e.state == statePlaying {
		if asset.position.Position() < asset.source.Len() {
			return nil
		}
		e.state = stateStopped
	}
	startOver := e.state == stateStopped || asset.position.Position() >= asset.source.Len()
	speaker.Lock()
	if startOver {
		if err := resetAsset(asset); err != nil {
			speaker.Unlock()
			return fmt.Errorf("cannot restart audio: %w", err)
		}
	}
	asset.ctrl.Paused = false
	speaker.Unlock()

	if e.state == statePaused && !startOver {
		e.state = statePlaying
		return nil
	}

	e.playbackID++
	playbackID := e.playbackID
	completion := beep.Callback(func() {
		e.finished(asset, playbackID)
	})
	speaker.Play(beep.Seq(asset.ctrl, completion))
	e.state = statePlaying
	return nil
}

func (e *audioEngine) pause() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.asset == nil {
		return errors.New("load a WAV file before pausing")
	}
	if e.state != statePlaying {
		return nil
	}
	speaker.Lock()
	e.asset.ctrl.Paused = true
	speaker.Unlock()
	e.state = statePaused
	return nil
}

func (e *audioEngine) stop() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.asset == nil {
		return errors.New("load a WAV file before stopping")
	}

	asset := e.asset
	e.playbackID++
	speaker.Clear()
	speaker.Lock()
	asset.ctrl.Paused = true
	err := resetAsset(asset)
	speaker.Unlock()
	e.state = stateStopped
	if err != nil {
		return fmt.Errorf("cannot reset audio: %w", err)
	}
	return nil
}

func resetAsset(asset *audioAsset) error {
	if err := asset.source.Seek(0); err != nil {
		return err
	}
	asset.analysis.Reset()
	asset.position.Reset()
	return nil
}

func (e *audioEngine) setTinnitus(preset TinnitusPreset) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.asset == nil {
		return errors.New("load a WAV file before changing tinnitus")
	}
	if err := validateTinnitus(preset); err != nil {
		return err
	}
	speaker.Lock()
	defer speaker.Unlock()
	e.asset.tinnitus.components = nil
	if preset.Enabled {
		e.asset.tinnitus.components = append(e.asset.tinnitus.components, preset.Components...)
	}
	e.asset.tinnitus.phases = make([]float64, len(e.asset.tinnitus.components))
	e.asset.tinnitus.noiseState = make([]float64, len(e.asset.tinnitus.components))
	e.asset.tinnitus.severity = clamp(preset.Severity, 0, 1)
	return nil
}

func (e *audioEngine) setHearingLoss(profile HearingLossProfile) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.asset == nil {
		return errors.New("load a WAV file before changing hearing loss")
	}
	if err := validateHearingLoss(profile); err != nil {
		return err
	}
	speaker.Lock()
	defer speaker.Unlock()
	e.asset.hearingLoss.setProfile(profile)
	return nil
}

// setHearingLossAndGains applies the linked hearing-loss controls as one
// speaker-thread update. Keeping this atomic prevents the audio callback from
// observing a partially updated frequency curve while the severity slider is
// moving.
func (e *audioEngine) setHearingLossAndGains(profile HearingLossProfile, gains []float64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.asset == nil {
		return errors.New("load a WAV file before changing hearing loss")
	}
	if err := validateHearingLoss(profile); err != nil {
		return err
	}
	if len(gains) != len(e.asset.eq.Filters) {
		return fmt.Errorf("hearing-loss update must contain %d gain values", len(e.asset.eq.Filters))
	}

	speaker.Lock()
	defer speaker.Unlock()
	for band, gainDB := range gains {
		gainDB = clamp(gainDB, MinGainDB, MaxGainDB)
		e.asset.eq.Filters[band][0].UpdateGain(gainDB)
		e.asset.eq.Filters[band][1].UpdateGain(gainDB)
	}
	e.asset.hearingLoss.setProfile(profile)
	return nil
}

func (e *audioEngine) finished(asset *audioAsset, playbackID uint64) {
	// beep invokes callbacks while holding the speaker lock. Handle the
	// engine state asynchronously so the callback never waits on a lock that
	// a UI action may hold while it is adding or removing a stream.
	go e.finishAsync(asset, playbackID)
}

func (e *audioEngine) finishAsync(asset *audioAsset, playbackID uint64) {
	e.mu.Lock()
	if e.asset != asset || e.closed || e.playbackID != playbackID {
		e.mu.Unlock()
		return
	}
	e.state = stateStopped
	onFinished := e.onFinished
	e.mu.Unlock()
	if onFinished != nil {
		onFinished()
	}
}

func (e *audioEngine) setGain(band int, gainDB float64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.asset == nil {
		return errors.New("load a WAV file before changing EQ")
	}
	if band < 0 || band >= len(e.asset.eq.Filters) {
		return fmt.Errorf("EQ band %d is out of range", band)
	}
	gainDB = clamp(gainDB, MinGainDB, MaxGainDB)
	e.asset.eq.Filters[band][0].UpdateGain(gainDB)
	e.asset.eq.Filters[band][1].UpdateGain(gainDB)
	return nil
}

func (e *audioEngine) gains() []float64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.asset == nil {
		return nil
	}
	return e.asset.eq.Gains()
}

func (e *audioEngine) hasAsset() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.asset != nil
}

func (e *audioEngine) analysisSnapshot() analysisSnapshot {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.asset == nil || e.asset.analysis == nil {
		return analysisSnapshot{}
	}
	return e.asset.analysis.Snapshot()
}

func (e *audioEngine) snapshot() audioSnapshot {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.asset == nil {
		return audioSnapshot{state: stateStopped}
	}
	positionN := e.asset.position.Position()
	totalN := e.asset.source.Len()
	position := time.Duration(0)
	if e.asset.format.SampleRate > 0 {
		position = time.Duration(float64(positionN) / float64(e.asset.format.SampleRate) * float64(time.Second))
	}
	return audioSnapshot{
		loaded:    true,
		path:      e.asset.path,
		format:    e.asset.format,
		position:  position,
		duration:  e.asset.duration,
		state:     e.state,
		positionN: positionN,
		totalN:    totalN,
	}
}

func equalFoldExtension(path, extension string) bool {
	return strings.EqualFold(filepath.Ext(path), extension)
}
