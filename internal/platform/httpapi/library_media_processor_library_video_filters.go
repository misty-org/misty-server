package api

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"math"
	"os"
	"strings"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func libraryVideoFilters(definition db.LibraryEditDefinition) []string {
	filters := []string{}
	if crop := definition.Crop; crop != nil {
		filters = append(filters, fmt.Sprintf("crop=iw*%s:ih*%s:iw*%s:ih*%s", formatMediaNumber(crop.Width), formatMediaNumber(crop.Height), formatMediaNumber(crop.X), formatMediaNumber(crop.Y)))
	}
	if definition.FlipHorizontal {
		filters = append(filters, "hflip")
	}
	if definition.FlipVertical {
		filters = append(filters, "vflip")
	}
	switch definition.Rotation {
	case 90:
		filters = append(filters, "transpose=clock")
	case 180:
		filters = append(filters, "transpose=clock", "transpose=clock")
	case 270:
		filters = append(filters, "transpose=cclock")
	}
	if definition.Straighten != 0 {
		filters = append(filters, "rotate="+formatMediaNumber(definition.Straighten)+"*PI/180:fillcolor=black")
	}
	brightness := math.Max(-1, math.Min(1, definition.Brightness-1+definition.Exposure*.125+definition.Brilliance*.05-definition.BlackPoint*.04))
	contrast := math.Max(0, math.Min(3, definition.Contrast*(1+definition.Highlights*.18-definition.Shadows*.08+definition.BlackPoint*.16)))
	saturation := math.Max(0, math.Min(3, definition.Saturation*(1+definition.Vibrance*.5)))
	gamma := math.Max(.1, math.Min(10, 1+definition.Shadows*.22-definition.Highlights*.12+definition.Brilliance*.08))
	if brightness != 0 || contrast != 1 || saturation != 1 || gamma != 1 {
		filters = append(filters, fmt.Sprintf("eq=brightness=%s:contrast=%s:saturation=%s:gamma=%s", formatMediaNumber(brightness), formatMediaNumber(contrast), formatMediaNumber(saturation), formatMediaNumber(gamma)))
	}
	if definition.Warmth != 0 || definition.Tint != 0 {
		filters = append(filters, fmt.Sprintf("colorbalance=rs=%s:gs=%s:bs=%s", formatMediaNumber(definition.Warmth*.12+definition.Tint*.04), formatMediaNumber(-definition.Tint*.08), formatMediaNumber(-definition.Warmth*.12+definition.Tint*.04)))
	}
	if definition.NoiseReduction > 0 {
		strength := 1 + definition.NoiseReduction*5
		filters = append(filters, fmt.Sprintf("hqdn3d=%s:%s:%s:%s", formatMediaNumber(strength), formatMediaNumber(strength), formatMediaNumber(strength*1.5), formatMediaNumber(strength*1.5)))
	}
	if definition.Sharpness > 0 || definition.Definition > 0 {
		amount := math.Min(2, definition.Sharpness*.7+definition.Definition*.5)
		filters = append(filters, fmt.Sprintf("unsharp=5:5:%s:5:5:0", formatMediaNumber(amount)))
	}
	if definition.Vignette > 0 {
		filters = append(filters, "vignette=PI/"+formatMediaNumber(16-12*definition.Vignette))
	}
	if definition.Grayscale > 0 {
		filters = append(filters, "hue=s="+formatMediaNumber(1-definition.Grayscale))
	}
	if definition.AutoEnhance {
		filters = append(filters, "eq=contrast=1.05:saturation=1.08:gamma=1.02")
	}
	switch definition.Filter {
	case "vivid":
		filters = append(filters, "eq=contrast=1.08:saturation=1.28")
	case "dramatic":
		filters = append(filters, "eq=contrast=1.25:saturation=.82:gamma=.92")
	case "warm":
		filters = append(filters, "colorbalance=rs=.08:bs=-.06")
	case "cool":
		filters = append(filters, "colorbalance=rs=-.05:bs=.09")
	case "mono":
		filters = append(filters, "hue=s=0")
	case "noir":
		filters = append(filters, "hue=s=0", "eq=contrast=1.35:brightness=-.04")
	}
	filters = append(filters, libraryMarkupFilters(definition.Markup)...)
	if definition.PlaybackSpeed > 0 && definition.PlaybackSpeed != 1 {
		filters = append(filters, "setpts=PTS/"+formatMediaNumber(definition.PlaybackSpeed))
	}
	return filters
}

