package layout

import "image"

type Split struct {
	Window       image.Rectangle
	LeftRegion   image.Rectangle
	Sidebar      image.Rectangle
	PanelAreas   [3]image.Rectangle
	PanelContent [3]image.Rectangle
}

func Compute(windowWidth, windowHeight int) Split {
	if windowWidth < 640 {
		windowWidth = 640
	}
	if windowHeight < 540 {
		windowHeight = 540
	}

	const (
		outerPad        = 16
		innerPad        = 12
		sidebarWidth    = 330
		minSidebarWidth = 280
	)

	sidebarW := sidebarWidth
	if windowWidth/4 < sidebarW {
		sidebarW = windowWidth / 4
		if sidebarW < minSidebarWidth {
			sidebarW = minSidebarWidth
		}
	}
	if sidebarW > windowWidth-outerPad*3 {
		sidebarW = windowWidth - outerPad*3
	}

	leftX := outerPad
	leftY := outerPad
	leftW := windowWidth - outerPad*3 - sidebarW
	leftH := windowHeight - outerPad*2
	if leftW < 120 {
		leftW = 120
	}
	if leftH < 120 {
		leftH = 120
	}

	sidebarX := leftX + leftW + outerPad
	sidebarY := outerPad
	sidebarH := windowHeight - outerPad*2

	panelH := (leftH - innerPad*2) / 3

	var panelAreas [3]image.Rectangle
	var panelContent [3]image.Rectangle
	for index := range 3 {
		y := leftY + index*(panelH+innerPad)
		panelAreas[index] = image.Rect(leftX, y, leftX+leftW, y+panelH)
		panelContent[index] = fit16x9(panelAreas[index])
	}

	return Split{
		Window:       image.Rect(0, 0, windowWidth, windowHeight),
		LeftRegion:   image.Rect(leftX, leftY, leftX+leftW, leftY+leftH),
		Sidebar:      image.Rect(sidebarX, sidebarY, sidebarX+sidebarW, sidebarY+sidebarH),
		PanelAreas:   panelAreas,
		PanelContent: panelContent,
	}
}

func fit16x9(area image.Rectangle) image.Rectangle {
	areaW := area.Dx()
	areaH := area.Dy()
	if areaW <= 0 || areaH <= 0 {
		return area
	}

	contentW := areaW
	contentH := (contentW * 9) / 16
	if contentH > areaH {
		contentH = areaH
		contentW = (contentH * 16) / 9
	}

	x := area.Min.X + (areaW-contentW)/2
	y := area.Min.Y + (areaH-contentH)/2
	return image.Rect(x, y, x+contentW, y+contentH)
}
