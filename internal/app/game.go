package app

import (
	"fmt"
	"image"
	"image/color"
	"math"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/ebitenutil"
	"github.com/hajimehoshi/ebiten/v2/vector"
	"github.com/luigipizzolito/ditherprinter/internal/capture"
	"github.com/luigipizzolito/ditherprinter/internal/dither"
	"github.com/luigipizzolito/ditherprinter/internal/layout"
)

type Game struct {
	layout layout.Split

	capture *capture.PortalCapture

	algorithms []dither.Algorithm
	algorithm  dither.Algorithm
	resamples  []dither.ResampleAlgorithm
	params     dither.Params

	lastCaptureFrameID uint64
	sourceFrame        image.Image
	stageImages        [3]*ebiten.Image
	pipelineDirty      bool
	algorithmMenuOpen  bool
	resampleMenuOpen   bool

	lastMousePressed bool
}

func NewGame() *Game {
	return &Game{
		capture:    capture.NewPortalCapture(),
		algorithms: []dither.Algorithm{dither.AlgorithmThreshold, dither.AlgorithmFloyd},
		algorithm:  dither.AlgorithmThreshold,
		resamples: []dither.ResampleAlgorithm{
			dither.ResampleNearest,
			dither.ResampleBilinear,
			dither.ResampleBicubic,
			dither.ResampleLanczos,
		},
		params: dither.Params{
			Threshold: 0.5,
			Diffusion: 1.0,
			Levels:    2,
			Scale:     1.0,
			Resample:  dither.ResampleLanczos,
		},
		pipelineDirty: true,
	}
}

func (game *Game) Update() error {
	windowW, windowH := ebiten.WindowSize()
	game.layout = layout.Compute(windowW, windowH)

	mousePressed := ebiten.IsMouseButtonPressed(ebiten.MouseButtonLeft)
	justPressed := mousePressed && !game.lastMousePressed
	mouseX, mouseY := ebiten.CursorPosition()

	if justPressed {
		switch {
		case pointInRect(mouseX, mouseY, game.captureButtonRect()):
			if game.capture.IsRunning() {
				game.capture.Stop()
			} else {
				_ = game.capture.Start()
			}
			game.algorithmMenuOpen = false
			game.resampleMenuOpen = false
		case pointInRect(mouseX, mouseY, game.algorithmButtonRect()):
			game.algorithmMenuOpen = !game.algorithmMenuOpen
			game.resampleMenuOpen = false
		case pointInRect(mouseX, mouseY, game.resampleButtonRect()):
			game.resampleMenuOpen = !game.resampleMenuOpen
			game.algorithmMenuOpen = false
		case game.algorithmMenuOpen:
			if algorithm, ok := game.algorithmMenuSelection(mouseX, mouseY); ok {
				game.algorithm = algorithm
				game.pipelineDirty = true
			}
			game.algorithmMenuOpen = false
		case game.resampleMenuOpen:
			if resample, ok := game.resampleMenuSelection(mouseX, mouseY); ok {
				game.params.Resample = resample
				game.pipelineDirty = true
			}
			game.resampleMenuOpen = false
		}
	}

	if game.updateScaleSlider(mouseX, mouseY, mousePressed) {
		game.pipelineDirty = true
	}
	if game.updateLevelsSlider(mouseX, mouseY, mousePressed) {
		game.pipelineDirty = true
	}
	if game.updateSlider(mouseX, mouseY, mousePressed, game.thresholdSliderRect(), 0, 1, &game.params.Threshold) {
		game.pipelineDirty = true
	}
	if game.algorithm == dither.AlgorithmFloyd {
		if game.updateSlider(mouseX, mouseY, mousePressed, game.diffusionSliderRect(), 0, 1, &game.params.Diffusion) {
			game.pipelineDirty = true
		}
	}

	currentCaptureFrameID := game.capture.FrameID()
	if currentCaptureFrameID != game.lastCaptureFrameID {
		latest := game.capture.LatestFrame()
		if latest != nil {
			game.sourceFrame = latest
			game.lastCaptureFrameID = currentCaptureFrameID
			game.pipelineDirty = true
		}
	}

	if game.pipelineDirty && game.sourceFrame != nil {
		stage1, stage2, stage3 := dither.Process(game.sourceFrame, game.algorithm, game.params)
		game.stageImages[0] = ebiten.NewImageFromImage(stage1)
		game.stageImages[1] = ebiten.NewImageFromImage(stage2)
		game.stageImages[2] = ebiten.NewImageFromImage(stage3)
		game.pipelineDirty = false
	}

	game.lastMousePressed = mousePressed
	return nil
}

