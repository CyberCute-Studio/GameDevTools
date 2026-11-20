package imageresizer

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// The Image Resizer acceptance fixtures (RSZ-09). Every fixture is generated
// in code from fixed parameters, so each run of the suite writes the same
// bytes on any machine and no binary image is checked in: the formats, EXIF
// orientations, alpha, and collision layouts below are the ones the
// scenarios of PRD section 15.2 name, and each scenario builds its folders
// from them as it needs.

var (
	fixtureRed     = color.NRGBA{R: 0xff, A: 0xff}
	fixtureGreen   = color.NRGBA{G: 0xff, A: 0xff}
	fixtureBlue    = color.NRGBA{B: 0xff, A: 0xff}
	fixtureWhite   = color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
	fixtureBlack   = color.NRGBA{A: 0xff}
	fixtureMagenta = color.NRGBA{R: 0xff, B: 0xff, A: 0xff}
)

// fixtureFill is a width × height picture of one color.
func fixtureFill(width, height int, c color.NRGBA) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	draw.Draw(img, img.Bounds(), image.NewUniform(c), image.Point{}, draw.Src)
	return img
}

// fixtureQuadrants is a picture whose quadrants are red (top left), green
// (top right), blue (bottom left), and white (bottom right), so every mirror
// and rotation of it can be told apart.
func fixtureQuadrants(width, height int) *image.NRGBA {
	img := fixtureFill(width, height, fixtureWhite)
	halfWidth, halfHeight := width/2, height/2
	draw.Draw(img, image.Rect(0, 0, halfWidth, halfHeight), image.NewUniform(fixtureRed), image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(halfWidth, 0, width, halfHeight), image.NewUniform(fixtureGreen), image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(0, halfHeight, halfWidth, height), image.NewUniform(fixtureBlue), image.Point{}, draw.Src)
	return img
}

// fixtureCropMarks is the 1000 × 500 source of AC-R01, AC-R02, and AC-R04.
// Its middle half, x 250 to 750, is green, with red to the left and blue to
// the right, so a 512 × 512 Crop to Fill, which scales it to 1024 × 512 and
// keeps exactly that middle half when centered, holds no red or blue. Black
// stripes ten pixels wide cross the green at x 300 and 495 and at y 50 and
// 245, so that, at the 1.024 scale both axes share when the aspect ratio is
// kept, the stripes at x 300 and y 50 land on output column and row 56, and
// those at x 495 and y 245 on column and row 256.
func fixtureCropMarks() *image.NRGBA {
	img := fixtureFill(1000, 500, fixtureGreen)
	draw.Draw(img, image.Rect(0, 0, 250, 500), image.NewUniform(fixtureRed), image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(750, 0, 1000, 500), image.NewUniform(fixtureBlue), image.Point{}, draw.Src)
	for _, x := range []int{300, 495} {
		draw.Draw(img, image.Rect(x, 0, x+10, 500), image.NewUniform(fixtureBlack), image.Point{}, draw.Src)
	}
	for _, y := range []int{50, 245} {
		draw.Draw(img, image.Rect(250, y, 750, y+10), image.NewUniform(fixtureBlack), image.Point{}, draw.Src)
	}
	return img
}

// fixtureTranslucent is the AC-R11 source: a 90 × 30 PNG whose left third
// is fully transparent green, whose middle third is red at half opacity
// (alpha 128), and whose right third is opaque blue.
func fixtureTranslucent() *image.NRGBA {
	img := fixtureFill(90, 30, color.NRGBA{G: 0xff})
	draw.Draw(img, image.Rect(30, 0, 60, 30), image.NewUniform(color.NRGBA{R: 0xff, A: 0x80}), image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(60, 0, 90, 30), image.NewUniform(fixtureBlue), image.Point{}, draw.Src)
	return img
}

// fixtureStored is the picture a camera stores for an upright picture under
// an EXIF orientation: the pixel it stores at (x, y) is the one the
// orientation then shows at the returned position, by the orientation's
// definition in the EXIF standard (2: mirrored left to right; 3: turned
// 180°; 4: mirrored top to bottom; 5: mirrored across the main diagonal; 6:
// to be turned 90° clockwise; 7: mirrored across the anti-diagonal; 8: to be
// turned 90° counterclockwise). It is written independently of the decoder,
// which AC-R12 checks against it.
func fixtureStored(upright *image.NRGBA, orientation uint16) *image.NRGBA {
	width, height := upright.Bounds().Dx(), upright.Bounds().Dy()
	storedWidth, storedHeight := width, height
	if orientation >= 5 {
		storedWidth, storedHeight = height, width
	}
	stored := image.NewNRGBA(image.Rect(0, 0, storedWidth, storedHeight))
	for y := range storedHeight {
		for x := range storedWidth {
			shownX, shownY := x, y
			switch orientation {
			case 2:
				shownX = storedWidth - 1 - x
			case 3:
				shownX, shownY = storedWidth-1-x, storedHeight-1-y
			case 4:
				shownY = storedHeight - 1 - y
			case 5:
				shownX, shownY = y, x
			case 6:
				shownX, shownY = storedHeight-1-y, x
			case 7:
				shownX, shownY = storedHeight-1-y, storedWidth-1-x
			case 8:
				shownX, shownY = y, storedWidth-1-x
			}
			stored.SetNRGBA(x, y, upright.NRGBAAt(shownX, shownY))
		}
	}
	return stored
}

// withOrientation inserts an EXIF APP1 segment carrying the orientation
// directly after the JPEG's SOI marker.
func withOrientation(content []byte, orientation uint16) []byte {
	tiff := []byte{'M', 'M', 0, 42, 0, 0, 0, 8, 0, 1, 0x01, 0x12, 0, 3, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0}
	binary.BigEndian.PutUint16(tiff[18:], orientation)
	payload := append([]byte("Exif\x00\x00"), tiff...)
	segment := []byte{0xff, 0xe1, 0, 0}
	binary.BigEndian.PutUint16(segment[2:], uint16(len(payload)+2))
	result := append([]byte(nil), content[:2]...)
	result = append(result, segment...)
	result = append(result, payload...)
	return append(result, content[2:]...)
}

func fixturePNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, img); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

// fixtureJPEG encodes at quality 95, so solid areas decode within a few
// steps of their color.
func fixtureJPEG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buffer bytes.Buffer
	if err := jpeg.Encode(&buffer, img, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

// fixtureDamagedPNG is a file that starts with the PNG signature, so it is
// identified as a PNG, but holds no valid PNG data after it.
var fixtureDamagedPNG = []byte("\x89PNG\r\n\x1a\nthe rest of this file is damaged")

// fixtureGIF is the header of a GIF image, a format the resizer does not
// read.
var fixtureGIF = []byte("GIF89a\x01\x00\x01\x00\x00\x00\x00;")

// writeFixture writes a fixture into directory, creating the directory, and
// returns its path.
func writeFixture(t *testing.T, directory, name string, content []byte) string {
	t.Helper()
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
