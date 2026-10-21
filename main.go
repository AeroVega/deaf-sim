package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"image/color"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

const (
	windowWidth      = 1180
	windowHeight     = 920
	bandSliderHeight = 88
)

type commandLineOptions struct {
	filePath       string
	bufferDuration time.Duration
}

func parseCommandLine(args []string) (commandLineOptions, error) {
	flags := flag.NewFlagSet("deaf-sim", flag.ContinueOnError)
	filePath := flags.String("file", "", "WAV file to load on startup")
	bufferMS := flags.Int("buffer-ms", 50, "audio buffer size in milliseconds")
	if err := flags.Parse(args); err != nil {
		return commandLineOptions{}, err
	}
	if flags.NArg() > 0 && *filePath != "" {
		return commandLineOptions{}, errors.New("choose either -file or a positional WAV path, not both")
	}
	if flags.NArg() > 0 {
		*filePath = flags.Arg(0)
	}
	if flags.NArg() > 1 {
		return commandLineOptions{}, errors.New("only one WAV file may be supplied")
	}
	if *bufferMS < 10 || *bufferMS > 1000 {
		return commandLineOptions{}, errors.New("-buffer-ms must be between 10 and 1000")
	}
	return commandLineOptions{
		filePath:       *filePath,
		bufferDuration: time.Duration(*bufferMS) * time.Millisecond,
	}, nil
}

