package main

import (
	"image"
	"image/color"
	"image/draw"
	"math"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/widget"
	"github.com/faiface/beep"
)

const analysisWindowSize = 1024

type analysisSample struct{ left, right float64 }

type analysisSnapshot struct {
	samples             []analysisSample
	leftRMS, rightRMS   float64
	leftPeak, rightPeak float64
}

// analysisTap observes the final processed stream without changing it.
type analysisTap struct {
	streamer beep.Streamer
	mu       sync.RWMutex
	ring     []analysisSample
	write    int
	count    int
}

func newAnalysisTap(streamer beep.Streamer) *analysisTap {
	return &analysisTap{streamer: streamer, ring: make([]analysisSample, analysisWindowSize)}
}

func (a *analysisTap) Stream(samples [][2]float64) (int, bool) {
	n, ok := a.streamer.Stream(samples)
	if n <= 0 {
		return n, ok
	}
	a.mu.Lock()
	for i := 0; i < n; i++ {
		a.ring[a.write] = analysisSample{left: samples[i][0], right: samples[i][1]}
		a.write = (a.write + 1) % len(a.ring)
		if a.count < len(a.ring) {
			a.count++
		}
	}
	a.mu.Unlock()
	return n, ok
}

func (a *analysisTap) Err() error { return a.streamer.Err() }

func (a *analysisTap) Reset() {
	if resetter, ok := a.streamer.(interface{ Reset() }); ok {
		resetter.Reset()
	}
	a.mu.Lock()
	for i := range a.ring {
		a.ring[i] = analysisSample{}
	}
	a.write, a.count = 0, 0
	a.mu.Unlock()
}

func (a *analysisTap) Snapshot() analysisSnapshot {
	a.mu.RLock()
	defer a.mu.RUnlock()
	result := analysisSnapshot{samples: make([]analysisSample, a.count)}
	if a.count == 0 {
		return result
	}
	start := (a.write - a.count + len(a.ring)) % len(a.ring)
	var leftSum, rightSum, leftPeak, rightPeak float64
	for i := 0; i < a.count; i++ {
		sample := a.ring[(start+i)%len(a.ring)]
		result.samples[i] = sample
		leftAbs, rightAbs := math.Abs(sample.left), math.Abs(sample.right)
		leftSum += sample.left * sample.left
		rightSum += sample.right * sample.right
		leftPeak, rightPeak = math.Max(leftPeak, leftAbs), math.Max(rightPeak, rightAbs)
	}
	result.leftRMS = math.Sqrt(leftSum / float64(a.count))
	result.rightRMS = math.Sqrt(rightSum / float64(a.count))
	result.leftPeak, result.rightPeak = leftPeak, rightPeak
	return result
}

type analysisView struct {
	*widget.BaseWidget
	read   func() analysisSnapshot
	raster *canvas.Raster
}

func newAnalysisView(read func() analysisSnapshot) *analysisView {
	v := &analysisView{BaseWidget: &widget.BaseWidget{}, read: read}
	v.raster = canvas.NewRaster(func(width, height int) image.Image {
		return renderAnalysis(width, height, v.read())
	})
	v.ExtendBaseWidget(v)
	return v
}

func (v *analysisView) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(v.raster)
}

func (v *analysisView) Refresh() { canvas.Refresh(v.raster) }

func renderAnalysis(width, height int, snapshot analysisSnapshot) image.Image {
	if width < 1 {
		width = 1
	}
	if height < 1 {
		height = 1
	}
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.Draw(img, img.Bounds(), &image.Uniform{C: color.RGBA{R: 18, G: 22, B: 29, A: 255}}, image.Point{}, draw.Src)
	waveHeight := height / 2
	drawGrid(img, 0, waveHeight, width, waveHeight)
	drawWaveform(img, snapshot.samples, 0, waveHeight, color.RGBA{R: 70, G: 190, B: 255, A: 255}, true)
	drawWaveform(img, snapshot.samples, 0, waveHeight, color.RGBA{R: 255, G: 170, B: 75, A: 230}, false)
	drawSpectrum(img, snapshot.samples, waveHeight, height-waveHeight)
	return img
}

func drawGrid(img *image.RGBA, x, y, width, height int) {
	grid := color.RGBA{R: 58, G: 66, B: 78, A: 255}
	for i := 1; i < 4; i++ {
		drawLine(img, x, y+i*height/4, x+width, y+i*height/4, grid)
	}
	drawLine(img, x, y+height/2, x+width, y+height/2, color.RGBA{R: 90, G: 100, B: 112, A: 255})
}

func drawWaveform(img *image.RGBA, samples []analysisSample, y, height int, c color.Color, left bool) {
	if len(samples) < 2 || height < 2 {
		return
	}
	previousX, previousY := 0, y+height/2
	for x := 0; x < img.Bounds().Dx(); x++ {
		index := x * (len(samples) - 1) / maxInt(1, img.Bounds().Dx()-1)
		sample := samples[int(index)].left
		if !left {
			sample = samples[int(index)].right
		}
		currentY := y + height/2 - int(clamp(sample, -1, 1)*float64(height/2-3))
		drawLine(img, previousX, previousY, x, currentY, c)
		previousX, previousY = x, currentY
	}
}

func drawSpectrum(img *image.RGBA, samples []analysisSample, y, height int) {
	if len(samples) < 32 || height < 2 {
		return
	}
	const bins = 64
	barWidth := math.Max(1, float64(img.Bounds().Dx())/bins)
	for bin := 1; bin < bins; bin++ {
		var real, imag float64
		for i, sample := range samples {
			value := (sample.left + sample.right) * 0.5
			window := 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(len(samples)-1))
			angle := 2 * math.Pi * float64(bin*i) / float64(len(samples))
			real += value * window * math.Cos(angle)
			imag -= value * window * math.Sin(angle)
		}
		magnitude := math.Sqrt(real*real+imag*imag) / float64(len(samples))
		barHeight := int(clamp(20*math.Log10(math.Max(magnitude, 1e-5))+100, 0, 100) / 100 * float64(height-3))
		x0 := int(float64(bin-1) * barWidth)
		x1 := int(float64(bin) * barWidth)
		for x := x0; x < x1 && x < img.Bounds().Dx(); x++ {
			for yy := y + height - 2; yy >= y+height-2-barHeight; yy-- {
				if yy >= y {
					img.Set(x, yy, color.RGBA{R: 105, G: 220, B: 145, A: 255})
				}
			}
		}
	}
}

func drawLine(img *image.RGBA, x0, y0, x1, y1 int, c color.Color) {
	dx, sx := absInt(x1-x0), 1
	if x0 > x1 {
		sx = -1
	}
	dy, sy := -absInt(y1-y0), 1
	if y0 > y1 {
		sy = -1
	}
	err := dx + dy
	for {
		if x0 >= 0 && y0 >= 0 && x0 < img.Bounds().Dx() && y0 < img.Bounds().Dy() {
			img.Set(x0, y0, c)
		}
		if x0 == x1 && y0 == y1 {
			break
		}
		e2 := 2 * err
		if e2 >= dy {
			err += dy
			x0 += sx
		}
		if e2 <= dx {
			err += dx
			y0 += sy
		}
	}
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
