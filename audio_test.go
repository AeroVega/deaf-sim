package main

import (
	"bytes"
	"math"
	"os"
	"testing"
	"time"

	"github.com/faiface/beep"
	"github.com/faiface/beep/wav"
)

func TestBiquadHandlesGainRangeWithoutNonFiniteSamples(t *testing.T) {
	filter := NewBiquad(48000, 1000, 1)

	for _, gain := range []float64{MinGainDB, -60, 0, 12} {
		filter.UpdateGain(gain)
		if got := filter.Gain(); got != gain {
			t.Fatalf("Gain() = %v, want %v", got, gain)
		}
		for i := 0; i < 128; i++ {
			if sample := filter.Process(0.25); !isFinite(sample) {
				t.Fatalf("gain %v produced non-finite sample %v", gain, sample)
			}
		}
	}
}

func TestBiquadClampsExtremeInput(t *testing.T) {
	filter := NewBiquad(48000, 1000, 1)
	filter.UpdateGain(-1000)
	if got := filter.Gain(); got != internalMinGainDB {
		t.Fatalf("Gain() = %v, want internal floor %v", got, internalMinGainDB)
	}
	filter.UpdateGain(math.NaN())
	if got := filter.Gain(); got != 0 {
		t.Fatalf("NaN gain should return to neutral, got %v", got)
	}
}

func TestEqualizerBuildsStereoTenBandMatrix(t *testing.T) {
	source := beep.StreamerFunc(func(samples [][2]float64) (int, bool) {
		for i := range samples {
			samples[i] = [2]float64{0.1, -0.1}
		}
		return len(samples), true
	})

	eq := NewEqualizerStreamer(source, 48000)
	if len(eq.Filters) != 10 {
		t.Fatalf("got %d bands, want 10", len(eq.Filters))
	}
	for i, band := range eq.Filters {
		if band[0] == nil || band[1] == nil {
			t.Fatalf("band %d does not have two channel filters", i)
		}
	}

	samples := make([][2]float64, 8)
	if n, ok := eq.Stream(samples); n != len(samples) || !ok {
		t.Fatalf("Stream() = (%d, %v), want (%d, true)", n, ok, len(samples))
	}
	for i, sample := range samples {
		if math.Abs(sample[0]-0.1) > 1e-12 || math.Abs(sample[1]+0.1) > 1e-12 {
			t.Fatalf("neutral EQ changed sample %d to %v", i, sample)
		}
	}
}

func TestEqualizerResetClearsFilterHistory(t *testing.T) {
	source := beep.StreamerFunc(func(samples [][2]float64) (int, bool) {
		if len(samples) == 0 {
			return 0, false
		}
		samples[0] = [2]float64{1, 1}
		return 1, true
	})
	eq := NewEqualizerStreamer(source, 48000)
	eq.Filters[4][0].UpdateGain(12)
	eq.Filters[4][1].UpdateGain(12)
	first := make([][2]float64, 1)
	_, _ = eq.Stream(first)
	eq.Reset()

	if eq.Filters[4][0].Process(0) != 0 || eq.Filters[4][1].Process(0) != 0 {
		t.Fatal("Reset() left delayed filter output behind")
	}
}

func TestValidatePreset(t *testing.T) {
	valid := presetFile{Version: 2, Scope: "combined", Gains: make([]float64, len(equalizerFrequencies))}
	if err := validatePreset(valid); err != nil {
		t.Fatalf("valid preset rejected: %v", err)
	}

	bad := valid
	bad.Gains = append([]float64(nil), valid.Gains[:9]...)
	if err := validatePreset(bad); err == nil {
		t.Fatal("preset with too few gains was accepted")
	}

	bad = valid
	bad.Gains = append([]float64(nil), valid.Gains...)
	bad.Gains[3] = MaxGainDB + 1
	if err := validatePreset(bad); err == nil {
		t.Fatal("preset with out-of-range gain was accepted")
	}
}

