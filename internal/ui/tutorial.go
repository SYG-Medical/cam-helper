package ui

import (
	"fmt"

	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"nystavision/internal/i18n"
)

// TutorialStep describes one step of the tutorial.
type TutorialStep struct {
	// TargetWidget is the widget to highlight (can be nil for intro/outro).
	TargetWidget fyne.CanvasObject
	// TargetWidgetFunc is evaluated when the step is reached, allowing dynamic widgets.
	TargetWidgetFunc func() fyne.CanvasObject
	// OnEnter runs when the step is reached.
	OnEnter func()
	// OnLeave runs when the step is left.
	OnLeave func()
	TitleKey string
	DescKey  string
}

var currentTutorial *tutorialDialog

// ShowTutorial displays the interactive tutorial over the given window.
// onComplete is called when the tutorial finishes or is skipped.
func ShowTutorial(win fyne.Window, steps []TutorialStep, onComplete func()) {
	if len(steps) == 0 {
		if onComplete != nil {
			onComplete()
		}
		return
	}

	var tutDialog *tutorialDialog

	tutDialog = newTutorialDialog(win, steps, func() {
		// Skip / Finish
		tutDialog.hide()
		currentTutorial = nil
		if onComplete != nil {
			onComplete()
		}
	}, func(step int) {
		// Step changed — update highlight
		tutDialog.goTo(step)
	})

	currentTutorial = tutDialog
	tutDialog.show()
}

// tutorialDialog manages the tutorial overlay.
type tutorialDialog struct {
	win      fyne.Window
	steps    []TutorialStep
	current  int
	onFinish func()
	onStep   func(int)

	overlay *widget.PopUp
	highlight *canvas.Rectangle

	// content widgets
	stepLabel *widget.Label
	title     *widget.Label
	desc      *widget.RichText
	prevBtn   *widget.Button
	nextBtn   *widget.Button
	skipBtn   *widget.Button
}

