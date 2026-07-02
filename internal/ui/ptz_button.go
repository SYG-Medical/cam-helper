package ui

import (
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"
)

// PTZButton is a widget.Button that also reports mouse press/release, so a
// motorized camera can be moved for as long as the button stays held down
// and stopped as soon as it's released.
type PTZButton struct {
	widget.Button
	OnPress   func()
	OnRelease func()
}

// NewPTZButton creates a PTZButton that calls onPress on mouse-down and
// onRelease on mouse-up.
func NewPTZButton(label string, onPress, onRelease func()) *PTZButton {
	b := &PTZButton{
		OnPress:   onPress,
		OnRelease: onRelease,
	}
	b.Text = label
	b.ExtendBaseWidget(b)
	return b
}

// MouseDown implements desktop.Mouseable.
func (b *PTZButton) MouseDown(me *desktop.MouseEvent) {
	if me.Button == desktop.MouseButtonPrimary && b.OnPress != nil {
		b.OnPress()
	}
}

// MouseUp implements desktop.Mouseable.
func (b *PTZButton) MouseUp(me *desktop.MouseEvent) {
	if me.Button == desktop.MouseButtonPrimary && b.OnRelease != nil {
		b.OnRelease()
	}
}
