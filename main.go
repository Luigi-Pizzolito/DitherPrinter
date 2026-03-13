package main

import (
	"log"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/luigipizzolito/ditherprinter/internal/app"
)

func main() {
	game := app.NewGame()

	ebiten.SetWindowSize(1200, 1400)
	ebiten.SetWindowTitle("Dither Explorer")
	ebiten.SetWindowResizingMode(ebiten.WindowResizingModeEnabled)

	if err := ebiten.RunGame(game); err != nil {
		log.Fatal(err)
	}
}