func main() {
	options, err := parseCommandLine(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	myApp := app.NewWithID("com.deafsim.equalizer")
	myApp.Settings().SetTheme(newDeafSimTheme())
	myWindow := myApp.NewWindow("Deaf Sim · Hearing perspective lab")
	myWindow.Resize(fyne.NewSize(windowWidth, windowHeight))

	ui := newAppUI(myWindow, options.bufferDuration)
	myWindow.SetContent(ui.content())
	myWindow.SetCloseIntercept(func() {
		ui.shutdown()
		// Close interception runs on the Fyne event loop. Queue Quit so the
		// application exits through Fyne's normal shutdown path.
		fyne.Do(myApp.Quit)
	})

	ui.startProgressUpdater()
	if options.filePath != "" {
		ui.loadPath(options.filePath)
	}

	myWindow.ShowAndRun()
	ui.shutdown()
}

type appUI struct {
	window fyne.Window
	engine *audioEngine

	fileLabel             *widget.Label
	formatLabel           *widget.Label
	statusLabel           *widget.Label
	timeLabel             *widget.Label
	progress              *widget.ProgressBar
	clipSelector          *clipRangeSelector
	clipStartEntry        *widget.Entry
	clipEndEntry          *widget.Entry
	clipLengthLabel       *widget.Label
	clipTimelineEnd       *widget.Label
	clipAvailabilityLabel *widget.Label
	clipNameEntry         *widget.Entry
	previewClipButton     *widget.Button
	exportClipButton      *widget.Button
	demoSelect            *widget.Select
	loadDemoButton        *widget.Button
	demoPaths             map[string]string
	outputLevelSlider     *widget.Slider
	outputLevelLabel      *widget.Label
	outputLevelDB         float64
	bandCards             *fyne.Container
	analysisView          *analysisView
	leftMeter, rightMeter *widget.ProgressBar
	leftLevel, rightLevel *widget.Label

	playButton  *widget.Button
	pauseButton *widget.Button
	stopButton  *widget.Button

	sliders                []*widget.Slider
	tinnitus               TinnitusPreset
	tinnitusLabel          *widget.Label
	hearingLoss            HearingLossProfile
	hearingLabel           *widget.Label
	hearingProfileName     string
	tinnitusProfileName    string
	severitySlider         *widget.Slider
	tinnitusSeveritySlider *widget.Slider
	updatingControls       bool

	loading        bool
	exporting      atomic.Bool
	loadGeneration atomic.Uint64
	closed         atomic.Bool
	shutdownOnce   sync.Once
	updatesDone    chan struct{}
}

func newAppUI(window fyne.Window, bufferDuration time.Duration) *appUI {
	ui := &appUI{
		window:        window,
		updatesDone:   make(chan struct{}),
		outputLevelDB: -12,
	}
	ui.engine = newAudioEngine(bufferDuration, func() {
		if ui.closed.Load() {
			return
		}
		fyne.Do(func() {
			if ui.closed.Load() {
				return
			}
			ui.statusLabel.SetText("Playback finished.")
			ui.refreshPlaybackUI()
		})
	})

	ui.fileLabel = widget.NewLabel("No recording selected")
	ui.fileLabel.Truncation = fyne.TextTruncateEllipsis
	ui.formatLabel = widget.NewLabel("Choose a WAV recording to begin")
	ui.formatLabel.Importance = widget.LowImportance
	ui.statusLabel = widget.NewLabel("Ready when you are.")
	ui.statusLabel.Truncation = fyne.TextTruncateEllipsis
	ui.timeLabel = widget.NewLabel("00:00 / 00:00")
	ui.tinnitusLabel = newCaptionLabel("Off · no tinnitus layer")
	ui.tinnitusLabel.Wrapping = fyne.TextWrapWord
	ui.hearingLabel = newCaptionLabel("Off · no hearing-loss profile")
	ui.hearingLabel.Wrapping = fyne.TextWrapWord
	ui.progress = widget.NewProgressBar()
	ui.progress.TextFormatter = func() string { return "" }
	ui.clipSelector = newClipRangeSelector()
	ui.clipStartEntry = widget.NewEntry()
	ui.clipStartEntry.SetText("0.00")
	ui.clipStartEntry.SetPlaceHolder("0.00")
	ui.clipStartEntry.OnSubmitted = func(string) { ui.applyClipTimeEntries() }
	ui.clipEndEntry = widget.NewEntry()
	ui.clipEndEntry.SetText("0.00")
	ui.clipEndEntry.SetPlaceHolder("30.00")
	ui.clipEndEntry.OnSubmitted = func(string) { ui.applyClipTimeEntries() }
	ui.clipLengthLabel = newCaptionLabel("Length 00.00 s")
	ui.clipTimelineEnd = newCaptionLabel("00:00")
	ui.clipAvailabilityLabel = newCaptionLabel("Select between 10 and 30 seconds from the full recording.")
	ui.clipNameEntry = widget.NewEntry()
	ui.clipNameEntry.SetPlaceHolder("deaf-sim-clip")
	ui.clipNameEntry.SetText("deaf-sim-clip")
	ui.previewClipButton = widget.NewButtonWithIcon("Preview selection", theme.MediaPlayIcon(), func() {
		ui.previewClipSelection()
	})
	ui.previewClipButton.Importance = widget.LowImportance
	ui.exportClipButton = widget.NewButtonWithIcon("Export .wav", theme.DocumentSaveIcon(), func() {
		ui.showClipExportSave()
	})
	ui.exportClipButton.Importance = widget.HighImportance
	ui.buildDemoControls()
	ui.buildOutputLevelControl()
	ui.clipSelector.OnChanged = func(startFrame, endFrame int) {
		ui.updateClipLabels(startFrame, endFrame)
	}

	ui.playButton = widget.NewButtonWithIcon("Play", theme.MediaPlayIcon(), func() {
		if err := ui.engine.play(); err != nil {
			ui.showError(err)
			return
		}
		ui.statusLabel.SetText("Playing")
		ui.refreshPlaybackUI()
	})
	ui.pauseButton = widget.NewButtonWithIcon("Pause", theme.MediaPauseIcon(), func() {
		if err := ui.engine.pause(); err != nil {
			ui.showError(err)
			return
		}
		ui.statusLabel.SetText("Paused")
		ui.refreshPlaybackUI()
	})
	ui.stopButton = widget.NewButtonWithIcon("Stop", theme.MediaStopIcon(), func() {
		if err := ui.engine.stop(); err != nil {
			ui.showError(err)
			return
		}
		ui.statusLabel.SetText("Stopped")
		ui.refreshPlaybackUI()
	})

	ui.buildSliders()
	ui.buildModelControls()
	ui.analysisView = newAnalysisView(ui.engine.analysisSnapshot)
	ui.analysisView.raster.SetMinSize(fyne.NewSize(520, 110))
	ui.leftMeter, ui.rightMeter = widget.NewProgressBar(), widget.NewProgressBar()
	ui.leftMeter.TextFormatter = func() string { return "" }
	ui.rightMeter.TextFormatter = func() string { return "" }
	ui.leftLevel, ui.rightLevel = widget.NewLabel("-∞ dBFS"), widget.NewLabel("-∞ dBFS")
	ui.refreshPlaybackUI()
	return ui
}

type demoRecording struct {
	label string
	path  string
}

func (ui *appUI) buildDemoControls() {
	demos := discoverDemoRecordings()
	options := make([]string, 0, len(demos))
	ui.demoPaths = make(map[string]string, len(demos))
	for _, demo := range demos {
		options = append(options, demo.label)
		ui.demoPaths[demo.label] = demo.path
	}
	ui.demoSelect = widget.NewSelect(options, func(string) {
		if ui.loadDemoButton != nil {
			ui.loadDemoButton.Enable()
		}
	})
	ui.demoSelect.PlaceHolder = "Try a sample recording"
	ui.loadDemoButton = widget.NewButtonWithIcon("Load sample", theme.MediaPlayIcon(), func() {
		if path := ui.demoPaths[ui.demoSelect.Selected]; path != "" {
			ui.loadPath(path)
		}
	})
	ui.loadDemoButton.Importance = widget.LowImportance
	if len(demos) == 0 {
		ui.demoSelect.PlaceHolder = "No bundled samples found"
		ui.demoSelect.Disable()
		ui.loadDemoButton.Disable()
	} else {
		ui.loadDemoButton.Disable()
	}
}

func discoverDemoRecordings() []demoRecording {
	executablePath, _ := os.Executable()
	candidates := []string{
		filepath.Join("assets", "audio"),
		filepath.Join(filepath.Dir(executablePath), "assets", "audio"),
		filepath.Join(filepath.Dir(executablePath), "..", "assets", "audio"),
	}
	for _, directory := range candidates {
		entries, err := os.ReadDir(directory)
		if err != nil {
			continue
		}
		demos := make([]demoRecording, 0, len(entries))
		for _, entry := range entries {
			if entry.IsDir() || !equalFoldExtension(entry.Name(), ".wav") {
				continue
			}
			path, err := filepath.Abs(filepath.Join(directory, entry.Name()))
			if err != nil {
				continue
			}
			name := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
			name = strings.ReplaceAll(name, "-", " ")
			if name == "" {
				continue
			}
			name = strings.ToUpper(name[:1]) + name[1:]
			demos = append(demos, demoRecording{label: name, path: path})
		}
		if len(demos) > 0 {
			sort.Slice(demos, func(i, j int) bool { return demos[i].label < demos[j].label })
			return demos
		}
	}
	return nil
}

func (ui *appUI) buildOutputLevelControl() {
	ui.outputLevelLabel = newCaptionLabel("Playback trim · −12 dB")
	ui.outputLevelSlider = widget.NewSlider(-24, 0)
	ui.outputLevelSlider.Step = 1
	ui.outputLevelSlider.Value = ui.outputLevelDB
	ui.outputLevelSlider.OnChanged = func(levelDB float64) {
		ui.outputLevelDB = levelDB
		ui.outputLevelLabel.SetText(fmt.Sprintf("Playback trim · %+.0f dB", levelDB))
		if err := ui.engine.setOutputLevel(levelDB); err != nil {
			ui.showError(err)
		}
	}
}

func (ui *appUI) applyClipTimeEntries() {
	snapshot := ui.engine.snapshot()
	sampleRate := int(snapshot.format.SampleRate)
	if sampleRate <= 0 {
		return
	}
	start, startErr := strconv.ParseFloat(strings.TrimSpace(ui.clipStartEntry.Text), 64)
	end, endErr := strconv.ParseFloat(strings.TrimSpace(ui.clipEndEntry.Text), 64)
	if startErr != nil || endErr != nil || !isFinite(start) || !isFinite(end) || start < 0 || end < 0 {
		ui.statusLabel.SetText("Enter valid start and end times in seconds, then press Enter.")
		return
	}
	startFrame := int(math.Round(start * float64(sampleRate)))
	endFrame := int(math.Round(end * float64(sampleRate)))
	startFrame, endFrame = ui.clipSelector.normalizeSelection(startFrame, endFrame)
	ui.clipSelector.SetSelection(startFrame, endFrame)
	ui.updateClipLabels(startFrame, endFrame)
}

func (ui *appUI) buildModelControls() {
	ui.severitySlider = widget.NewSlider(0, 1)
	ui.severitySlider.Step = 0.01
	ui.severitySlider.Value = 0
	ui.severitySlider.OnChanged = func(value float64) {
		if ui.updatingControls {
			return
		}
		ui.applyHearingSeverity(value)
	}
	ui.tinnitusSeveritySlider = widget.NewSlider(0, 1)
	ui.tinnitusSeveritySlider.Step = 0.01
	ui.tinnitusSeveritySlider.Value = 0
	ui.tinnitusSeveritySlider.OnChanged = func(value float64) {
		if ui.updatingControls {
			return
		}
		ui.applyTinnitusSeverity(value)
	}
}

func (ui *appUI) buildSliders() {
	ui.sliders = make([]*widget.Slider, 0, len(equalizerFrequencies))
	bandCards := make([]fyne.CanvasObject, 0, len(equalizerFrequencies))

	for band, frequency := range equalizerFrequencies {
		bandIndex := band
		readout := widget.NewLabel(formatGain(0))
		readout.Alignment = fyne.TextAlignCenter
		readout.SizeName = theme.SizeNameCaptionText

		slider := widget.NewSlider(MinGainDB, MaxGainDB)
		slider.Step = 0.1
		slider.Orientation = widget.Vertical
		slider.Value = 0
		slider.OnChanged = func(value float64) {
			readout.SetText(formatGain(value))
			if ui.updatingControls {
				return
			}
			if ui.engine != nil && ui.engine.hasAsset() {
				if err := ui.engine.setGain(bandIndex, value); err != nil {
					ui.showError(err)
				}
			}
		}

		resetButton := widget.NewButton("Reset", func() {
			slider.SetValue(0)
		})
		resetButton.Importance = widget.LowImportance
		frequencyLabel := widget.NewLabel(formatFrequency(frequency))
		frequencyLabel.Alignment = fyne.TextAlignCenter
		frequencyLabel.SizeName = theme.SizeNameCaptionText

		sliderBackground := canvas.NewRectangle(color.Transparent)
		sliderBackground.SetMinSize(fyne.NewSize(52, bandSliderHeight))
		sliderArea := container.NewStack(sliderBackground, slider)
		card := container.NewVBox(readout, sliderArea, frequencyLabel, resetButton)
		cardBackground := canvas.NewRectangle(theme.Color(theme.ColorNameButton))
		cardBackground.CornerRadius = 10
		cardBackground.SetMinSize(fyne.NewSize(82, bandSliderHeight+80))
		card = container.NewStack(cardBackground, card)

		ui.sliders = append(ui.sliders, slider)
		bandCards = append(bandCards, card)
	}

	ui.bandCards = container.NewGridWithColumns(5, bandCards...)
}

func (ui *appUI) content() fyne.CanvasObject {
	openButton := widget.NewButtonWithIcon("Choose recording", theme.FolderOpenIcon(), func() { ui.showAudioChooser() })
	openButton.Importance = widget.HighImportance
	fileRow := container.NewHBox(openButton, ui.demoSelect, ui.loadDemoButton)
	playbackButtons := container.NewHBox(ui.playButton, ui.pauseButton, ui.stopButton)
	outputLevelRow := container.NewBorder(nil, nil, ui.outputLevelLabel, nil, ui.outputLevelSlider)
	playbackAndLevel := container.NewGridWithColumns(2, playbackButtons, outputLevelRow)
	sourceContent := container.NewVBox(fileRow, ui.fileLabel, playbackAndLevel, ui.formatLabel)
	sourceCard := newSurfaceCard("Choose a recording", "Starts 12 dB below the recording; adjust to a comfortable listening level.", sourceContent)

	savePreset := widget.NewButtonWithIcon("Save profile", theme.DocumentSaveIcon(), func() { ui.showPresetSave() })
	hearingPresets := widget.NewButtonWithIcon("Hearing-loss presets", theme.DocumentIcon(), func() { ui.showPresetLibrary("hearing-loss") })
	tinnitusPresets := widget.NewButtonWithIcon("Tinnitus presets", theme.DocumentIcon(), func() { ui.showPresetLibrary("tinnitus") })
	loadPreset := widget.NewButtonWithIcon("Load saved profile", theme.FolderOpenIcon(), func() { ui.showPresetOpen() })
	resetAll := widget.NewButtonWithIcon("Reset EQ", theme.ViewRefreshIcon(), func() { ui.resetAll() })
	savePreset.Importance, loadPreset.Importance, resetAll.Importance = widget.LowImportance, widget.LowImportance, widget.LowImportance
	hearingPresets.Importance, tinnitusPresets.Importance = widget.HighImportance, widget.HighImportance
	profileActions := container.NewHBox(savePreset, loadPreset, resetAll)
	hearingDescription := newBodyLabel("Explore changes in audibility, detail, and loudness.")
	hearingControl := container.NewBorder(ui.hearingLabel, nil, widget.NewLabel("Severity"), nil, ui.severitySlider)
	hearingColumn := container.NewVBox(newSectionLabel("Hearing loss"), hearingDescription, hearingControl, hearingPresets)
	tinnitusDescription := newBodyLabel("Explore tonal, noise-like, or fluctuating sound layers.")
	tinnitusControl := container.NewBorder(ui.tinnitusLabel, nil, widget.NewLabel("Severity"), nil, ui.tinnitusSeveritySlider)
	tinnitusColumn := container.NewVBox(newSectionLabel("Tinnitus"), tinnitusDescription, tinnitusControl, tinnitusPresets)
	modelColumns := container.NewGridWithColumns(2, hearingColumn, tinnitusColumn)
	modelContent := container.NewVBox(modelColumns, profileActions)
	modelCard := newSurfaceCard("Explore listening layers", "Choose one or both profiles, then adjust their strength.", modelContent)

	startTimeField := container.NewBorder(widget.NewLabel("Start (seconds)"), nil, nil, nil, ui.clipStartEntry)
	endTimeField := container.NewBorder(widget.NewLabel("End (seconds)"), nil, nil, nil, ui.clipEndEntry)
	clipSelectionLabels := container.NewGridWithColumns(3, startTimeField, endTimeField, ui.clipLengthLabel)
	clipTimeline := container.NewBorder(nil, nil, newCaptionLabel("0:00"), ui.clipTimelineEnd, nil)
	clipActions := container.NewHBox(ui.previewClipButton, ui.exportClipButton)
	clipNameRow := container.NewBorder(widget.NewLabel("File name"), nil, nil, clipActions, ui.clipNameEntry)
	clipContent := container.NewVBox(
		newBodyLabel("Select 10–30 seconds. Drag the range or enter exact start and end times."),
		ui.clipSelector,
		clipTimeline,
		clipSelectionLabels,
		ui.clipAvailabilityLabel,
		clipNameRow,
		newCaptionLabel("A new WAV is created; your original stays unchanged."),
	)
	clipCard := newSurfaceCard("Export a clip · optional", "", clipContent)

	leftMeter := container.NewVBox(
		container.NewBorder(nil, nil, newCaptionLabel("Left"), ui.leftLevel, nil),
		ui.leftMeter,
	)
	rightMeter := container.NewVBox(
		container.NewBorder(nil, nil, newCaptionLabel("Right"), ui.rightLevel, nil),
		ui.rightMeter,
	)
	meters := container.NewGridWithColumns(2, leftMeter, rightMeter)
	viewLabel := newCaptionLabel("Waveform · blue = left · orange = right · spectrum = green")
	viewLabel.Alignment = fyne.TextAlignCenter
	analysisPanel := container.NewVBox(meters, viewLabel, ui.analysisView)
	analysisCard := newSurfaceCard("Listen and observe", "Meters and graph show processed audio, not a clinical measurement.", analysisPanel)
	eqHint := newBodyLabel("Hearing-loss strength shifts the curve; each slider adjusts one frequency band.")
	eqContent := container.NewVBox(eqHint, container.NewPadded(ui.bandCards))
	eqCard := newSurfaceCard("Fine-tune the frequency curve", "Adjust individual bands after exploring a profile.", eqContent)

	headerTitle := widget.NewLabelWithStyle("Listen with a different perspective", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	headerTitle.SizeName = theme.SizeNameSubHeadingText
	headerNote := newCaptionLabel("Load a recording → choose a profile → listen and adjust. Export is optional; no simulation represents everyone’s lived experience.")
	aboutButton := widget.NewButtonWithIcon("About this experience", theme.InfoIcon(), func() { ui.showAbout() })
	aboutButton.Importance = widget.LowImportance
	header := container.NewBorder(nil, nil, nil, aboutButton, container.NewPadded(container.NewVBox(headerTitle, headerNote)))
	leftColumn := container.NewVBox(sourceCard, modelCard, clipCard)
	rightColumn := container.NewVBox(analysisCard, eqCard)
	columns := container.NewHSplit(container.NewPadded(leftColumn), container.NewPadded(rightColumn))
	columns.Offset = 0.44
	bottom := container.NewBorder(nil, nil, ui.timeLabel, ui.statusLabel, ui.progress)
	return container.NewBorder(header, bottom, nil, nil, columns)
}

func newSurfaceCard(title, subtitle string, content fyne.CanvasObject) fyne.CanvasObject {
	titleLabel := widget.NewLabelWithStyle(title, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	titleLabel.SizeName = theme.SizeNameSubHeadingText
	headerItems := []fyne.CanvasObject{titleLabel}
	if strings.TrimSpace(subtitle) != "" {
		headerItems = append(headerItems, newCaptionLabel(subtitle))
	}
	header := container.NewVBox(headerItems...)
	body := container.NewVBox(header, content)
	background := canvas.NewRectangle(theme.Color(theme.ColorNameHeaderBackground))
	background.CornerRadius = theme.Size(theme.SizeNameCardRadius)
	return container.NewStack(background, container.NewPadded(body))
}

func newSectionLabel(text string) *widget.Label {
	label := widget.NewLabelWithStyle(text, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	label.SizeName = theme.SizeNameSubHeadingText
	return label
}

func newBodyLabel(text string) *widget.Label {
	label := widget.NewLabel(text)
	label.Wrapping = fyne.TextWrapWord
	return label
}

func newCaptionLabel(text string) *widget.Label {
	label := widget.NewLabel(text)
	label.SizeName = theme.SizeNameCaptionText
	label.Importance = widget.LowImportance
	return label
}

func (ui *appUI) showAbout() {
	dialog.ShowInformation("About Deaf Sim", "Deaf Sim is an educational listening tool. It helps you notice how changes in audibility, loudness growth, frequency detail, and tinnitus-like sound layers can affect a recording.\n\nPlease keep your listening level comfortable. These simulations are simplified and cannot represent the full lived experience of Deaf or hard-of-hearing people. If you have concerns about your hearing or tinnitus, speak with a qualified hearing health professional.", ui.window)
}

func (ui *appUI) showAudioChooser() {
	fileDialog := dialog.NewFileOpen(func(reader fyne.URIReadCloser, err error) {
		if err != nil {
			if reader != nil {
				_ = reader.Close()
			}
			ui.showError(err)
			return
		}
		if reader == nil {
			return
		}
		uri := reader.URI()
		_ = reader.Close()
		if uri == nil || uri.Scheme() != "file" {
			ui.showError(errors.New("please choose a local WAV file"))
			return
		}
		ui.loadPath(pathFromURI(uri))
	}, ui.window)
	fileDialog.SetFilter(storage.NewExtensionFileFilter([]string{".wav"}))
	fileDialog.Show()
}

func (ui *appUI) updateClipLabels(startFrame, endFrame int) {
	snapshot := ui.engine.snapshot()
	sampleRate := int(snapshot.format.SampleRate)
	if sampleRate <= 0 {
		ui.clipStartEntry.SetText("0.00")
		ui.clipEndEntry.SetText("0.00")
		ui.clipLengthLabel.SetText("Length 00.00 s")
		return
	}
	start := float64(startFrame) / float64(sampleRate)
	end := float64(endFrame) / float64(sampleRate)
	ui.clipStartEntry.SetText(fmt.Sprintf("%.2f", start))
	ui.clipEndEntry.SetText(fmt.Sprintf("%.2f", end))
	ui.clipLengthLabel.SetText(fmt.Sprintf("Length %05.2f s", end-start))
}

func (ui *appUI) setClipRange(snapshot audioSnapshot) {
	minFrames := snapshot.format.SampleRate.N(minClipDuration)
	maxFrames := snapshot.format.SampleRate.N(maxClipDuration)
	ui.clipSelector.SetRange(snapshot.totalN, minFrames, maxFrames)
	ui.clipTimelineEnd.SetText(formatTime(snapshot.duration))
	if snapshot.totalN < minFrames {
		ui.clipAvailabilityLabel.SetText("This recording is shorter than the 10-second minimum export length.")
	} else {
		ui.clipAvailabilityLabel.SetText(fmt.Sprintf("Full recording: %s · first 5 seconds of export are dry for comparison.", formatTime(snapshot.duration)))
	}
	startFrame, endFrame := ui.clipSelector.Selection()
	ui.updateClipLabels(startFrame, endFrame)
}

func (ui *appUI) previewClipSelection() {
	if !ui.engine.hasAsset() || ui.loading || ui.exporting.Load() {
		return
	}
	snapshot := ui.engine.snapshot()
	startFrame, endFrame := ui.clipSelector.Selection()
	if err := validateClipSelection(snapshot.format, snapshot.totalN, startFrame, endFrame); err != nil {
		ui.statusLabel.SetText("Choose a valid 10–30 second range to preview.")
		return
	}
	if err := ui.engine.previewSelection(startFrame, endFrame); err != nil {
		ui.showError(err)
		return
	}
	ui.statusLabel.SetText("Previewing selected range")
	ui.refreshPlaybackUI()
}

func (ui *appUI) clipSelectionAvailable(snapshot audioSnapshot) bool {
	if !snapshot.loaded || snapshot.format.SampleRate <= 0 {
		return false
	}
	startFrame, endFrame := ui.clipSelector.Selection()
	return validateClipSelection(snapshot.format, snapshot.totalN, startFrame, endFrame) == nil
}

func normalizeClipFileName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("enter a name for the exported clip")
	}
	if filepath.Base(name) != name || strings.ContainsAny(name, `/\\`) {
		return "", errors.New("clip name must be a file name, not a folder path")
	}
	if filepath.Ext(name) == "" {
		name += ".wav"
	}
	if !equalFoldExtension(name, ".wav") {
		return "", errors.New("clip exports must use the .wav extension")
	}
	return name, nil
}

func (ui *appUI) showClipExportSave() {
	if !ui.engine.hasAsset() || ui.loading {
		ui.showError(errors.New("choose a WAV recording before exporting a clip"))
		return
	}
	if ui.exporting.Load() {
		return
	}
	fileName, err := normalizeClipFileName(ui.clipNameEntry.Text)
	if err != nil {
		ui.showError(err)
		return
	}

	saveDialog := dialog.NewFileSave(func(writer fyne.URIWriteCloser, callbackErr error) {
		if callbackErr != nil {
			if writer != nil {
				_ = writer.Close()
			}
			ui.showError(callbackErr)
			return
		}
		if writer == nil {
			return
		}
		if writer.URI() == nil || !equalFoldExtension(writer.URI().Name(), ".wav") {
			_ = writer.Close()
			ui.showError(errors.New("choose a destination ending in .wav"))
			return
		}
		outputName := writer.URI().Name()
		if !ui.exporting.CompareAndSwap(false, true) {
			_ = writer.Close()
			return
		}

		snapshot := ui.engine.snapshot()
		startFrame, endFrame := ui.clipSelector.Selection()
		if err := validateClipSelection(snapshot.format, snapshot.totalN, startFrame, endFrame); err != nil {
			_ = writer.Close()
			ui.showError(err)
			return
		}
		gains := ui.currentGains()
		hearingLoss := ui.hearingLoss
		tinnitus := ui.tinnitus
		ui.statusLabel.SetText("Rendering selected clip…")
		ui.refreshPlaybackUI()

		go func() {
			renderErr := exportProcessedWAV(writer, snapshot.path, snapshot.format, startFrame, endFrame, gains, hearingLoss, tinnitus)
			closeErr := writer.Close()
			if renderErr == nil {
				renderErr = closeErr
			}
			fyne.Do(func() {
				ui.exporting.Store(false)
				if ui.closed.Load() {
					return
				}
				if renderErr != nil {
					ui.showError(renderErr)
					return
				}
				ui.statusLabel.SetText(fmt.Sprintf("Exported %s (%s).", outputName, formatClipDuration(startFrame, endFrame, int(snapshot.format.SampleRate))))
				ui.refreshPlaybackUI()
			})
		}()
	}, ui.window)
	saveDialog.SetFileName(fileName)
	saveDialog.SetFilter(storage.NewExtensionFileFilter([]string{".wav"}))
	saveDialog.Show()
}

func formatClipDuration(startFrame, endFrame int, sampleRate int) string {
	if sampleRate <= 0 || endFrame <= startFrame {
		return "00.00 s"
	}
	return fmt.Sprintf("%05.2f s", float64(endFrame-startFrame)/float64(sampleRate))
}

func (ui *appUI) loadPath(path string) {
	if ui.closed.Load() {
		return
	}
	if path == "" {
		ui.showError(errors.New("no WAV file was selected"))
		return
	}
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		ui.showError(err)
		return
	}

	requestID := ui.loadGeneration.Add(1)
	ui.loading = true
	ui.engine.unload()
	ui.clipSelector.SetRange(0, 0, 0)
	ui.clipTimelineEnd.SetText("00:00")
	ui.updateClipLabels(0, 0)
	ui.fileLabel.SetText(filepath.Base(absolutePath))
	ui.formatLabel.SetText("Loading…")
	ui.statusLabel.SetText("Loading recording…")
	ui.refreshPlaybackUI()

	go func() {
		asset, loadErr := loadAudioAsset(absolutePath)
		if ui.closed.Load() {
			if asset != nil {
				asset.close()
			}
			return
		}
		fyne.Do(func() {
			if ui.closed.Load() || requestID != ui.loadGeneration.Load() {
				if asset != nil {
					asset.close()
				}
				return
			}
			if loadErr != nil {
				ui.loading = false
				ui.formatLabel.SetText("No file loaded")
				ui.statusLabel.SetText("Could not load WAV file")
				ui.showError(loadErr)
				ui.refreshPlaybackUI()
				return
			}
			if err := ui.engine.install(asset); err != nil {
				ui.loading = false
				ui.formatLabel.SetText("No file loaded")
				ui.statusLabel.SetText("Could not initialize audio")
				ui.showError(err)
				ui.refreshPlaybackUI()
				return
			}
			if err := ui.engine.setOutputLevel(ui.outputLevelDB); err != nil {
				ui.showError(err)
			}

			ui.loading = false
			ui.setClipRange(ui.engine.snapshot())
			for band, slider := range ui.sliders {
				// Preserve the selected EQ curve when changing audio files.
				_ = ui.engine.setGain(band, slider.Value)
			}
			if err := ui.engine.setHearingLoss(ui.hearingLoss); err != nil {
				ui.showError(err)
			}
			if err := ui.engine.setTinnitus(ui.tinnitus); err != nil {
				ui.showError(err)
			}
			snapshot := ui.engine.snapshot()
			ui.formatLabel.SetText(formatAudioInfo(snapshot))
			ui.statusLabel.SetText("Recording ready. Press Play when you are comfortable.")
			ui.refreshPlaybackUI()
		})
	}()
}

func (ui *appUI) showPresetSave() {
	presetDialog := dialog.NewFileSave(func(writer fyne.URIWriteCloser, err error) {
		if err != nil {
			if writer != nil {
				_ = writer.Close()
			}
			ui.showError(err)
			return
		}
		if writer == nil {
			return
		}
		preset := presetFile{
			Version:     2,
			Scope:       "combined",
			Name:        "Saved Deaf Sim profile",
			Description: "Saved hearing-loss and tinnitus settings.",
			Frequencies: append([]float64(nil), equalizerFrequencies...),
			Gains:       ui.currentGains(),
			HearingLoss: ui.hearingLoss,
			Tinnitus:    ui.tinnitus,
		}
		encodeErr := json.NewEncoder(writer).Encode(preset)
		closeErr := writer.Close()
		if encodeErr != nil {
			ui.showError(encodeErr)
			return
		}
		if closeErr != nil {
			ui.showError(closeErr)
			return
		}
		ui.statusLabel.SetText("Preset saved.")
	}, ui.window)
	presetDialog.SetFileName("deaf-sim-profile.json")
	presetDialog.SetFilter(storage.NewExtensionFileFilter([]string{".json"}))
	presetDialog.Show()
}

func (ui *appUI) showPresetOpen() {
	presetDialog := dialog.NewFileOpen(func(reader fyne.URIReadCloser, err error) {
		if err != nil {
			if reader != nil {
				_ = reader.Close()
			}
			ui.showError(err)
			return
		}
		if reader == nil {
			return
		}
		uri := reader.URI()
		_ = reader.Close()
		if uri == nil || uri.Scheme() != "file" {
			ui.showError(errors.New("please choose a local JSON preset"))
			return
		}
		path := pathFromURI(uri)
		ui.loadPresetFile(path)
	}, ui.window)
	presetDialog.SetFilter(storage.NewExtensionFileFilter([]string{".json"}))
	presetDialog.Show()
}

type presetLibraryEntry struct {
	path        string
	name        string
	description string
}

func (ui *appUI) showPresetLibrary(scope string) {
	entries, err := discoverPresetLibrary(scope)
	if err != nil {
		ui.showError(err)
		return
	}

	selected := 0
	list := widget.NewList(
		func() int { return len(entries) },
		func() fyne.CanvasObject {
			return container.NewVBox(widget.NewLabel("Preset name"), newCaptionLabel("Preset description"))
		},
		func(id int, object fyne.CanvasObject) {
			row := object.(*fyne.Container)
			row.Objects[0].(*widget.Label).SetText(entries[id].name)
			row.Objects[1].(*widget.Label).SetText(entries[id].description)
		},
	)
	list.OnSelected = func(id int) { selected = id }
	listContainer := container.NewGridWrap(fyne.NewSize(680, 330), list)

	var presetDialog *dialog.CustomDialog
	useButton := widget.NewButtonWithIcon("Use selected preset", theme.ConfirmIcon(), func() {
		path := entries[selected].path
		presetDialog.Hide()
		ui.loadPresetFile(path)
	})
	useButton.Importance = widget.HighImportance

	label := "Choose a hearing-loss profile to explore its frequency pattern and severity control."
	title := "Hearing-loss presets"
	if scope == "tinnitus" {
		label = "Choose a tinnitus sound profile to explore its layers and severity control."
		title = "Tinnitus presets"
	}
	content := container.NewVBox(newBodyLabel(label), listContainer, useButton)
	presetDialog = dialog.NewCustom(title, "Cancel", content, ui.window)
	presetDialog.Show()
	list.Select(0)
}

func discoverPresetLibrary(scope string) ([]presetLibraryEntry, error) {
	directory, err := locatePresetDirectory(scope)
	if err != nil {
		return nil, err
	}
	files, err := os.ReadDir(directory)
	if err != nil {
		return nil, fmt.Errorf("could not read %s presets: %w", scope, err)
	}

	entries := make([]presetLibraryEntry, 0, len(files))
	for _, file := range files {
		if file.IsDir() || strings.ToLower(filepath.Ext(file.Name())) != ".json" {
			continue
		}
		path := filepath.Join(directory, file.Name())
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			continue
		}
		var preset presetFile
		if json.Unmarshal(data, &preset) != nil || preset.Scope != scope {
			continue
		}
		name := preset.Name
		if name == "" {
			name = strings.TrimSuffix(file.Name(), filepath.Ext(file.Name()))
		}
		description := preset.Description
		if description == "" {
			description = "Educational listening profile."
		}
		entries = append(entries, presetLibraryEntry{path: path, name: name, description: description})
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("no %s presets were found in %s", scope, directory)
	}
	return entries, nil
}