func (game *Game) Draw(screen *ebiten.Image) {
	screen.Fill(color.RGBA{R: 15, G: 17, B: 20, A: 255})

	for index := range 3 {
		panelArea := game.layout.PanelAreas[index]
		contentArea := game.layout.PanelContent[index]

		ebitenutil.DrawRect(screen, float64(panelArea.Min.X), float64(panelArea.Min.Y), float64(panelArea.Dx()), float64(panelArea.Dy()), color.RGBA{R: 29, G: 33, B: 39, A: 255})
		vector.StrokeRect(screen, float32(panelArea.Min.X), float32(panelArea.Min.Y), float32(panelArea.Dx()), float32(panelArea.Dy()), 2, color.RGBA{R: 70, G: 80, B: 92, A: 255}, false)

		ebitenutil.DrawRect(screen, float64(contentArea.Min.X), float64(contentArea.Min.Y), float64(contentArea.Dx()), float64(contentArea.Dy()), color.RGBA{R: 8, G: 9, B: 10, A: 255})
		vector.StrokeRect(screen, float32(contentArea.Min.X), float32(contentArea.Min.Y), float32(contentArea.Dx()), float32(contentArea.Dy()), 1, color.RGBA{R: 95, G: 105, B: 120, A: 255}, false)

		if game.stageImages[index] != nil {
			drawImageIntoRect(screen, game.stageImages[index], contentArea)
		} else {
			ebitenutil.DebugPrintAt(screen, "Waiting for live capture...", contentArea.Min.X+10, contentArea.Min.Y+10)
		}

		labels := [3]string{"Input", "Algorithm Preview", "Output"}
		ebitenutil.DebugPrintAt(screen, labels[index], panelArea.Min.X+8, panelArea.Min.Y+8)
	}

	sidebar := game.layout.Sidebar
	ebitenutil.DrawRect(screen, float64(sidebar.Min.X), float64(sidebar.Min.Y), float64(sidebar.Dx()), float64(sidebar.Dy()), color.RGBA{R: 24, G: 26, B: 32, A: 255})
	vector.StrokeRect(screen, float32(sidebar.Min.X), float32(sidebar.Min.Y), float32(sidebar.Dx()), float32(sidebar.Dy()), 2, color.RGBA{R: 76, G: 86, B: 103, A: 255}, false)

	game.drawSidebar(screen)
	game.drawAlgorithmMenuOverlay(screen)
	game.drawResampleMenuOverlay(screen)
}

