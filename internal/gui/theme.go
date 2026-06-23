package gui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// SYG Medical brand colors extracted from the logo.
var (
	// Primary palette
	colorDeepTeal    = color.NRGBA{R: 26, G: 34, B: 44, A: 255}   // Dark slate gray-blue
	colorMedicalBlue = color.NRGBA{R: 46, G: 134, B: 193, A: 255} // #2E86C1
	colorFreshGreen  = color.NRGBA{R: 39, G: 174, B: 96, A: 255}  // #27AE60

	// Surface palette
	colorDarkSurface  = color.NRGBA{R: 12, G: 15, B: 19, A: 255}   // Deep blackish-gray (#0C0F13)
	colorCardSurface  = color.NRGBA{R: 22, G: 27, B: 34, A: 255}   // Dark card surface (#161B22)
	colorInputSurface = color.NRGBA{R: 30, G: 37, B: 47, A: 255}   // Input background (#1E252F)

	// Text palette
	colorTextPrimary   = color.NRGBA{R: 236, G: 240, B: 241, A: 255} // #ECF0F1
	colorTextSecondary = color.NRGBA{R: 149, G: 165, B: 166, A: 255} // #95A5A6
	colorSilver        = color.NRGBA{R: 189, G: 195, B: 199, A: 255} // #BDC3C7

	// Status palette
	colorAmberWarning = color.NRGBA{R: 243, G: 156, B: 18, A: 255} // #F39C12
	colorRedCritical  = color.NRGBA{R: 231, G: 76, B: 60, A: 255}  // #E74C3C
)

// SYGMedicalTheme implements fyne.Theme with SYG Medical branding.
// Dark-only theme designed for medical environments.
type SYGMedicalTheme struct{}

var _ fyne.Theme = (*SYGMedicalTheme)(nil)

func (t *SYGMedicalTheme) Color(name fyne.ThemeColorName, _ fyne.ThemeVariant) color.Color {
	// Force dark variant for all lookups
	switch name {
	// Primary & accent
	case theme.ColorNamePrimary, theme.ColorNameHyperlink:
		return colorMedicalBlue
	case theme.ColorNameFocus:
		return colorMedicalBlue
	case theme.ColorNameSelection:
		return color.NRGBA{R: 46, G: 134, B: 193, A: 60} // Medical Blue @ 24%

	// Buttons
	case theme.ColorNameButton:
		return colorDeepTeal
	case theme.ColorNameDisabledButton:
		return color.NRGBA{R: 44, G: 62, B: 80, A: 255} // Card Surface
	case theme.ColorNamePressed:
		return color.NRGBA{R: 20, G: 66, B: 95, A: 255} // Darker teal

	// Surfaces
	case theme.ColorNameBackground:
		return colorDarkSurface
	case theme.ColorNameOverlayBackground:
		return color.NRGBA{R: 28, G: 40, B: 51, A: 230} // Dark Surface @ 90%
	case theme.ColorNameMenuBackground:
		return colorCardSurface
	case theme.ColorNameHeaderBackground:
		return colorCardSurface
	case theme.ColorNameInputBackground:
		return colorInputSurface
	case theme.ColorNameInputBorder:
		return color.NRGBA{R: 93, G: 109, B: 126, A: 255} // Subtle border

	// Text
	case theme.ColorNameForeground:
		return colorTextPrimary
	case theme.ColorNamePlaceHolder:
		return colorTextSecondary
	case theme.ColorNameDisabled:
		return colorSilver
	case theme.ColorNameForegroundOnPrimary:
		return colorTextPrimary

	// Status colors
	case theme.ColorNameSuccess:
		return colorFreshGreen
	case theme.ColorNameForegroundOnSuccess:
		return colorTextPrimary
	case theme.ColorNameWarning:
		return colorAmberWarning
	case theme.ColorNameForegroundOnWarning:
		return colorDarkSurface
	case theme.ColorNameError:
		return colorRedCritical
	case theme.ColorNameForegroundOnError:
		return colorTextPrimary

	// Interactive
	case theme.ColorNameHover:
		return color.NRGBA{R: 46, G: 134, B: 193, A: 50} // Medical Blue @ 20%
	case theme.ColorNameScrollBar:
		return color.NRGBA{R: 149, G: 165, B: 166, A: 150} // Silver semi-transparent
	case theme.ColorNameScrollBarBackground:
		return color.NRGBA{R: 28, G: 40, B: 51, A: 100}

	// Structural
	case theme.ColorNameSeparator:
		return color.NRGBA{R: 189, G: 195, B: 199, A: 75} // Silver @ 30%
	case theme.ColorNameShadow:
		return color.NRGBA{R: 0, G: 0, B: 0, A: 80}

	default:
		// Fallback to Fyne's built-in dark theme
		return theme.DefaultTheme().Color(name, theme.VariantDark)
	}
}

func (t *SYGMedicalTheme) Font(style fyne.TextStyle) fyne.Resource {
	return theme.DefaultTheme().Font(style)
}

func (t *SYGMedicalTheme) Icon(name fyne.ThemeIconName) fyne.Resource {
	return theme.DefaultTheme().Icon(name)
}

func (t *SYGMedicalTheme) Size(name fyne.ThemeSizeName) float32 {
	switch name {
	case theme.SizeNamePadding:
		return 8 // More spacious (default: 4)
	case theme.SizeNameInnerPadding:
		return 12 // Roomier button interiors (default: 8)
	case theme.SizeNameText:
		return 14 // Slightly larger for readability (default: 13)
	case theme.SizeNameSubHeadingText:
		return 16
	case theme.SizeNameHeadingText:
		return 22
	case theme.SizeNameCaptionText:
		return 11
	case theme.SizeNameInputBorder:
		return 2 // More visible input borders (default: 1)
	case theme.SizeNameInputRadius:
		return 8 // Rounded corners for modern look (default: 5)
	case theme.SizeNameSelectionRadius:
		return 6
	case theme.SizeNameSeparatorThickness:
		return 1
	case theme.SizeNameScrollBarSmall:
		return 4
	default:
		return theme.DefaultTheme().Size(name)
	}
}
