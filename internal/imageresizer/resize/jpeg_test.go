package resize

import (
	"bytes"
	"context"
	"encoding/binary"
	"image"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"

	"github.com/CyberCute-Studio/GameDevTools/internal/imageresizer/decode"
)

// mirroredJPEG returns JPEG data with an EXIF segment marking it mirrored
// left to right inserted directly after its start-of-image marker.
func mirroredJPEG(data []byte) []byte {
	tiff := []byte{'M', 'M', 0, 42, 0, 0, 0, 8, 0, 1}  // big-endian, IFD0 at 8 with one entry
	tiff = binary.BigEndian.AppendUint16(tiff, 0x0112) // orientation
	tiff = binary.BigEndian.AppendUint16(tiff, 3)      // SHORT
	tiff = binary.BigEndian.AppendUint32(tiff, 1)      // one value
	tiff = binary.BigEndian.AppendUint16(tiff, uint16(decode.OrientationMirrorHorizontal))
	tiff = append(tiff, 0, 0, 0, 0, 0, 0) // value padding, then no further IFD
	payload := append([]byte("Exif\x00\x00"), tiff...)
	segment := binary.BigEndian.AppendUint16([]byte{0xff, 0xe1}, uint16(len(payload)+2))
	out := append(append([]byte(nil), data[:2]...), segment...)
	out = append(out, payload...)
	return append(out, data[2:]...)
}

// decodeJPEG writes JPEG data to a file and decodes it with the decoder.
func decodeJPEG(t *testing.T, name string, data []byte) *decode.Image {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	decoded, err := decode.Decode(context.Background(), path)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return decoded
}

// TestJPEGPixelsMatchTheDecoder ties the resizer's YCbCr and CMYK
// conversion to the decoder's own: decoded upright, a JPEG arrives as YCbCr
// or CMYK pixels the resizer converts; marked mirrored, the same JPEG
// arrives as the decoder's *image.RGBA conversion. Once the mirror is
// undone, both must resize to the same pixels — at the source's own size,
// the conversion itself — so reorienting a picture does not change the
// colors the filter starts from.
func TestJPEGPixelsMatchTheDecoder(t *testing.T) {
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, noise(91, image.Rect(0, 0, 45, 31), false), &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	cmyk, err := os.ReadFile(filepath.Join("..", "decode", "testdata", "cmyk.jpeg"))
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{"ycbcr.jpg": encoded.Bytes(), "cmyk.jpg": cmyk} {
		upright := decodeJPEG(t, name, data)
		mirrored := decodeJPEG(t, "mirrored-"+name, mirroredJPEG(data))
		switch upright.Pixels.(type) {
		case *image.YCbCr, *image.CMYK:
		default:
			t.Fatalf("%s: upright pixels are %T, want YCbCr or CMYK", name, upright.Pixels)
		}
		reoriented, ok := mirrored.Pixels.(*image.RGBA)
		if mirrored.Orientation != decode.OrientationMirrorHorizontal || !ok {
			t.Fatalf("%s: mirrored copy decoded with orientation %d as %T", name, mirrored.Orientation, mirrored.Pixels)
		}
		bounds := reoriented.Bounds()
		converted := image.NewRGBA(bounds)
		for y := range bounds.Dy() {
			for x := range bounds.Dx() {
				converted.SetRGBA(x, y, reoriented.RGBAAt(bounds.Dx()-1-x, y))
			}
		}
		for _, target := range []Target{fit(bounds.Dx(), bounds.Dy()), fit(20, 20), stretch(97, 13), crop(30, 30)} {
			for _, o := range []order{orderHorizontalFirst, orderVerticalFirst} {
				if !sameImage(mustResample(t, upright.Pixels, target, o), mustResample(t, converted, target, o)) {
					t.Errorf("%s %+v order %d: the resizer's conversion differs from the decoder's", name, target, o)
				}
			}
		}
	}
}