func (game *Game) drawSidebar(screen *ebiten.Image) {
	sidebar := game.layout.Sidebar
	ebitenutil.DebugPrintAt(screen, "Dither Explorer", sidebar.Min.X+16, sidebar.Min.Y+12)

	captureRect := game.captureButtonRect()
	buttonColor := color.RGBA{R: 57, G: 82, B: 130, A: 255}
	buttonLabel := "Start Capture"
	if game.capture.IsRunning() {
		buttonColor = color.RGBA{R: 120, G: 61, B: 61, A: 255}
		buttonLabel = "Stop Capture"
	}
	ebitenutil.DrawRect(screen, float64(captureRect.Min.X), float64(captureRect.Min.Y), float64(captureRect.Dx()), float64(captureRect.Dy()), buttonColor)
	vector.StrokeRect(screen, float32(captureRect.Min.X), float32(captureRect.Min.Y), float32(captureRect.Dx()), float32(captureRect.Dy()), 1, color.RGBA{R: 180, G: 190, B: 210, A: 255}, false)
	ebitenutil.DebugPrintAt(screen, buttonLabel, captureRect.Min.X+10, captureRect.Min.Y+10)

	status := fmt.Sprintf("Status: %s", game.capture.Status())
	ebitenutil.DebugPrintAt(screen, status, sidebar.Min.X+16, captureRect.Max.Y+12)

	algorithmRect := game.algorithmButtonRect()
	ebitenutil.DrawRect(screen, float64(algorithmRect.Min.X), float64(algorithmRect.Min.Y), float64(algorithmRect.Dx()), float64(algorithmRect.Dy()), color.RGBA{R: 43, G: 49, B: 59, A: 255})
	vector.StrokeRect(screen, float32(algorithmRect.Min.X), float32(algorithmRect.Min.Y), float32(algorithmRect.Dx()), float32(algorithmRect.Dy()), 1, color.RGBA{R: 130, G: 140, B: 156, A: 255}, false)
	algorithmIndicator := "▼"
	if game.algorithmMenuOpen {
		algorithmIndicator = "▲"
	}
	ebitenutil.DebugPrintAt(screen, fmt.Sprintf("Algorithm: %s %s", game.algorithm, algorithmIndicator), algorithmRect.Min.X+10, algorithmRect.Min.Y+10)

	resampleRect := game.resampleButtonRect()
	ebitenutil.DrawRect(screen, float64(resampleRect.Min.X), float64(resampleRect.Min.Y), float64(resampleRect.Dx()), float64(resampleRect.Dy()), color.RGBA{R: 43, G: 49, B: 59, A: 255})
	vector.StrokeRect(screen, float32(resampleRect.Min.X), float32(resampleRect.Min.Y), float32(resampleRect.Dx()), float32(resampleRect.Dy()), 1, color.RGBA{R: 130, G: 140, B: 156, A: 255}, false)
	resampleIndicator := "▼"
	if game.resampleMenuOpen {
		resampleIndicator = "▲"
	}
	ebitenutil.DebugPrintAt(screen, fmt.Sprintf("Resample: %s %s", game.params.Resample, resampleIndicator), resampleRect.Min.X+10, resampleRect.Min.Y+10)

	ebitenutil.DebugPrintAt(screen, fmt.Sprintf("Pre-scale: %.2fx", game.params.Scale), sidebar.Min.X+16, resampleRect.Max.Y+16)
	game.drawSlider(screen, game.scaleSliderRect(), (game.params.Scale-0.10)/0.90, color.RGBA{R: 104, G: 180, B: 148, A: 255})

	ebitenutil.DebugPrintAt(screen, fmt.Sprintf("Output levels: %d", game.params.Levels), sidebar.Min.X+16, game.scaleSliderRect().Max.Y+16)
	game.drawSlider(screen, game.levelsSliderRect(), levelsToNormalized(game.params.Levels), color.RGBA{R: 188, G: 128, B: 214, A: 255})

	ebitenutil.DebugPrintAt(screen, fmt.Sprintf("Threshold: %.2f", game.params.Threshold), sidebar.Min.X+16, game.levelsSliderRect().Max.Y+16)
	game.drawSlider(screen, game.thresholdSliderRect(), game.params.Threshold, color.RGBA{R: 82, G: 150, B: 243, A: 255})

	if game.algorithm == dither.AlgorithmFloyd {
		diffusionRect := game.diffusionSliderRect()
		ebitenutil.DebugPrintAt(screen, fmt.Sprintf("Error Diffusion: %.2f", game.params.Diffusion), sidebar.Min.X+16, diffusionRect.Min.Y-16)
		game.drawSlider(screen, diffusionRect, game.params.Diffusion, color.RGBA{R: 220, G: 166, B: 64, A: 255})
	}
}

func (game *Game) drawSlider(screen *ebiten.Image, rect image.Rectangle, value float64, fill color.Color) {
	if value < 0 {
		value = 0
	}
	if value > 1 {
		value = 1
	}

	ebitenutil.DrawRect(screen, float64(rect.Min.X), float64(rect.Min.Y), float64(rect.Dx()), float64(rect.Dy()), color.RGBA{R: 53, G: 59, B: 71, A: 255})
	ebitenutil.DrawRect(screen, float64(rect.Min.X), float64(rect.Min.Y), float64(rect.Dx())*value, float64(rect.Dy()), fill)
	vector.StrokeRect(screen, float32(rect.Min.X), float32(rect.Min.Y), float32(rect.Dx()), float32(rect.Dy()), 1, color.RGBA{R: 140, G: 150, B: 167, A: 255}, false)

	knobX := rect.Min.X + int(float64(rect.Dx())*value)
	if knobX < rect.Min.X {
		knobX = rect.Min.X
	}
	if knobX > rect.Max.X {
		knobX = rect.Max.X
	}
	ebitenutil.DrawRect(screen, float64(knobX-4), float64(rect.Min.Y-3), 8, float64(rect.Dy()+6), color.RGBA{R: 240, G: 245, B: 255, A: 255})
}