func TestHearingBandGainModelsRecruitment(t *testing.T) {
	profile := HearingLossProfile{Enabled: true, Type: "sensorineural", Recruitment: 0.8}
	quiet := hearingBandGain(40, math.Pow(10, -55.0/20), profile)
	loud := hearingBandGain(40, math.Pow(10, -5.0/20), profile)
	if quiet >= loud {
		t.Fatalf("quiet gain %v should be lower than loud gain %v", quiet, loud)
	}
	conductive := profile
	conductive.Type = "conductive"
	if got := hearingBandGain(40, math.Pow(10, -5.0/20), conductive); got <= 0 {
		t.Fatal("conductive gain should remain audible")
	}
}

func TestTinnitusSeverityScalesLayer(t *testing.T) {
	config := TinnitusPreset{Enabled: true, Severity: 1, Components: []TinnitusComponent{{Type: "tone", FrequencyHz: 1000, LevelDBFS: -20, Ear: "both"}}}
	source := beep.StreamerFunc(func(samples [][2]float64) (int, bool) {
		for i := range samples {
			samples[i] = [2]float64{}
		}
		return len(samples), true
	})
	stream := newTinnitusStreamer(source, 48000, config)
	full := make([][2]float64, 32)
	_, _ = stream.Stream(full)
	stream.severity = 0.25
	stream.Reset()
	quarter := make([][2]float64, 32)
	_, _ = stream.Stream(quarter)
	if math.Abs(quarter[8][0]) >= math.Abs(full[8][0]) {
		t.Fatalf("quarter severity did not reduce tone: full=%v quarter=%v", full[8][0], quarter[8][0])
	}
}

func TestParseCommandLine(t *testing.T) {
	options, err := parseCommandLine([]string{"-buffer-ms", "75", "recording.wav"})
	if err != nil {
		t.Fatalf("parseCommandLine() returned error: %v", err)
	}
	if options.filePath != "recording.wav" || options.bufferDuration != 75*time.Millisecond {
		t.Fatalf("unexpected options: %+v", options)
	}

	if _, err := parseCommandLine([]string{"-file", "one.wav", "two.wav"}); err == nil {
		t.Fatal("accepted both -file and positional path")
	}
	if _, err := parseCommandLine([]string{"-buffer-ms", "5"}); err == nil {
		t.Fatal("accepted an unsafe buffer size")
	}
}

func TestBundledWAVAssetsProvideFrames(t *testing.T) {
	for _, path := range []string{"assets/audio/speech-podcast.wav", "assets/audio/fingerstyle-guitar.wav"} {
		if _, err := os.Stat(path); err != nil {
			t.Skipf("fixture %s is unavailable: %v", path, err)
		}
		asset, err := loadAudioAsset(path)
		if err != nil {
			t.Fatalf("loadAudioAsset(%q): %v", path, err)
		}
		if asset.source.Len() == 0 {
			asset.close()
			t.Fatalf("loadAudioAsset(%q) reported an empty stream", path)
		}

		samples := make([][2]float64, 128)
		n, ok := asset.ctrl.Stream(samples)
		if n == 0 || !ok {
			asset.close()
			t.Fatalf("first stream from %q = (%d, %v), want audio frames", path, n, ok)
		}
		if err := resetAsset(asset); err != nil {
			asset.close()
			t.Fatalf("resetAsset(%q): %v", path, err)
		}
		n, ok = asset.ctrl.Stream(samples)
		asset.close()
		if n == 0 || !ok {
			t.Fatalf("restarted stream from %q = (%d, %v), want audio frames", path, n, ok)
		}
	}
}

func TestLimitedStreamerStopsAtClipLength(t *testing.T) {
	source := beep.StreamerFunc(func(samples [][2]float64) (int, bool) {
		for i := range samples {
			samples[i] = [2]float64{0.25, -0.25}
		}
		return len(samples), true
	})
	limited := &limitedStreamer{streamer: source, remaining: 3}
	buffer := make([][2]float64, 8)
	if n, ok := limited.Stream(buffer); n != 3 || !ok {
		t.Fatalf("first limited stream = (%d, %v), want (3, true)", n, ok)
	}
	if n, ok := limited.Stream(buffer); n != 0 || ok {
		t.Fatalf("completed limited stream = (%d, %v), want (0, false)", n, ok)
	}
}

