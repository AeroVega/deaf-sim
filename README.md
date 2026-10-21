# Deaf Sim

Deaf Sim is an educational desktop tool for exploring how different EQ curves can change the character of recorded sound. It loads a WAV file, routes it through a real-time 10-band graphic equalizer, and lets you adjust each band while the audio is playing.

This project is an early development build, not a v1 release. It is intended for experimentation and perspective-building, not as a clinical hearing-loss simulator.

## Features

- WAV file chooser and command-line file loading
- 10 peaking EQ bands: 31, 63, 125, 250, 500, 1k, 2k, 4k, 8k, and 16k Hz
- Independent left/right filter chains with a 1-octave default bandwidth
- Gain range of `-110.0 dB` to `+12.0 dB` in `0.1 dB` steps
- Perceptual hearing-loss profiles with shared severity control, threshold elevation, recruitment, and reduced frequency selectivity approximations
- Tinnitus profiles with shared severity control over layered tonal, warbling, and noise-like components
- Real-time left/right level meters plus waveform and spectrum visualization of the processed signal
- Guided interface with clear listening steps, profile controls, comfortable-volume guidance, and an explanation of the simulator’s limits
- In-app hearing-loss and tinnitus preset libraries, with separate pickers for each listening layer
- Play, pause, stop, progress, duration, sample-rate, and channel readouts
- Per-band reset and Reset all controls
- JSON preset save/load
- Full-timeline two-handle selection and offline export of 10–30 second named WAV clips; the first 5 seconds remain dry for A/B comparison before the active simulation begins
- Configurable playback buffer, defaulting to about 50 ms

## Requirements

- Go 1.26 or newer
- A desktop environment supported by Fyne and the beep audio backend

The first build may download Go module dependencies.

## Run

Start the app and choose a file from the Open WAV button:

```text
go run .
```

Load a file at startup using either a positional path or `-file`:

```text
go run . path/to/recording.wav
go run . -file path/to/recording.wav
```

Adjust the audio buffer when troubleshooting latency or dropouts:

```text
go run . -file path/to/recording.wav -buffer-ms 75
```

The accepted buffer range is 10–1000 ms. Smaller values can reduce control latency; larger values are generally more tolerant of busy systems.

## Workspace layout

The repository keeps runtime source at the module root while separating content that will grow with the simulator:

- `assets/audio/` — demo WAV recordings
- `presets/hearing-loss/` — EQ-based hearing-loss teaching examples
- `presets/tinnitus/` — layered tinnitus teaching examples
- `bin/` — local build output, ignored by version control

Source files use role-based names such as `audio_engine.go`, `audio_stream.go`, and `equalizer.go`; tests use the corresponding `*_test.go` convention. New feature-specific assets should follow the same category-first organization rather than being added to the repository root.

## Presets

Presets are JSON files containing a `scope` (`hearing-loss`, `tinnitus`, or `combined`), the equalizer frequencies, ten gain values, and optional hearing-loss and tinnitus profiles. Loading a single-domain preset changes only that domain, preserving the other active layer. Hearing-loss profiles define a type (`conductive`, `sensorineural`, or `mixed`), a ten-band threshold curve, recruitment, and selectivity-loss parameters. The GUI severity slider scales the whole profile and updates the visible frequency sliders together. Tinnitus profiles have their own severity slider, which scales all of their components together. Gain values must remain within `-110` to `+12` dB.

The `presets/` folder includes illustrative hearing-loss curves and tinnitus examples. The GUI presents these automatically in separate hearing-loss and tinnitus preset libraries, so users do not need to browse the filesystem to find the included examples. A tinnitus preset may add up to eight layered `tone`, `warble`, or shaped `noise` components, optionally with slow amplitude modulation and left/right placement. These are educational listening approximations, not clinical models: tinnitus is a subjective and heterogeneous experience, and cochlear hearing loss can involve distortion and loudness recruitment that a graphic EQ cannot reproduce. The pulsatile example includes a medical caution because pulse-synchronous tinnitus can warrant clinical evaluation.

## Development

Run the tests with:

```text
go test ./...
```

The current implementation supports PCM WAV files decoded by `github.com/faiface/beep/wav`, including files whose data chunk advertises an unknown length and runs to end-of-file. Mono input is presented to both output channels by the decoder. EQ bands at or above the file's Nyquist frequency are safely bypassed rather than producing unstable coefficients.

## ROADMAP

- [x] Add research-informed hearing-loss and tinnitus presets
- [x] Add a severity control that scales hearing-loss profiles across the spectrum
- [x] Add a perceptual hearing-loss approximation with threshold elevation, loudness recruitment, and reduced frequency selectivity
- [x] Add a shared tinnitus severity control for layered, time-varying components
- [x] Add level meters and a waveform/spectrum view
- [x] Export arbitrary 10–30-second WAV clips with a 5-second dry A/B lead-in followed by the active hearing-loss and tinnitus settings
- [ ] Improve calibration and validation against audiological reference signals
- [ ] Explore a more complete auditory-periphery model with documented limitations
- [ ] Produce shareable Windows and Linux releases: build a native executable for each target OS, verify each build on a clean machine, and package the executable, required runtime assets, presets, README, and license notices into a clearly named ZIP archive. Document platform-specific dependencies and avoid implying that one binary is universally portable across distributions.
- [ ] Include a small, curated set of premade WAV demonstrations in `assets/audio/` for users who do not have recordings available. Add an in-app demo picker or clearly surfaced starter recordings while preserving the existing custom WAV import flow; use recordings with appropriate redistribution rights and include attribution/licensing notes.
- [ ] Add a safe-listening startup and transition experience: begin playback at a conservative level, provide a clear comfortable-volume reminder, and fade playback or parameter changes when needed to reduce surprises and protect users who may be sensitive to sound.
- [ ] Add guided educational listening scenarios with plain-language prompts, captions or written observations, and suggested comparisons such as speech in quiet versus music with reduced high-frequency detail. Keep the scenarios framed as perspective-building exercises rather than representations of every Deaf, deaf, or hard-of-hearing person’s experience.
- [ ] Expand accessibility and inclusive interaction support: ensure keyboard navigation, visible focus states, readable contrast, descriptive control labels, and non-color-only status communication. Validate the workflow with people who use assistive technology and with Deaf and hard-of-hearing collaborators.
- [ ] Add automated audio regression coverage for live parameter changes, including checks for click-free severity updates, bounded output levels, stable filter state, and correct preservation of the other listening layer when presets are loaded.
- [ ] Add a documented export/share format for educational sessions that records the selected preset names, severity values, EQ curve, and recording metadata without embedding or uploading the user’s private audio.

The current perceptual model is intentionally compact and real-time friendly. It uses a ten-band auditory filter bank, level-dependent attenuation, and profile-controlled filter broadening. It does not claim to reproduce individual loudness growth, speech intelligibility, dead regions, distortion, hyperacusis, or the lived experience of hearing loss or tinnitus.

The analyzer displays the final processed stereo signal. The upper half shows the recent waveform (blue = left, orange = right); the lower half shows a compact magnitude spectrum. Meter values are RMS estimates in dBFS, intended for relative comparison rather than calibrated sound-pressure measurement.