func locatePresetDirectory(scope string) (string, error) {
	executablePath, _ := filepath.Abs(os.Args[0])
	candidates := []string{
		filepath.Join("presets", scope),
		filepath.Join(filepath.Dir(executablePath), "presets", scope),
		filepath.Join(filepath.Dir(executablePath), "..", "presets", scope),
	}
	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err == nil && info.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("could not find the %s preset library", scope)
}

func (ui *appUI) loadPresetFile(path string) {
	if path == "" {
		ui.showError(errors.New("no preset was selected"))
		return
	}
	ui.statusLabel.SetText("Loading preset…")
	go func() {
		data, readErr := os.ReadFile(path)
		var preset presetFile
		if readErr == nil {
			readErr = json.Unmarshal(data, &preset)
		}
		if ui.closed.Load() {
			return
		}
		fyne.Do(func() {
			if ui.closed.Load() {
				return
			}
			if readErr != nil {
				ui.showError(readErr)
				return
			}
			if err := validatePreset(preset); err != nil {
				ui.showError(err)
				return
			}
			switch preset.Scope {
			case "hearing-loss":
				ui.applyHearingLoss(preset.HearingLoss, preset.Name)
			case "tinnitus":
				ui.applyTinnitus(preset.Tinnitus, preset.Name)
			case "combined":
				ui.applyPreset(preset.Gains)
				ui.applyHearingLoss(preset.HearingLoss, preset.Name)
				ui.applyTinnitus(preset.Tinnitus, preset.Name)
			}
			ui.statusLabel.SetText(fmt.Sprintf("Preset loaded: %s", preset.Name))
		})
	}()
}