func TestNormalizeClipFileName(t *testing.T) {
	for _, test := range []struct {
		input string
		want  string
	}{
		{input: "quiet-speech", want: "quiet-speech.wav"},
		{input: "quiet-speech.WAV", want: "quiet-speech.WAV"},
	} {
		got, err := normalizeClipFileName(test.input)
		if err != nil || got != test.want {
			t.Fatalf("normalizeClipFileName(%q) = %q, %v; want %q", test.input, got, err, test.want)
		}
	}
	for _, input := range []string{"", "clip.mp3", "folder/clip"} {
		if _, err := normalizeClipFileName(input); err == nil {
			t.Fatalf("normalizeClipFileName(%q) accepted an invalid name", input)
		}
	}
}

func TestExportProcessedWAVWritesSelectedFrames(t *testing.T) {
	asset, err := loadAudioAsset("assets/audio/speech-podcast.wav")
	if err != nil {
		t.Skipf("fixture is unavailable: %v", err)
	}
	asset.close()

	format := asset.format
	endFrame := format.SampleRate.N(minClipDuration)
	if asset.source.Len() < endFrame {
		t.Skipf("fixture is shorter than the minimum export length")
	}
	var output bytes.Buffer
	if err := exportProcessedWAV(&output, "assets/audio/speech-podcast.wav", format, 0, endFrame, make([]float64, len(equalizerFrequencies)), HearingLossProfile{}, TinnitusPreset{}); err != nil {
		t.Fatalf("exportProcessedWAV() returned error: %v", err)
	}
	decoded, decodedFormat, err := wav.Decode(bytes.NewReader(output.Bytes()))
	if err != nil {
		t.Fatalf("exported WAV could not be decoded: %v", err)
	}
	defer decoded.Close()
	if decodedFormat != format {
		t.Fatalf("exported format = %+v, want %+v", decodedFormat, format)
	}
	if decoded.Len() != endFrame {
		t.Fatalf("exported frame count = %d, want %d", decoded.Len(), endFrame)
	}
}