func libraryMarkupFilters(elements []db.LibraryMarkupElement) []string {
	filters := []string{}
	for _, element := range elements {
		color := "0x" + strings.TrimPrefix(element.Color, "#") + "@" + formatMediaNumber(element.Opacity)
		switch element.Kind {
		case "stroke", "highlight":
			width := element.LineWidth
			if element.Kind == "highlight" {
				width *= 2.5
			}
			for _, point := range element.Points {
				filters = append(filters, fmt.Sprintf("drawbox=x=iw*%s:y=ih*%s:w=iw*%s:h=iw*%s:color=%s:t=fill", formatMediaNumber(math.Max(0, point.X-width/2)), formatMediaNumber(math.Max(0, point.Y-width/2)), formatMediaNumber(width), formatMediaNumber(width), color))
			}
		case "rectangle":
			line := element.LineWidth
			filters = append(filters,
				fmt.Sprintf("drawbox=x=iw*%s:y=ih*%s:w=iw*%s:h=iw*%s:color=%s:t=fill", formatMediaNumber(element.X), formatMediaNumber(element.Y), formatMediaNumber(element.Width), formatMediaNumber(line), color),
				fmt.Sprintf("drawbox=x=iw*%s:y=ih*%s:w=iw*%s:h=iw*%s:color=%s:t=fill", formatMediaNumber(element.X), formatMediaNumber(element.Y+element.Height-line), formatMediaNumber(element.Width), formatMediaNumber(line), color),
				fmt.Sprintf("drawbox=x=iw*%s:y=ih*%s:w=iw*%s:h=ih*%s:color=%s:t=fill", formatMediaNumber(element.X), formatMediaNumber(element.Y), formatMediaNumber(line), formatMediaNumber(element.Height), color),
				fmt.Sprintf("drawbox=x=iw*%s:y=ih*%s:w=iw*%s:h=ih*%s:color=%s:t=fill", formatMediaNumber(element.X+element.Width-line), formatMediaNumber(element.Y), formatMediaNumber(line), formatMediaNumber(element.Height), color),
			)
		case "text":
			filters = append(filters, libraryMarkupTextFilters(element, color)...)
		}
	}
	return filters
}