type presetFile struct {
	Version     int                `json:"version"`
	Scope       string             `json:"scope"`
	Name        string             `json:"name,omitempty"`
	Description string             `json:"description,omitempty"`
	Frequencies []float64          `json:"frequencies"`
	Gains       []float64          `json:"gains"`
	HearingLoss HearingLossProfile `json:"hearing_loss"`
	Tinnitus    TinnitusPreset     `json:"tinnitus,omitempty"`
}

func validatePreset(preset presetFile) error {
	if preset.Version != 2 {
		return fmt.Errorf("preset version %d is not supported; expected version 2", preset.Version)
	}
	if preset.Scope != "hearing-loss" && preset.Scope != "tinnitus" && preset.Scope != "combined" {
		return errors.New("preset scope must be hearing-loss, tinnitus, or combined")
	}
	if len(preset.Gains) != len(equalizerFrequencies) {
		return fmt.Errorf("preset must contain %d gain values", len(equalizerFrequencies))
	}
	if len(preset.Frequencies) != 0 {
		if len(preset.Frequencies) != len(equalizerFrequencies) {
			return errors.New("preset frequency list does not match this equalizer")
		}
		for i, frequency := range preset.Frequencies {
			if math.Abs(frequency-equalizerFrequencies[i]) > 0.01 {
				return errors.New("preset frequency list does not match this equalizer")
			}
		}
	}
	for _, gain := range preset.Gains {
		if !isFinite(gain) || gain < MinGainDB || gain > MaxGainDB {
			return fmt.Errorf("preset gain values must be between %.1f and %.1f dB", MinGainDB, MaxGainDB)
		}
	}
	if err := validateTinnitus(preset.Tinnitus); err != nil {
		return err
	}
	if err := validateHearingLoss(preset.HearingLoss); err != nil {
		return err
	}
	return nil
}

