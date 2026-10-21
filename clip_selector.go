package main

import (
	"image/color"
	"math"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

const (
	clipHandleNone = iota
	clipHandleStart
	clipHandleEnd
	clipHandleRange
)

// clipRangeSelector presents a two-handle, frame-accurate selection control.
// The track represents the whole recording; minFrames and maxFrames constrain
// the selected window without restricting where that window can be placed.
type clipRangeSelector struct {
	widget.BaseWidget

	totalFrames   int
	minFrames     int
	maxFrames     int
	startFrame    int
	endFrame      int
	active        int
	dragOrigin    int
	originalStart int
	originalEnd   int
	hovered       bool
	disabled      bool

	OnChanged func(startFrame, endFrame int)
}

var (
	_ fyne.Tappable     = (*clipRangeSelector)(nil)
	_ fyne.Draggable    = (*clipRangeSelector)(nil)
	_ desktop.Hoverable = (*clipRangeSelector)(nil)
	_ fyne.Disableable  = (*clipRangeSelector)(nil)
)

func newClipRangeSelector() *clipRangeSelector {
	selector := &clipRangeSelector{}
	selector.ExtendBaseWidget(selector)
	return selector
}

func (s *clipRangeSelector) SetRange(totalFrames, minFrames, maxFrames int) {
	if totalFrames < 0 {
		totalFrames = 0
	}
	if minFrames < 0 {
		minFrames = 0
	}
	if maxFrames <= 0 || maxFrames > totalFrames {
		maxFrames = totalFrames
	}
	s.totalFrames = totalFrames
	s.minFrames = minFrames
	s.maxFrames = maxFrames
	s.startFrame = 0
	s.endFrame = maxFrames
	if totalFrames >= minFrames && s.endFrame < minFrames {
		s.endFrame = minFrames
	}
	s.active = clipHandleNone
	s.Refresh()
}

func (s *clipRangeSelector) Selection() (startFrame, endFrame int) {
	return s.startFrame, s.endFrame
}

func (s *clipRangeSelector) SetSelection(startFrame, endFrame int) {
	startFrame, endFrame = s.normalizeSelection(startFrame, endFrame)
	s.setSelection(startFrame, endFrame, false)
}

func (s *clipRangeSelector) effectiveMinFrames() int {
	if s.totalFrames < s.minFrames {
		return s.totalFrames
	}
	return s.minFrames
}

func (s *clipRangeSelector) normalizeSelection(startFrame, endFrame int) (int, int) {
	domain := s.totalFrames
	if domain <= 0 {
		return 0, 0
	}
	if startFrame < 0 {
		startFrame = 0
	}
	if endFrame < 0 {
		endFrame = 0
	}
	if startFrame > domain {
		startFrame = domain
	}
	if endFrame > domain {
		endFrame = domain
	}
	if endFrame < startFrame {
		startFrame, endFrame = endFrame, startFrame
	}

	minLength := s.effectiveMinFrames()
	if endFrame-startFrame < minLength {
		if startFrame+minLength <= domain {
			endFrame = startFrame + minLength
		} else {
			endFrame = domain
			startFrame = endFrame - minLength
		}
	}
	if endFrame-startFrame > s.maxFrames {
		endFrame = startFrame + s.maxFrames
		if endFrame > domain {
			endFrame = domain
			startFrame = endFrame - s.maxFrames
		}
	}
	return startFrame, endFrame
}

func (s *clipRangeSelector) setSelection(startFrame, endFrame int, notify bool) {
	if startFrame == s.startFrame && endFrame == s.endFrame {
		return
	}
	s.startFrame = startFrame
	s.endFrame = endFrame
	s.Refresh()
	if notify && s.OnChanged != nil {
		s.OnChanged(s.startFrame, s.endFrame)
	}
}

func (s *clipRangeSelector) frameToX(frame int) float32 {
	pad := float32(14)
	width := s.Size().Width - pad*2
	if width <= 0 || s.totalFrames <= 0 {
		return pad
	}
	return pad + width*float32(frame)/float32(s.totalFrames)
}

func (s *clipRangeSelector) xToFrame(x float32) int {
	pad := float32(14)
	width := s.Size().Width - pad*2
	if width <= 0 || s.totalFrames <= 0 {
		return 0
	}
	ratio := float64((x - pad) / width)
	if ratio < 0 {
		ratio = 0
	}
	if ratio > 1 {
		ratio = 1
	}
	return int(math.Round(ratio * float64(s.totalFrames)))
}

func (s *clipRangeSelector) handleForX(x float32) int {
	startX := s.frameToX(s.startFrame)
	endX := s.frameToX(s.endFrame)
	if math.Abs(float64(x-startX)) <= math.Abs(float64(x-endX)) {
		return clipHandleStart
	}
	return clipHandleEnd
}

func (s *clipRangeSelector) moveHandle(x float32) {
	if s.totalFrames <= 0 {
		return
	}
	frame := s.xToFrame(x)
	minLength := s.effectiveMinFrames()
	if s.active == clipHandleStart {
		lower := s.endFrame - s.maxFrames
		if lower < 0 {
			lower = 0
		}
		upper := s.endFrame - minLength
		if upper < lower {
			upper = lower
		}
		if frame < lower {
			frame = lower
		}
		if frame > upper {
			frame = upper
		}
		s.setSelection(frame, s.endFrame, true)
	} else if s.active == clipHandleEnd {
		lower := s.startFrame + minLength
		upper := s.startFrame + s.maxFrames
		if upper > s.totalFrames {
			upper = s.totalFrames
		}
		if frame < lower {
			frame = lower
		}
		if frame > upper {
			frame = upper
		}
		s.setSelection(s.startFrame, frame, true)
	}
}

func (s *clipRangeSelector) beginDrag(x float32) {
	startX := s.frameToX(s.startFrame)
	endX := s.frameToX(s.endFrame)
	if x > startX+8 && x < endX-8 {
		s.active = clipHandleRange
		s.dragOrigin = s.xToFrame(x)
		s.originalStart = s.startFrame
		s.originalEnd = s.endFrame
		return
	}
	s.active = s.handleForX(x)
}

func (s *clipRangeSelector) moveRange(x float32) {
	if s.active != clipHandleRange {
		return
	}
	shift := s.xToFrame(x) - s.dragOrigin
	minShift := -s.originalStart
	maxShift := s.totalFrames - s.originalEnd
	if shift < minShift {
		shift = minShift
	}
	if shift > maxShift {
		shift = maxShift
	}
	s.setSelection(s.originalStart+shift, s.originalEnd+shift, true)
}

func (s *clipRangeSelector) Tapped(event *fyne.PointEvent) {
	if s.disabled {
		return
	}
	s.active = s.handleForX(event.Position.X)
	s.moveHandle(event.Position.X)
	s.active = clipHandleNone
}

func (s *clipRangeSelector) Dragged(event *fyne.DragEvent) {
	if s.disabled {
		return
	}
	if s.active == clipHandleNone {
		s.beginDrag(event.Position.X)
	}
	if s.active == clipHandleRange {
		s.moveRange(event.Position.X)
	} else {
		s.moveHandle(event.Position.X)
	}
}

func (s *clipRangeSelector) DragEnd() {
	s.active = clipHandleNone
}

func (s *clipRangeSelector) MouseIn(_ *desktop.MouseEvent) {
	s.hovered = true
	if !s.disabled {
		s.Refresh()
	}
}

func (s *clipRangeSelector) MouseMoved(_ *desktop.MouseEvent) {}

func (s *clipRangeSelector) MouseOut() {
	s.hovered = false
	if !s.disabled {
		s.Refresh()
	}
}

func (s *clipRangeSelector) Disable() {
	s.disabled = true
	s.active = clipHandleNone
	s.Refresh()
}

func (s *clipRangeSelector) Enable() {
	s.disabled = false
	s.Refresh()
}

func (s *clipRangeSelector) Disabled() bool { return s.disabled }

func (s *clipRangeSelector) MinSize() fyne.Size {
	return fyne.NewSize(360, 58)
}

func (s *clipRangeSelector) CreateRenderer() fyne.WidgetRenderer {
	track := canvas.NewRectangle(color.Transparent)
	selected := canvas.NewRectangle(color.Transparent)
	startHandle := canvas.NewRectangle(color.Transparent)
	endHandle := canvas.NewRectangle(color.Transparent)
	r := &clipRangeRenderer{
		selector:    s,
		track:       track,
		selected:    selected,
		startHandle: startHandle,
		endHandle:   endHandle,
		objects:     []fyne.CanvasObject{track, selected, startHandle, endHandle},
	}
	r.Refresh()
	return r
}

type clipRangeRenderer struct {
	selector    *clipRangeSelector
	track       *canvas.Rectangle
	selected    *canvas.Rectangle
	startHandle *canvas.Rectangle
	endHandle   *canvas.Rectangle
	objects     []fyne.CanvasObject
}

func (r *clipRangeRenderer) Destroy() {}

func (r *clipRangeRenderer) Objects() []fyne.CanvasObject { return r.objects }

func (r *clipRangeRenderer) Layout(size fyne.Size) {
	pad := float32(14)
	trackY := size.Height/2 - 6
	trackWidth := size.Width - pad*2
	if trackWidth < 0 {
		trackWidth = 0
	}
	r.track.Move(fyne.NewPos(pad, trackY))
	r.track.Resize(fyne.NewSize(trackWidth, 12))

	startX := r.selector.frameToX(r.selector.startFrame)
	endX := r.selector.frameToX(r.selector.endFrame)
	r.selected.Move(fyne.NewPos(startX, trackY))
	r.selected.Resize(fyne.NewSize(maxFloat32(0, endX-startX), 12))

	handleWidth := float32(10)
	handleHeight := float32(34)
	handleY := size.Height/2 - handleHeight/2
	r.startHandle.Move(fyne.NewPos(startX-handleWidth/2, handleY))
	r.startHandle.Resize(fyne.NewSize(handleWidth, handleHeight))
	r.endHandle.Move(fyne.NewPos(endX-handleWidth/2, handleY))
	r.endHandle.Resize(fyne.NewSize(handleWidth, handleHeight))
}

func (r *clipRangeRenderer) MinSize() fyne.Size { return fyne.NewSize(360, 58) }

func (r *clipRangeRenderer) Refresh() {
	th := r.selector.Theme()
	variant := fyne.CurrentApp().Settings().ThemeVariant()
	r.track.FillColor = th.Color(theme.ColorNameInputBackground, variant)
	if r.selector.disabled {
		r.selected.FillColor = th.Color(theme.ColorNameDisabled, variant)
		r.startHandle.FillColor = th.Color(theme.ColorNameDisabled, variant)
		r.endHandle.FillColor = th.Color(theme.ColorNameDisabled, variant)
	} else {
		r.selected.FillColor = th.Color(theme.ColorNameSelection, variant)
		r.startHandle.FillColor = th.Color(theme.ColorNamePrimary, variant)
		r.endHandle.FillColor = th.Color(theme.ColorNamePrimary, variant)
		if r.selector.hovered {
			r.startHandle.FillColor = th.Color(theme.ColorNameHover, variant)
			r.endHandle.FillColor = th.Color(theme.ColorNameHover, variant)
		}
	}
	r.Layout(r.selector.Size())
	canvas.Refresh(r.selector)
}

func maxFloat32(a, b float32) float32 {
	if a > b {
		return a
	}
	return b
}
