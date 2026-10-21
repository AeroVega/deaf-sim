package main

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// deafSimTheme keeps the interface calm and high-contrast while leaving icon
// glyphs and platform rendering to Fyne's default theme.
type deafSimTheme struct{}

func (deafSimTheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	dark := variant == theme.VariantDark
	if dark {
		switch name {
		case theme.ColorNameBackground:
			return color.NRGBA{R: 16, G: 24, B: 33, A: 255}
		case theme.ColorNameForeground:
			return color.NRGBA{R: 241, G: 247, B: 250, A: 255}
		case theme.ColorNameButton:
			return color.NRGBA{R: 32, G: 46, B: 58, A: 255}
		case theme.ColorNameInputBackground, theme.ColorNameMenuBackground, theme.ColorNameOverlayBackground:
			return color.NRGBA{R: 25, G: 36, B: 46, A: 255}
		case theme.ColorNameInputBorder:
			return color.NRGBA{R: 78, G: 101, B: 112, A: 255}
		case theme.ColorNamePrimary:
			return color.NRGBA{R: 83, G: 190, B: 184, A: 255}
		case theme.ColorNameForegroundOnPrimary:
			return color.NRGBA{R: 7, G: 37, B: 39, A: 255}
		case theme.ColorNameHover:
			return color.NRGBA{R: 54, G: 76, B: 88, A: 255}
		case theme.ColorNameFocus:
			return color.NRGBA{R: 133, G: 222, B: 215, A: 255}
		case theme.ColorNameDisabled:
			return color.NRGBA{R: 133, G: 149, B: 160, A: 255}
		case theme.ColorNameDisabledButton:
			return color.NRGBA{R: 31, G: 42, B: 51, A: 255}
		case theme.ColorNameHeaderBackground:
			return color.NRGBA{R: 24, G: 39, B: 51, A: 255}
		case theme.ColorNameSeparator:
			return color.NRGBA{R: 54, G: 72, B: 84, A: 255}
		case theme.ColorNameScrollBar:
			return color.NRGBA{R: 82, G: 112, B: 122, A: 255}
		case theme.ColorNameScrollBarBackground:
			return color.NRGBA{R: 29, G: 43, B: 53, A: 255}
		case theme.ColorNamePlaceHolder:
			return color.NRGBA{R: 150, G: 166, B: 176, A: 255}
		case theme.ColorNamePressed:
			return color.NRGBA{R: 45, G: 128, B: 127, A: 255}
		case theme.ColorNameError:
			return color.NRGBA{R: 220, G: 95, B: 92, A: 255}
		case theme.ColorNameForegroundOnError:
			return color.NRGBA{R: 25, G: 19, B: 20, A: 255}
		case theme.ColorNameSuccess:
			return color.NRGBA{R: 84, G: 181, B: 127, A: 255}
		case theme.ColorNameForegroundOnSuccess:
			return color.NRGBA{R: 11, G: 38, B: 25, A: 255}
		case theme.ColorNameWarning:
			return color.NRGBA{R: 224, G: 171, B: 77, A: 255}
		case theme.ColorNameForegroundOnWarning:
			return color.NRGBA{R: 48, G: 32, B: 9, A: 255}
		case theme.ColorNameSelection:
			return color.NRGBA{R: 40, G: 91, B: 92, A: 255}
		case theme.ColorNameShadow:
			return color.NRGBA{R: 0, G: 0, B: 0, A: 100}
		}
	}

	switch name {
	case theme.ColorNameBackground:
		return color.NRGBA{R: 244, G: 248, B: 250, A: 255}
	case theme.ColorNameForeground:
		return color.NRGBA{R: 22, G: 35, B: 46, A: 255}
	case theme.ColorNameButton:
		return color.NRGBA{R: 229, G: 238, B: 242, A: 255}
	case theme.ColorNameInputBackground, theme.ColorNameMenuBackground, theme.ColorNameOverlayBackground:
		return color.NRGBA{R: 255, G: 255, B: 255, A: 255}
	case theme.ColorNameInputBorder:
		return color.NRGBA{R: 181, G: 201, B: 209, A: 255}
	case theme.ColorNamePrimary:
		return color.NRGBA{R: 20, G: 111, B: 114, A: 255}
	case theme.ColorNameForegroundOnPrimary:
		return color.NRGBA{R: 255, G: 255, B: 255, A: 255}
	case theme.ColorNameHover:
		return color.NRGBA{R: 211, G: 231, B: 231, A: 255}
	case theme.ColorNameFocus:
		return color.NRGBA{R: 110, G: 192, B: 191, A: 255}
	case theme.ColorNameDisabled:
		return color.NRGBA{R: 128, G: 145, B: 155, A: 255}
	case theme.ColorNameDisabledButton:
		return color.NRGBA{R: 228, G: 235, B: 238, A: 255}
	case theme.ColorNameHeaderBackground:
		return color.NRGBA{R: 229, G: 241, B: 242, A: 255}
	case theme.ColorNameSeparator:
		return color.NRGBA{R: 211, G: 223, B: 229, A: 255}
	case theme.ColorNameScrollBar:
		return color.NRGBA{R: 145, G: 172, B: 181, A: 255}
	case theme.ColorNameScrollBarBackground:
		return color.NRGBA{R: 225, G: 235, B: 239, A: 255}
	case theme.ColorNamePlaceHolder:
		return color.NRGBA{R: 104, G: 125, B: 135, A: 255}
	case theme.ColorNamePressed:
		return color.NRGBA{R: 9, G: 83, B: 86, A: 255}
	case theme.ColorNameError:
		return color.NRGBA{R: 181, G: 52, B: 45, A: 255}
	case theme.ColorNameForegroundOnError:
		return color.NRGBA{R: 255, G: 255, B: 255, A: 255}
	case theme.ColorNameSuccess:
		return color.NRGBA{R: 32, G: 122, B: 77, A: 255}
	case theme.ColorNameForegroundOnSuccess:
		return color.NRGBA{R: 255, G: 255, B: 255, A: 255}
	case theme.ColorNameWarning:
		return color.NRGBA{R: 164, G: 111, B: 18, A: 255}
	case theme.ColorNameForegroundOnWarning:
		return color.NRGBA{R: 255, G: 255, B: 255, A: 255}
	case theme.ColorNameSelection:
		return color.NRGBA{R: 190, G: 226, B: 226, A: 255}
	case theme.ColorNameShadow:
		return color.NRGBA{R: 26, G: 52, B: 64, A: 45}
	}
	return theme.DefaultTheme().Color(name, variant)
}

func (deafSimTheme) Font(style fyne.TextStyle) fyne.Resource {
	return theme.DefaultTheme().Font(style)
}

func (deafSimTheme) Icon(name fyne.ThemeIconName) fyne.Resource {
	return theme.DefaultTheme().Icon(name)
}

func (deafSimTheme) Size(name fyne.ThemeSizeName) float32 {
	switch name {
	case theme.SizeNamePadding:
		return 12
	case theme.SizeNameInnerPadding:
		return 9
	case theme.SizeNameText:
		return 14
	case theme.SizeNameHeadingText:
		return 20
	case theme.SizeNameSubHeadingText:
		return 16
	case theme.SizeNameCaptionText:
		return 13
	case theme.SizeNameButtonRadius:
		return 10
	case theme.SizeNameCardRadius:
		return 14
	case theme.SizeNameInputBorder:
		return 1
	case theme.SizeNameScrollBar:
		return 10
	case theme.SizeNameScrollBarSmall:
		return 6
	}
	return theme.DefaultTheme().Size(name)
}

func newDeafSimTheme() fyne.Theme { return deafSimTheme{} }