func (ui *appUI) applyPreset(gains []float64) {
	for band, gain := range gains {
		ui.sliders[band].SetValue(gain)
		if ui.engine.hasAsset() {
			_ = ui.engine.setGain(band, gain)
		}
	}
	ui.refreshPlaybackUI()
}

func (ui *appUI) applyTinnitus(preset TinnitusPreset, name string) {
	ui.tinnitus = preset
	ui.tinnitusProfileName = strings.TrimSpace(name)
	ui.updatingControls = true
	ui.tinnitusSeveritySlider.SetValue(clamp(preset.Severity, 0, 1))
	ui.updatingControls = false
	ui.applyTinnitusSeverity(preset.Severity)
}

func (ui *appUI) applyHearingLoss(profile HearingLossProfile, name string) {
	ui.hearingLoss = profile
	ui.hearingProfileName = strings.TrimSpace(name)
	ui.updatingControls = true
	ui.severitySlider.SetValue(clamp(profile.Severity, 0, 1))
	ui.updatingControls = false
	ui.applyHearingSeverity(profile.Severity)
}

func (ui *appUI) applyHearingSeverity(severity float64) {
	severity = clamp(severity, 0, 1)
	ui.hearingLoss.Severity = severity
	gains := make([]float64, len(ui.sliders))
	if ui.hearingLoss.Enabled {
		ui.updatingControls = true
		for band, threshold := range ui.hearingLoss.ThresholdsDB {
			gain := -threshold * severity
			gains[band] = gain
			ui.sliders[band].SetValue(gain)
		}
		ui.updatingControls = false
		profileName := ui.hearingProfileName
		if profileName == "" {
			profileName = ui.hearingLoss.Type + " profile"
		}
		severityText := fmt.Sprintf("%d%% severity", int(math.Round(severity*100)))
		if severity == 0 {
			severityText = "0% · no effect"
		}
		ui.hearingLabel.SetText(fmt.Sprintf("%s · %s · %s", profileName, ui.hearingLoss.Type, severityText))
	} else {
		ui.hearingLabel.SetText("Off · no hearing-loss profile")
	}
	if ui.engine.hasAsset() {
		if !ui.hearingLoss.Enabled {
			for band, slider := range ui.sliders {
				gains[band] = slider.Value
			}
		}
		if err := ui.engine.setHearingLossAndGains(ui.hearingLoss, gains); err != nil {
			ui.showError(err)
		}
	}
}