func TestExportProcessedWAVStartsWithDryFiveSecondComparison(t *testing.T) {
	asset, err := loadAudioAsset("assets/audio/speech-podcast.wav")
	if err != nil {
		t.Skipf("fixture is unavailable: %v", err)
	}
	format := asset.format
	minimumFrames := format.SampleRate.N(minClipDuration)
	if asset.source.Len() < minimumFrames {
		asset.close()
		t.Skipf("fixture is shorter than the minimum export length")
	}
	asset.close()

	tinnitus := TinnitusPreset{
		Enabled:  true,
		Severity: 1,
		Components: []TinnitusComponent{{
			Type:        "tone",
			FrequencyHz: 440,
			LevelDBFS:   -10,
			Ear:         "both",
		}},
	}
	var output bytes.Buffer
	if err := exportProcessedWAV(&output, "assets/audio/speech-podcast.wav", format, 0, minimumFrames, make([]float64, len(equalizerFrequencies)), HearingLossProfile{}, tinnitus); err != nil {
		t.Fatalf("exportProcessedWAV() returned error: %v", err)
	}
	decoded, _, err := wav.Decode(bytes.NewReader(output.Bytes()))
	if err != nil {
		t.Fatalf("exported WAV could not be decoded: %v", err)
	}
	defer decoded.Close()
	var dryReference bytes.Buffer
	if err := exportProcessedWAV(&dryReference, "assets/audio/speech-podcast.wav", format, 0, minimumFrames, make([]float64, len(equalizerFrequencies)), HearingLossProfile{}, TinnitusPreset{}); err != nil {
		t.Fatalf("dry reference export returned error: %v", err)
	}
	reference, _, err := wav.Decode(bytes.NewReader(dryReference.Bytes()))
	if err != nil {
		t.Fatalf("dry reference WAV could not be decoded: %v", err)
	}
	defer reference.Close()

	dryOutput := make([][2]float64, 128)
	dryReferenceSamples := make([][2]float64, 128)
	if n, ok := decoded.Stream(dryOutput); n != len(dryOutput) || !ok {
		t.Fatalf("dry export stream = (%d, %v), want (%d, true)", n, ok, len(dryOutput))
	}
	if n, ok := reference.Stream(dryReferenceSamples); n != len(dryReferenceSamples) || !ok {
		t.Fatalf("dry reference stream = (%d, %v), want (%d, true)", n, ok, len(dryReferenceSamples))
	}
	for i := range dryOutput {
		if math.Abs(dryOutput[i][0]-dryReferenceSamples[i][0]) > 0.0001 || math.Abs(dryOutput[i][1]-dryReferenceSamples[i][1]) > 0.0001 {
			t.Fatalf("dry intro differs from dry reference sample %d: exported=%v reference=%v", i, dryOutput[i], dryReferenceSamples[i])
		}
	}

	comparisonFrame := format.SampleRate.N(5500 * time.Millisecond)
	if err := decoded.Seek(comparisonFrame); err != nil {
		t.Fatalf("could not seek exported comparison: %v", err)
	}
	if err := reference.Seek(comparisonFrame); err != nil {
		t.Fatalf("could not seek dry reference comparison: %v", err)
	}
	processedOutput := make([][2]float64, 128)
	processedReference := make([][2]float64, 128)
	if _, ok := decoded.Stream(processedOutput); !ok {
		t.Fatal("processed export comparison did not contain audio")
	}
	if _, ok := reference.Stream(processedReference); !ok {
		t.Fatal("dry reference comparison did not contain audio")
	}
	var difference float64
	for i := range processedOutput {
		difference += math.Abs(processedOutput[i][0]-processedReference[i][0]) + math.Abs(processedOutput[i][1]-processedReference[i][1])
	}
	if difference < 0.1 {
		t.Fatalf("processed section did not differ from the dry source enough: total difference=%v", difference)
	}
}

func TestExportProcessedWAVRejectsClipsLongerThanThirtySeconds(t *testing.T) {
	format := beep.Format{SampleRate: 48000, NumChannels: 2, Precision: 2}
	tooLong := format.SampleRate.N(31 * time.Second)
	if err := exportProcessedWAV(&bytes.Buffer{}, "missing.wav", format, 0, tooLong, make([]float64, len(equalizerFrequencies)), HearingLossProfile{}, TinnitusPreset{}); err == nil {
		t.Fatal("exportProcessedWAV accepted a clip longer than thirty seconds")
	}
}

func TestExportProcessedWAVRejectsClipsShorterThanTenSeconds(t *testing.T) {
	format := beep.Format{SampleRate: 48000, NumChannels: 2, Precision: 2}
	tooShort := format.SampleRate.N(9 * time.Second)
	if err := exportProcessedWAV(&bytes.Buffer{}, "missing.wav", format, 0, tooShort, make([]float64, len(equalizerFrequencies)), HearingLossProfile{}, TinnitusPreset{}); err == nil {
		t.Fatal("exportProcessedWAV accepted a clip shorter than ten seconds")
	}
}

func TestValidateClipSelectionAllowsAnArbitraryLaterWindow(t *testing.T) {
	format := beep.Format{SampleRate: 48000, NumChannels: 2, Precision: 2}
	totalFrames := format.SampleRate.N(90 * time.Second)
	startFrame := format.SampleRate.N(45 * time.Second)
	endFrame := startFrame + format.SampleRate.N(30*time.Second)
	if err := validateClipSelection(format, totalFrames, startFrame, endFrame); err != nil {
		t.Fatalf("later 30-second window was rejected: %v", err)
	}
}