func (game *Game) updateSlider(mouseX, mouseY int, mousePressed bool, sliderRect image.Rectangle, minValue, maxValue float64, target *float64) bool {
	if game.anyMenuOpen() {
		return false
	}
	if !mousePressed || !pointInRect(mouseX, mouseY, sliderRect) {
		return false
	}
	normalized := float64(mouseX-sliderRect.Min.X) / float64(sliderRect.Dx())
	if normalized < 0 {
		normalized = 0
	}
	if normalized > 1 {
		normalized = 1
	}
	newValue := minValue + normalized*(maxValue-minValue)
	if abs(*target-newValue) > 0.0001 {
		*target = newValue
		return true
	}
	return false
}

func (game *Game) updateScaleSlider(mouseX, mouseY int, mousePressed bool) bool {
	rect := game.scaleSliderRect()
	if game.anyMenuOpen() || !mousePressed || !pointInRect(mouseX, mouseY, rect) {
		return false
	}
	normalized := float64(mouseX-rect.Min.X) / float64(rect.Dx())
	if normalized < 0 {
		normalized = 0
	}
	if normalized > 1 {
		normalized = 1
	}
	scale := 0.10 + normalized*0.90
	if abs(game.params.Scale-scale) > 0.001 {
		game.params.Scale = scale
		return true
	}
	return false
}

func (game *Game) updateLevelsSlider(mouseX, mouseY int, mousePressed bool) bool {
	rect := game.levelsSliderRect()
	if game.anyMenuOpen() || !mousePressed || !pointInRect(mouseX, mouseY, rect) {
		return false
	}
	normalized := float64(mouseX-rect.Min.X) / float64(rect.Dx())
	if normalized < 0 {
		normalized = 0
	}
	if normalized > 1 {
		normalized = 1
	}
	levels := normalizedToLevels(normalized)
	if game.params.Levels != levels {
		game.params.Levels = levels
		return true
	}
	return false
}

func (game *Game) captureButtonRect() image.Rectangle {
	sidebar := game.layout.Sidebar
	return image.Rect(sidebar.Min.X+16, sidebar.Min.Y+42, sidebar.Max.X-16, sidebar.Min.Y+74)
}

func (game *Game) algorithmButtonRect() image.Rectangle {
	sidebar := game.layout.Sidebar
	return image.Rect(sidebar.Min.X+16, sidebar.Min.Y+132, sidebar.Max.X-16, sidebar.Min.Y+172)
}

func (game *Game) algorithmOptionRect(index int) image.Rectangle {
	buttonRect := game.algorithmButtonRect()
	optionHeight := buttonRect.Dy()
	minY := buttonRect.Max.Y + index*optionHeight
	maxY := minY + optionHeight
	return image.Rect(buttonRect.Min.X, minY, buttonRect.Max.X, maxY)
}

func (game *Game) algorithmMenuSelection(mouseX, mouseY int) (dither.Algorithm, bool) {
	for index, algorithm := range game.algorithms {
		if pointInRect(mouseX, mouseY, game.algorithmOptionRect(index)) {
			return algorithm, true
		}
	}
	return "", false
}

func (game *Game) drawAlgorithmMenuOverlay(screen *ebiten.Image) {
	if !game.algorithmMenuOpen {
		return
	}

	for index, algorithm := range game.algorithms {
		optionRect := game.algorithmOptionRect(index)
		backgroundColor := color.RGBA{R: 43, G: 49, B: 59, A: 255}
		if algorithm == game.algorithm {
			backgroundColor = color.RGBA{R: 63, G: 84, B: 120, A: 255}
		}
		ebitenutil.DrawRect(screen, float64(optionRect.Min.X), float64(optionRect.Min.Y), float64(optionRect.Dx()), float64(optionRect.Dy()), backgroundColor)
		vector.StrokeRect(screen, float32(optionRect.Min.X), float32(optionRect.Min.Y), float32(optionRect.Dx()), float32(optionRect.Dy()), 1, color.RGBA{R: 130, G: 140, B: 156, A: 255}, false)
		ebitenutil.DebugPrintAt(screen, string(algorithm), optionRect.Min.X+10, optionRect.Min.Y+10)
	}
}

func (game *Game) resampleButtonRect() image.Rectangle {
	sidebar := game.layout.Sidebar
	return image.Rect(sidebar.Min.X+16, sidebar.Min.Y+182, sidebar.Max.X-16, sidebar.Min.Y+222)
}