func (ui *appUI) applyTinnitusSeverity(severity float64) {
	severity = clamp(severity, 0, 1)
	ui.tinnitus.Severity = severity
	if ui.tinnitus.Enabled {
		severityText := fmt.Sprintf("%d%% severity", int(math.Round(severity*100)))
		if severity == 0 {
			severityText = "0% · no effect"
		}
		profileName := ui.tinnitusProfileName
		if profileName == "" {
			profileName = "Tinnitus profile"
		}
		ui.tinnitusLabel.SetText(fmt.Sprintf("%s · %s · %d components", profileName, severityText, len(ui.tinnitus.Components)))
	} else {
		ui.tinnitusLabel.SetText("Off · no tinnitus layer")
	}
	if ui.engine.hasAsset() {
		if err := ui.engine.setTinnitus(ui.tinnitus); err != nil {
			ui.showError(err)
		}
	}
}

func (ui *appUI) resetAll() {
	for _, slider := range ui.sliders {
		slider.SetValue(0)
	}
	ui.statusLabel.SetText("Frequency adjustments reset. Profile controls are unchanged.")
	ui.refreshPlaybackUI()
}

func (ui *appUI) currentGains() []float64 {
	if gains := ui.engine.gains(); gains != nil {
		return gains
	}
	gains := make([]float64, len(ui.sliders))
	for i, slider := range ui.sliders {
		gains[i] = slider.Value
	}
	return gains
}