func newTutorialDialog(win fyne.Window, steps []TutorialStep, onFinish func(), onStep func(int)) *tutorialDialog {
	td := &tutorialDialog{
		win:      win,
		steps:    steps,
		onFinish: onFinish,
		onStep:   onStep,
	}

	td.stepLabel = widget.NewLabelWithStyle("", fyne.TextAlignCenter, fyne.TextStyle{Italic: true})
	td.title = widget.NewLabelWithStyle("", fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
	td.title.Wrapping = fyne.TextWrapWord
	td.desc = widget.NewRichText()
	td.desc.Wrapping = fyne.TextWrapWord

	td.prevBtn = widget.NewButtonWithIcon(i18n.T("tutorial_prev"), theme.NavigateBackIcon(), func() {
		if td.current > 0 {
			if td.steps[td.current].OnLeave != nil {
				td.steps[td.current].OnLeave()
			}
			td.current--
			if td.steps[td.current].OnEnter != nil {
				td.steps[td.current].OnEnter()
			}
			td.refresh()
		}
	})

	td.nextBtn = widget.NewButtonWithIcon(i18n.T("tutorial_next"), theme.NavigateNextIcon(), func() {
		if td.current < len(td.steps)-1 {
			if td.steps[td.current].OnLeave != nil {
				td.steps[td.current].OnLeave()
			}
			td.current++
			if td.steps[td.current].OnEnter != nil {
				td.steps[td.current].OnEnter()
			}
			td.refresh()
		} else {
			if td.steps[td.current].OnLeave != nil {
				td.steps[td.current].OnLeave()
			}
			td.onFinish()
		}
	})
	td.nextBtn.Importance = widget.HighImportance

	td.skipBtn = widget.NewButton(i18n.T("tutorial_skip"), func() {
		if td.steps[td.current].OnLeave != nil {
			td.steps[td.current].OnLeave()
		}
		td.onFinish()
	})

	return td
}

func (td *tutorialDialog) refresh() {
	step := td.steps[td.current]
	total := len(td.steps)

	td.stepLabel.SetText(fmt.Sprintf(i18n.T("tutorial_step"), td.current+1, total))
	td.title.SetText(i18n.T(step.TitleKey))
	td.desc.ParseMarkdown(i18n.T(step.DescKey))

	// Update prev button
	td.prevBtn.SetText(i18n.T("tutorial_prev"))
	if td.current == 0 {
		td.prevBtn.Disable()
	} else {
		td.prevBtn.Enable()
	}

	// Update next button
	if td.current == len(td.steps)-1 {
		td.nextBtn.SetText(i18n.T("tutorial_finish"))
		td.nextBtn.Icon = theme.ConfirmIcon()
	} else {
		td.nextBtn.SetText(i18n.T("tutorial_next"))
		td.nextBtn.Icon = theme.NavigateNextIcon()
	}

	td.prevBtn.Refresh()
	td.nextBtn.Refresh()

	// Highlight target widget
	var target fyne.CanvasObject
	if step.TargetWidgetFunc != nil {
		target = step.TargetWidgetFunc()
	} else {
		target = step.TargetWidget
	}

	if target != nil {
		td.highlightWidget(target)
	} else {
		td.removeHighlight()
	}

	if td.overlay != nil {
		td.overlay.Refresh()
	}
}

func (td *tutorialDialog) highlightWidget(target fyne.CanvasObject) {
	if td.highlight == nil {
		return
	}

	pos := fyne.CurrentApp().Driver().AbsolutePositionForObject(target)
	size := target.Size()

	td.highlight.StrokeColor = theme.PrimaryColor()
	td.highlight.Move(fyne.NewPos(pos.X-3, pos.Y-3))
	td.highlight.Resize(fyne.NewSize(size.Width+6, size.Height+6))
	td.highlight.Refresh()
}

func (td *tutorialDialog) removeHighlight() {
	if td.highlight != nil {
		td.highlight.StrokeColor = color.Transparent
		td.highlight.Refresh()
	}
}

func (td *tutorialDialog) buildContent() fyne.CanvasObject {
	// Divider line
	separator := widget.NewSeparator()

	// Navigation row
	navRow := container.NewBorder(nil, nil, td.prevBtn, td.nextBtn, td.skipBtn)

	// Card content
	content := container.NewVBox(
		td.stepLabel,
		separator,
		td.title,
		widget.NewSeparator(),
		td.desc,
		widget.NewSeparator(),
		navRow,
	)

	// Wrap in a padded container with fixed width
	padded := container.NewPadded(content)
	return padded
}

func (td *tutorialDialog) show() {
	// Initialize and add the highlight to the overlay stack first.
	// This puts it below the interactive modal dialog (index 0),
	// so it doesn't block mouse/touch events destined for the dialog.
	td.highlight = canvas.NewRectangle(nil)
	td.highlight.StrokeColor = color.Transparent
	td.highlight.StrokeWidth = 3
	td.highlight.FillColor = nil
	td.win.Canvas().Overlays().Add(td.highlight)

	td.refresh()
	content := td.buildContent()

	td.overlay = widget.NewModalPopUp(content, td.win.Canvas())
	td.overlay.Resize(fyne.NewSize(420, 0))
	td.overlay.Show()

	if len(td.steps) > 0 && td.steps[0].OnEnter != nil {
		td.steps[0].OnEnter()
	}

	// Re-refresh to fix sizes and draw correct highlight
	td.refresh()
}

func BringTutorialToFront() {
	if currentTutorial != nil {
		currentTutorial.BringToFront()
	}
}

func RefreshTutorial() {
	if currentTutorial != nil {
		currentTutorial.refresh()
	}
}

func (td *tutorialDialog) BringToFront() {
	if td.highlight != nil {
		td.win.Canvas().Overlays().Remove(td.highlight)
		td.win.Canvas().Overlays().Add(td.highlight)
	}
	if td.overlay != nil {
		td.win.Canvas().Overlays().Remove(td.overlay)
		td.win.Canvas().Overlays().Add(td.overlay)
	}
}

func (td *tutorialDialog) hide() {
	if td.overlay != nil {
		td.overlay.Hide()
	}
	if td.highlight != nil {
		td.win.Canvas().Overlays().Remove(td.highlight)
		td.highlight = nil
	}
}

func (td *tutorialDialog) goTo(step int) {
	if step >= 0 && step < len(td.steps) {
		td.current = step
		td.refresh()
	}
}