func applyLibraryCleanup(path string, elements []db.LibraryMarkupElement) error {
	regions := []db.LibraryMarkupElement{}
	for _, element := range elements {
		if element.Kind == "cleanup" {
			regions = append(regions, element)
		}
	}
	if len(regions) == 0 {
		return nil
	}
	input, err := os.Open(path)
	if err != nil {
		return err
	}
	decoded, err := jpeg.Decode(input)
	_ = input.Close()
	if err != nil {
		return fmt.Errorf("decode Library cleanup rendition: %w", err)
	}
	bounds := decoded.Bounds()
	output := image.NewRGBA(bounds)
	draw.Draw(output, bounds, decoded, bounds.Min, draw.Src)
	for _, region := range regions {
		x0 := max(bounds.Min.X+1, bounds.Min.X+int(math.Round(region.X*float64(bounds.Dx()))))
		y0 := max(bounds.Min.Y+1, bounds.Min.Y+int(math.Round(region.Y*float64(bounds.Dy()))))
		x1 := min(bounds.Max.X-1, x0+max(1, int(math.Round(region.Width*float64(bounds.Dx())))))
		y1 := min(bounds.Max.Y-1, y0+max(1, int(math.Round(region.Height*float64(bounds.Dy())))))
		if x1 <= x0 || y1 <= y0 {
			continue
		}
		for y := y0; y < y1; y++ {
			vertical := float64(y-y0) / float64(max(1, y1-y0-1))
			for x := x0; x < x1; x++ {
				horizontal := float64(x-x0) / float64(max(1, x1-x0-1))
				left, right := color.RGBAModel.Convert(output.At(x0-1, y)).(color.RGBA), color.RGBAModel.Convert(output.At(x1, y)).(color.RGBA)
				top, bottom := color.RGBAModel.Convert(output.At(x, y0-1)).(color.RGBA), color.RGBAModel.Convert(output.At(x, y1)).(color.RGBA)
				horizontalColor := blendLibraryColor(left, right, horizontal)
				verticalColor := blendLibraryColor(top, bottom, vertical)
				output.SetRGBA(x, y, blendLibraryColor(horizontalColor, verticalColor, .5))
			}
		}
	}
	temporary := path + ".cleanup"
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	encodeErr := jpeg.Encode(file, output, &jpeg.Options{Quality: 92})
	closeErr := file.Close()
	if encodeErr != nil {
		_ = os.Remove(temporary)
		return encodeErr
	}
	if closeErr != nil {
		_ = os.Remove(temporary)
		return closeErr
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return nil
}

func blendLibraryColor(left, right color.RGBA, amount float64) color.RGBA {
	amount = math.Max(0, math.Min(1, amount))
	blend := func(a, b uint8) uint8 { return uint8(math.Round(float64(a)*(1-amount) + float64(b)*amount)) }
	return color.RGBA{R: blend(left.R, right.R), G: blend(left.G, right.G), B: blend(left.B, right.B), A: 255}
}

func libraryMarkupTextFilters(element db.LibraryMarkupElement, color string) []string {
	filters := []string{}
	cell := math.Max(.003, element.LineWidth*.55)
	x := element.X
	for _, character := range strings.ToUpper(element.Text) {
		glyph, ok := libraryMarkupGlyphs[character]
		if !ok {
			glyph = libraryMarkupGlyphs['?']
		}
		for row, bits := range glyph {
			for column := 0; column < 5; column++ {
				if bits&(1<<(4-column)) == 0 {
					continue
				}
				filters = append(filters, fmt.Sprintf("drawbox=x=iw*%s:y=ih*%s:w=iw*%s:h=ih*%s:color=%s:t=fill", formatMediaNumber(x+float64(column)*cell), formatMediaNumber(element.Y+float64(row)*cell), formatMediaNumber(cell), formatMediaNumber(cell), color))
			}
		}
		x += cell * 6
		if x >= 1-cell*5 {
			break
		}
	}
	return filters
}

var libraryMarkupGlyphs = map[rune][7]uint8{
	' ': {},
	'A': {0x0e, 0x11, 0x11, 0x1f, 0x11, 0x11, 0x11}, 'B': {0x1e, 0x11, 0x11, 0x1e, 0x11, 0x11, 0x1e},
	'C': {0x0e, 0x11, 0x10, 0x10, 0x10, 0x11, 0x0e}, 'D': {0x1e, 0x11, 0x11, 0x11, 0x11, 0x11, 0x1e},
	'E': {0x1f, 0x10, 0x10, 0x1e, 0x10, 0x10, 0x1f}, 'F': {0x1f, 0x10, 0x10, 0x1e, 0x10, 0x10, 0x10},
	'G': {0x0e, 0x11, 0x10, 0x17, 0x11, 0x11, 0x0e}, 'H': {0x11, 0x11, 0x11, 0x1f, 0x11, 0x11, 0x11},
	'I': {0x1f, 0x04, 0x04, 0x04, 0x04, 0x04, 0x1f}, 'J': {0x01, 0x01, 0x01, 0x01, 0x11, 0x11, 0x0e},
	'K': {0x11, 0x12, 0x14, 0x18, 0x14, 0x12, 0x11}, 'L': {0x10, 0x10, 0x10, 0x10, 0x10, 0x10, 0x1f},
	'M': {0x11, 0x1b, 0x15, 0x15, 0x11, 0x11, 0x11}, 'N': {0x11, 0x19, 0x15, 0x13, 0x11, 0x11, 0x11},
	'O': {0x0e, 0x11, 0x11, 0x11, 0x11, 0x11, 0x0e}, 'P': {0x1e, 0x11, 0x11, 0x1e, 0x10, 0x10, 0x10},
	'Q': {0x0e, 0x11, 0x11, 0x11, 0x15, 0x12, 0x0d}, 'R': {0x1e, 0x11, 0x11, 0x1e, 0x14, 0x12, 0x11},
	'S': {0x0f, 0x10, 0x10, 0x0e, 0x01, 0x01, 0x1e}, 'T': {0x1f, 0x04, 0x04, 0x04, 0x04, 0x04, 0x04},
	'U': {0x11, 0x11, 0x11, 0x11, 0x11, 0x11, 0x0e}, 'V': {0x11, 0x11, 0x11, 0x11, 0x11, 0x0a, 0x04},
	'W': {0x11, 0x11, 0x11, 0x15, 0x15, 0x15, 0x0a}, 'X': {0x11, 0x11, 0x0a, 0x04, 0x0a, 0x11, 0x11},
	'Y': {0x11, 0x11, 0x0a, 0x04, 0x04, 0x04, 0x04}, 'Z': {0x1f, 0x01, 0x02, 0x04, 0x08, 0x10, 0x1f},
	'0': {0x0e, 0x11, 0x13, 0x15, 0x19, 0x11, 0x0e}, '1': {0x04, 0x0c, 0x04, 0x04, 0x04, 0x04, 0x0e},
	'2': {0x0e, 0x11, 0x01, 0x02, 0x04, 0x08, 0x1f}, '3': {0x1e, 0x01, 0x01, 0x0e, 0x01, 0x01, 0x1e},
	'4': {0x02, 0x06, 0x0a, 0x12, 0x1f, 0x02, 0x02}, '5': {0x1f, 0x10, 0x10, 0x1e, 0x01, 0x01, 0x1e},
	'6': {0x0e, 0x10, 0x10, 0x1e, 0x11, 0x11, 0x0e}, '7': {0x1f, 0x01, 0x02, 0x04, 0x08, 0x08, 0x08},
	'8': {0x0e, 0x11, 0x11, 0x0e, 0x11, 0x11, 0x0e}, '9': {0x0e, 0x11, 0x11, 0x0f, 0x01, 0x01, 0x0e},
	'.': {0, 0, 0, 0, 0, 0, 0x04}, ',': {0, 0, 0, 0, 0, 0x04, 0x08}, '!': {0x04, 0x04, 0x04, 0x04, 0, 0, 0x04},
	'?': {0x0e, 0x11, 0x01, 0x02, 0x04, 0, 0x04}, '-': {0, 0, 0, 0x0e, 0, 0, 0}, '(': {0x02, 0x04, 0x08, 0x08, 0x08, 0x04, 0x02},
	')': {0x08, 0x04, 0x02, 0x02, 0x02, 0x04, 0x08},
}

func libraryMediaInputExtension(mimeType string) string {
	switch mimeType {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "video/quicktime":
		return ".mov"
	case "video/webm":
		return ".webm"
	default:
		return ".mp4"
	}
}