func (ui *appUI) showError(err error) {
	if err == nil {
		return
	}
	ui.statusLabel.SetText("Error: " + err.Error())
	// A dialog makes file and preset errors visible even when the status line
	// is narrow. It is created from a Fyne callback, so this remains UI-safe.
	dialog.ShowError(err, ui.window)
}

func (ui *appUI) refreshPlaybackUI() {
	snapshot := ui.engine.snapshot()
	analysis := ui.engine.analysisSnapshot()
	loaded := snapshot.loaded && !ui.loading
	if loaded {
		ui.playButton.Enable()
		ui.pauseButton.Enable()
		ui.stopButton.Enable()
		ui.formatLabel.SetText(formatAudioInfo(snapshot))
	} else {
		ui.playButton.Disable()
		ui.pauseButton.Disable()
		ui.stopButton.Disable()
	}
	for _, slider := range ui.sliders {
		if loaded {
			slider.Enable()
		} else {
			slider.Disable()
		}
	}
	canSelectClip := loaded && !ui.exporting.Load() && snapshot.totalN >= snapshot.format.SampleRate.N(minClipDuration)
	if canSelectClip {
		ui.clipSelector.Enable()
	} else {
		ui.clipSelector.Disable()
	}
	if loaded && !ui.exporting.Load() {
		ui.clipNameEntry.Enable()
		ui.clipStartEntry.Enable()
		ui.clipEndEntry.Enable()
	} else {
		ui.exportClipButton.Disable()
		ui.clipNameEntry.Disable()
		ui.clipStartEntry.Disable()
		ui.clipEndEntry.Disable()
	}
	if loaded && !ui.exporting.Load() && ui.clipSelectionAvailable(snapshot) {
		ui.exportClipButton.Enable()
		ui.previewClipButton.Enable()
	} else {
		ui.exportClipButton.Disable()
		ui.previewClipButton.Disable()
	}

	if snapshot.duration <= 0 {
		ui.progress.SetValue(0)
	} else {
		ratio := float64(snapshot.position) / float64(snapshot.duration)
		ui.progress.SetValue(clamp(ratio, 0, 1))
	}
	ui.timeLabel.SetText(formatTime(snapshot.position) + " / " + formatTime(snapshot.duration))
	ui.leftMeter.SetValue(levelMeterValue(analysis.leftRMS))
	ui.rightMeter.SetValue(levelMeterValue(analysis.rightRMS))
	ui.leftLevel.SetText(formatLevelDBFS(analysis.leftRMS))
	ui.rightLevel.SetText(formatLevelDBFS(analysis.rightRMS))
	ui.analysisView.Refresh()
}

