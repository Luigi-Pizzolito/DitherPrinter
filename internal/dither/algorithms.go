package dither

import (
	"image"
	"image/color"
	"math"

	"github.com/nfnt/resize"
)

type Algorithm string

const (
	AlgorithmThreshold Algorithm = "Threshold"
	AlgorithmFloyd     Algorithm = "Floyd-Steinberg"
)

type Params struct {
	Threshold float64
	Diffusion float64
	Levels    int
	Scale     float64
	Resample  ResampleAlgorithm
}

type ResampleAlgorithm string

const (
	ResampleNearest  ResampleAlgorithm = "Nearest"
	ResampleBilinear ResampleAlgorithm = "Bilinear"
	ResampleBicubic  ResampleAlgorithm = "Bicubic"
	ResampleLanczos  ResampleAlgorithm = "Lanczos"
)

func Process(source image.Image, algorithm Algorithm, params Params) (stage1, stage2, stage3 *image.RGBA) {
	prepared := preprocess(source, params.Scale, params.Resample)
	levels := normalizeLevels(params.Levels)

	if params.Scale <= 0 || params.Scale > 1 {
		params.Scale = 1
	}

	gray, width, height := toGray(source)
	if prepared != nil {
		gray, width, height = toGray(prepared)
	}
	stage1 = grayscalePreview(gray, width, height)

	switch algorithm {
	case AlgorithmFloyd:
		intermediate, result := floydSteinberg(gray, width, height, params.Threshold, params.Diffusion, levels)
		stage2 = grayscalePreview(intermediate, width, height)
		stage3 = grayscalePreview(result, width, height)
	default:
		intermediate, result := threshold(gray, width, height, params.Threshold, levels)
		stage2 = grayscalePreview(intermediate, width, height)
		stage3 = grayscalePreview(result, width, height)
	}

	return stage1, stage2, stage3
}

func preprocess(source image.Image, scale float64, resample ResampleAlgorithm) image.Image {
	if source == nil {
		return nil
	}
	if scale <= 0 || scale >= 1 {
		return source
	}

	bounds := source.Bounds()
	width := int(math.Round(float64(bounds.Dx()) * scale))
	height := int(math.Round(float64(bounds.Dy()) * scale))
	if width < 1 {
		width = 1
	}
	if height < 1 {
		height = 1
	}

	return resize.Resize(uint(width), uint(height), source, resampleInterpolation(resample))
}

func toGray(source image.Image) ([]float64, int, int) {
	bounds := source.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()
	gray := make([]float64, width*height)

	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			r, g, b, _ := source.At(bounds.Min.X+x, bounds.Min.Y+y).RGBA()
			rf := float64(r>>8) / 255.0
			gf := float64(g>>8) / 255.0
			bf := float64(b>>8) / 255.0
			gray[y*width+x] = 0.299*rf + 0.587*gf + 0.114*bf
		}
	}
	return gray, width, height
}

func threshold(gray []float64, width, height int, thresholdValue float64, levels int) ([]float64, []float64) {
	if thresholdValue < 0 {
		thresholdValue = 0
	}
	if thresholdValue > 1 {
		thresholdValue = 1
	}

	intermediate := make([]float64, len(gray))
	result := make([]float64, len(gray))
	for index, value := range gray {
		intermediate[index] = value
		result[index] = quantize(value, levels, thresholdValue)
	}
	return intermediate, result
}

func floydSteinberg(gray []float64, width, height int, thresholdValue, diffusion float64, levels int) ([]float64, []float64) {
	if thresholdValue < 0 {
		thresholdValue = 0
	}
	if thresholdValue > 1 {
		thresholdValue = 1
	}
	if diffusion < 0 {
		diffusion = 0
	}
	if diffusion > 1 {
		diffusion = 1
	}

	working := make([]float64, len(gray))
	copy(working, gray)
	result := make([]float64, len(gray))

	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			index := y*width + x
			old := clamp01(working[index])
			newValue := quantize(old, levels, thresholdValue)
			result[index] = newValue
			errorValue := (old - newValue) * diffusion

			if x+1 < width {
				working[index+1] += errorValue * (7.0 / 16.0)
			}
			if y+1 < height {
				down := index + width
				if x > 0 {
					working[down-1] += errorValue * (3.0 / 16.0)
				}
				working[down] += errorValue * (5.0 / 16.0)
				if x+1 < width {
					working[down+1] += errorValue * (1.0 / 16.0)
				}
			}
		}
	}

	for index := range working {
		working[index] = clamp01(working[index])
	}

	return working, result
}

func grayscalePreview(values []float64, width, height int) *image.RGBA {
	output := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			value := uint8(clamp01(values[y*width+x]) * 255)
			output.SetRGBA(x, y, color.RGBA{R: value, G: value, B: value, A: 255})
		}
	}
	return output
}

func clamp01(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

func quantize(value float64, levels int, threshold float64) float64 {
	value = clamp01(value)
	levels = normalizeLevels(levels)
	if levels <= 2 {
		if value >= clamp01(threshold) {
			return 1
		}
		return 0
	}
	steps := levels - 1
	index := int(math.Round(value * float64(steps)))
	if index < 0 {
		index = 0
	}
	if index > steps {
		index = steps
	}
	return float64(index) / float64(steps)
}

func normalizeLevels(levels int) int {
	if levels < 2 {
		return 2
	}
	if levels > 16 {
		return 16
	}
	return levels
}

func resampleInterpolation(resample ResampleAlgorithm) resize.InterpolationFunction {
	switch resample {
	case ResampleNearest:
		return resize.NearestNeighbor
	case ResampleBilinear:
		return resize.Bilinear
	case ResampleBicubic:
		return resize.Bicubic
	default:
		return resize.Lanczos3
	}
}