func (game *Game) resampleOptionRect(index int) image.Rectangle {
	buttonRect := game.resampleButtonRect()
	optionHeight := buttonRect.Dy()
	minY := buttonRect.Max.Y + index*optionHeight
	maxY := minY + optionHeight
	return image.Rect(buttonRect.Min.X, minY, buttonRect.Max.X, maxY)
}

func (game *Game) resampleMenuSelection(mouseX, mouseY int) (dither.ResampleAlgorithm, bool) {
	for index, resample := range game.resamples {
		if pointInRect(mouseX, mouseY, game.resampleOptionRect(index)) {
			return resample, true
		}
	}
	return "", false
}

func (game *Game) drawResampleMenuOverlay(screen *ebiten.Image) {
	if !game.resampleMenuOpen {
		return
	}

	for index, resample := range game.resamples {
		optionRect := game.resampleOptionRect(index)
		backgroundColor := color.RGBA{R: 43, G: 49, B: 59, A: 255}
		if resample == game.params.Resample {
			backgroundColor = color.RGBA{R: 63, G: 84, B: 120, A: 255}
		}
		ebitenutil.DrawRect(screen, float64(optionRect.Min.X), float64(optionRect.Min.Y), float64(optionRect.Dx()), float64(optionRect.Dy()), backgroundColor)
		vector.StrokeRect(screen, float32(optionRect.Min.X), float32(optionRect.Min.Y), float32(optionRect.Dx()), float32(optionRect.Dy()), 1, color.RGBA{R: 130, G: 140, B: 156, A: 255}, false)
		ebitenutil.DebugPrintAt(screen, string(resample), optionRect.Min.X+10, optionRect.Min.Y+10)
	}
}

func (game *Game) scaleSliderRect() image.Rectangle {
	sidebar := game.layout.Sidebar
	return image.Rect(sidebar.Min.X+16, sidebar.Min.Y+258, sidebar.Max.X-16, sidebar.Min.Y+278)
}

func (game *Game) levelsSliderRect() image.Rectangle {
	sidebar := game.layout.Sidebar
	return image.Rect(sidebar.Min.X+16, sidebar.Min.Y+318, sidebar.Max.X-16, sidebar.Min.Y+338)
}

func (game *Game) thresholdSliderRect() image.Rectangle {
	sidebar := game.layout.Sidebar
	return image.Rect(sidebar.Min.X+16, sidebar.Min.Y+378, sidebar.Max.X-16, sidebar.Min.Y+398)
}

func (game *Game) diffusionSliderRect() image.Rectangle {
	sidebar := game.layout.Sidebar
	return image.Rect(sidebar.Min.X+16, sidebar.Min.Y+438, sidebar.Max.X-16, sidebar.Min.Y+458)
}

func (game *Game) anyMenuOpen() bool {
	return game.algorithmMenuOpen || game.resampleMenuOpen
}

func normalizedToLevels(normalized float64) int {
	step := int(math.Round(normalized * 3.0))
	if step < 0 {
		step = 0
	}
	if step > 3 {
		step = 3
	}
	return 1 << (step + 1)
}

func levelsToNormalized(levels int) float64 {
	switch levels {
	case 2:
		return 0.0
	case 4:
		return 1.0 / 3.0
	case 8:
		return 2.0 / 3.0
	case 16:
		return 1.0
	default:
		return 0.0
	}
}

func (game *Game) Layout(outsideWidth, outsideHeight int) (int, int) {
	return outsideWidth, outsideHeight
}

func pointInRect(x, y int, rect image.Rectangle) bool {
	return x >= rect.Min.X && x <= rect.Max.X && y >= rect.Min.Y && y <= rect.Max.Y
}

func drawImageIntoRect(target *ebiten.Image, source *ebiten.Image, rect image.Rectangle) {
	if source == nil || rect.Dx() <= 0 || rect.Dy() <= 0 {
		return
	}

	sourceBounds := source.Bounds()
	scaleX := float64(rect.Dx()) / float64(sourceBounds.Dx())
	scaleY := float64(rect.Dy()) / float64(sourceBounds.Dy())

	op := &ebiten.DrawImageOptions{}
	op.GeoM.Scale(scaleX, scaleY)
	op.GeoM.Translate(float64(rect.Min.X), float64(rect.Min.Y))
	target.DrawImage(source, op)
}

func abs(value float64) float64 {
	if value < 0 {
		return -value
	}
	return value
}