func (ui *appUI) startProgressUpdater() {
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if ui.closed.Load() {
					return
				}
				fyne.Do(func() {
					if !ui.closed.Load() {
						ui.refreshPlaybackUI()
					}
				})
			case <-ui.updatesDone:
				return
			}
		}
	}()
}

func (ui *appUI) shutdown() {
	ui.shutdownOnce.Do(func() {
		ui.closed.Store(true)
		ui.loadGeneration.Add(1)
		close(ui.updatesDone)
		ui.engine.close()
	})
}

func pathFromURI(uri fyne.URI) string {
	path := filepath.FromSlash(uri.Path())
	if runtime.GOOS == "windows" && strings.HasPrefix(path, `\`) && len(path) > 2 && path[2] == ':' {
		path = path[1:]
	}
	return filepath.Clean(path)
}

func formatAudioInfo(snapshot audioSnapshot) string {
	channelDescription := fmt.Sprintf("%d channels", snapshot.format.NumChannels)
	switch snapshot.format.NumChannels {
	case 1:
		channelDescription = "mono"
	case 2:
		channelDescription = "stereo"
	}
	return fmt.Sprintf("%.1f kHz · %s · %s", float64(snapshot.format.SampleRate)/1000, channelDescription, formatTime(snapshot.duration))
}

func formatFrequency(frequency float64) string {
	if frequency >= 1000 {
		return fmt.Sprintf("%.0f kHz", frequency/1000)
	}
	return fmt.Sprintf("%.0f Hz", frequency)
}

func formatGain(gain float64) string {
	if math.Abs(gain) < 0.05 {
		return "0.0 dB"
	}
	return fmt.Sprintf("%+.1f dB", gain)
}

func levelMeterValue(rms float64) float64 {
	if rms <= 0 {
		return 0
	}
	return clamp((20*math.Log10(rms)+60)/60, 0, 1)
}

func formatLevelDBFS(rms float64) string {
	if rms <= 0.000001 {
		return "-∞ dBFS"
	}
	return fmt.Sprintf("%+.1f dBFS", 20*math.Log10(rms))
}

func formatTime(duration time.Duration) string {
	if duration < 0 {
		duration = 0
	}
	totalSeconds := int(duration / time.Second)
	hours := totalSeconds / 3600
	minutes := (totalSeconds % 3600) / 60
	seconds := totalSeconds % 60
	if hours > 0 {
		return fmt.Sprintf("%d:%02d:%02d", hours, minutes, seconds)
	}
	return fmt.Sprintf("%02d:%02d", minutes, seconds)
}
