package decode

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"image"
	"image/color"
	"image/draw"
	"testing"
)

var (
	red   = color.RGBA{R: 0xff, A: 0xff}
	green = color.RGBA{G: 0xff, A: 0xff}
	blue  = color.RGBA{B: 0xff, A: 0xff}
	white = color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
)

// quadrants builds a width × height picture whose four quadrants are red
// (top left), green (top right), blue (bottom left), and white (bottom
// right), large enough that JPEG compression keeps each quadrant's center
// close to its color.
func quadrants(width, height int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	halfWidth, halfHeight := width/2, height/2
	draw.Draw(img, image.Rect(0, 0, halfWidth, halfHeight), image.NewUniform(red), image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(halfWidth, 0, width, halfHeight), image.NewUniform(green), image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(0, halfHeight, halfWidth, height), image.NewUniform(blue), image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(halfWidth, halfHeight, width, height), image.NewUniform(white), image.Point{}, draw.Src)
	return img
}

// exifSegment builds an APP1 EXIF segment whose IFD0 holds one orientation
// entry of the given TIFF type, in the given byte order.
func exifSegment(order binary.ByteOrder, orientation uint32, tiffType uint16) []byte {
	var tiff bytes.Buffer
	if order == binary.LittleEndian {
		tiff.WriteString("II")
	} else {
		tiff.WriteString("MM")
	}
	binary.Write(&tiff, order, uint16(tiffMagic))
	binary.Write(&tiff, order, uint32(tiffHeaderSize))
	binary.Write(&tiff, order, uint16(1))
	binary.Write(&tiff, order, uint16(tagOrientation))
	binary.Write(&tiff, order, tiffType)
	binary.Write(&tiff, order, uint32(1))
	var value [4]byte
	if tiffType == tiffTypeShort {
		order.PutUint16(value[:2], uint16(orientation))
	} else {
		order.PutUint32(value[:], orientation)
	}
	tiff.Write(value[:])
	binary.Write(&tiff, order, uint32(0)) // no further IFDs
	return app1(append(append([]byte(nil), exifHeader...), tiff.Bytes()...))
}

func app1(payload []byte) []byte {
	segment := []byte{0xff, markerAPP1, 0, 0}
	binary.BigEndian.PutUint16(segment[2:], uint16(len(payload)+2))
	return append(segment, payload...)
}

// withSegments inserts marker segments directly after a JPEG's SOI marker.
func withSegments(jpegData []byte, segments ...[]byte) []byte {
	result := append([]byte(nil), jpegData[:2]...)
	for _, segment := range segments {
		result = append(result, segment...)
	}
	return append(result, jpegData[2:]...)
}

// TestDecodeAppliesEveryEXIFOrientation pins RSZ-018 and AC-R12 for all
// eight standard orientations: the reported dimensions are the upright
// ones, and each quadrant lands where the EXIF definition puts it. The
// expected corners were derived by hand from where each orientation places
// the stored 0th row and 0th column, independently of the transform code.
func TestDecodeAppliesEveryEXIFOrientation(t *testing.T) {
	const storedWidth, storedHeight = 96, 64
	source := encodeJPEG(t, quadrants(storedWidth, storedHeight))
	// Each row lists the upright top-left, top-right, bottom-left, and
	// bottom-right colors.
	cases := []struct {
		orientation Orientation
		corners     [4]color.RGBA
	}{
		{OrientationNormal, [4]color.RGBA{red, green, blue, white}},
		{OrientationMirrorHorizontal, [4]color.RGBA{green, red, white, blue}},
		{OrientationRotate180, [4]color.RGBA{white, blue, green, red}},
		{OrientationMirrorVertical, [4]color.RGBA{blue, white, red, green}},
		{OrientationTranspose, [4]color.RGBA{red, blue, green, white}},
		{OrientationRotate90, [4]color.RGBA{blue, red, white, green}},
		{OrientationTransverse, [4]color.RGBA{white, green, blue, red}},
		{OrientationRotate270, [4]color.RGBA{green, white, red, blue}},
	}
	for index, tc := range cases {
		order := binary.ByteOrder(binary.BigEndian)
		if index%2 == 1 {
			order = binary.LittleEndian
		}
		content := withSegments(source, exifSegment(order, uint32(tc.orientation), tiffTypeShort))
		path := writeFile(t, "oriented.jpg", content)

		wantWidth, wantHeight := storedWidth, storedHeight
		if tc.orientation >= OrientationTranspose {
			wantWidth, wantHeight = storedHeight, storedWidth
		}
		info, err := Inspect(context.Background(), path)
		if err != nil {
			t.Fatalf("orientation %d: %v", tc.orientation, err)
		}
		if info.Orientation != tc.orientation || info.Width != wantWidth || info.Height != wantHeight {
			t.Fatalf("orientation %d: Inspect = %+v, want %d × %d", tc.orientation, info, wantWidth, wantHeight)
		}
		if width, height := info.StoredSize(); width != storedWidth || height != storedHeight {
			t.Fatalf("orientation %d: StoredSize = %d × %d", tc.orientation, width, height)
		}

		decoded, err := Decode(context.Background(), path)
		if err != nil {
			t.Fatalf("orientation %d: %v", tc.orientation, err)
		}
		if decoded.Info != info {
			t.Fatalf("orientation %d: Decode info %+v differs from Inspect %+v", tc.orientation, decoded.Info, info)
		}
		if bounds := decoded.Pixels.Bounds(); bounds != image.Rect(0, 0, wantWidth, wantHeight) {
			t.Fatalf("orientation %d: bounds = %v", tc.orientation, bounds)
		}
		quarterX, quarterY := wantWidth/4, wantHeight/4
		points := [4]image.Point{
			{quarterX, quarterY},
			{wantWidth - 1 - quarterX, quarterY},
			{quarterX, wantHeight - 1 - quarterY},
			{wantWidth - 1 - quarterX, wantHeight - 1 - quarterY},
		}
		for corner, point := range points {
			got := color.RGBAModel.Convert(decoded.Pixels.At(point.X, point.Y)).(color.RGBA)
			if !near(got, tc.corners[corner], 24) {
				t.Fatalf("orientation %d: pixel %v = %+v, want %+v", tc.orientation, point, got, tc.corners[corner])
			}
		}
	}
}

// TestOrientReaderPathsAgree runs the transform over every decoder image type
// with a direct pixel reader, and over the generic fallback, and requires
// identical upright pixels from the same picture.
func TestOrientReaderPathsAgree(t *testing.T) {
	const width, height = 5, 3
	reference := image.NewRGBA(image.Rect(0, 0, width, height))
	gray := image.NewGray(reference.Bounds())
	cmyk := image.NewCMYK(reference.Bounds())
	ycbcr := image.NewYCbCr(reference.Bounds(), image.YCbCrSubsampleRatio444)
	// The JPEG decoder's usual 4:2:0 layout shares each chroma sample
	// between 2 × 2 pixels.
	subsampled := image.NewYCbCr(reference.Bounds(), image.YCbCrSubsampleRatio420)
	generic := image.NewNRGBA(reference.Bounds())
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			value := uint8(10 + 40*x + 13*y)
			gray.SetGray(x, y, color.Gray{Y: value})
			reference.SetRGBA(x, y, color.RGBA{R: value, G: value, B: value, A: 0xff})
			cmyk.SetCMYK(x, y, color.CMYK{K: 0xff - value})
			for _, planes := range []*image.YCbCr{ycbcr, subsampled} {
				yi := planes.YOffset(x, y)
				ci := planes.COffset(x, y)
				planes.Y[yi], planes.Cb[ci], planes.Cr[ci] = value, 128, 128
			}
			generic.SetNRGBA(x, y, color.NRGBA{R: value, G: value, B: value, A: 0xff})
		}
	}
	for orientation := OrientationNormal; orientation <= OrientationRotate270; orientation++ {
		want := orient(reference, orientation)
		for name, source := range map[string]image.Image{"gray": gray, "cmyk": cmyk, "ycbcr": ycbcr, "ycbcr420": subsampled, "generic": generic} {
			got := orient(source, orientation)
			if got.Bounds() != want.Bounds() {
				t.Fatalf("%s orientation %d: bounds %v, want %v", name, orientation, got.Bounds(), want.Bounds())
			}
			bounds := want.Bounds()
			for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
				for x := bounds.Min.X; x < bounds.Max.X; x++ {
					a := color.RGBAModel.Convert(got.At(x, y)).(color.RGBA)
					b := color.RGBAModel.Convert(want.At(x, y)).(color.RGBA)
					if !near(a, b, 1) {
						t.Fatalf("%s orientation %d: pixel (%d, %d) = %+v, want %+v", name, orientation, x, y, a, b)
					}
				}
			}
		}
	}
}

// TestOrientExactPixels pins the transform on a picture with a unique value
// at every pixel, for one rotation and one mirror-and-rotation.
func TestOrientExactPixels(t *testing.T) {
	// Stored 3 × 2:
	//   1 2 3
	//   4 5 6
	source := image.NewGray(image.Rect(0, 0, 3, 2))
	copy(source.Pix, []uint8{1, 2, 3, 4, 5, 6})
	cases := []struct {
		orientation Orientation
		width       int
		rows        []uint8
	}{
		// Rotated 90° clockwise, 2 × 3:
		//   4 1
		//   5 2
		//   6 3
		{OrientationRotate90, 2, []uint8{4, 1, 5, 2, 6, 3}},
		// Transposed, 2 × 3:
		//   1 4
		//   2 5
		//   3 6
		{OrientationTranspose, 2, []uint8{1, 4, 2, 5, 3, 6}},
		// Rotated 180°, 3 × 2:
		//   6 5 4
		//   3 2 1
		{OrientationRotate180, 3, []uint8{6, 5, 4, 3, 2, 1}},
		// Mirrored left to right, 3 × 2:
		//   3 2 1
		//   6 5 4
		{OrientationMirrorHorizontal, 3, []uint8{3, 2, 1, 6, 5, 4}},
		// Mirrored top to bottom, 3 × 2:
		//   4 5 6
		//   1 2 3
		{OrientationMirrorVertical, 3, []uint8{4, 5, 6, 1, 2, 3}},
		// Transversed, 2 × 3:
		//   6 3
		//   5 2
		//   4 1
		{OrientationTransverse, 2, []uint8{6, 3, 5, 2, 4, 1}},
		// Rotated 90° counterclockwise, 2 × 3:
		//   3 6
		//   2 5
		//   1 4
		{OrientationRotate270, 2, []uint8{3, 6, 2, 5, 1, 4}},
	}
	// A grayscale source stays grayscale at one byte per pixel, and any
	// other becomes opaque RGBA.
	opaque := image.NewRGBA(source.Bounds())
	draw.Draw(opaque, opaque.Bounds(), source, image.Point{}, draw.Src)
	for _, tc := range cases {
		gray := orient(source, tc.orientation).(*image.Gray)
		rgba := orient(opaque, tc.orientation).(*image.RGBA)
		if gray.Bounds().Dx() != tc.width || rgba.Bounds() != gray.Bounds() {
			t.Fatalf("orientation %d: bounds %v and %v, want width %d", tc.orientation, gray.Bounds(), rgba.Bounds(), tc.width)
		}
		for index, want := range tc.rows {
			x, y := index%tc.width, index/tc.width
			if value := gray.GrayAt(x, y); value.Y != want {
				t.Fatalf("orientation %d: gray pixel (%d, %d) = %d, want %d", tc.orientation, x, y, value.Y, want)
			}
			if value := rgba.RGBAAt(x, y); value.R != want || value.A != 0xff {
				t.Fatalf("orientation %d: RGBA pixel (%d, %d) = %+v, want gray %d", tc.orientation, x, y, value, want)
			}
		}
	}
	for _, unchanged := range []Orientation{OrientationNormal, 0, 9} {
		if orient(source, unchanged) != image.Image(source) {
			t.Fatalf("orientation %d did not return the source unchanged", unchanged)
		}
		if unchanged.Reorients() {
			t.Fatalf("orientation %d reports that it reorients", unchanged)
		}
	}

	// A source whose bounds do not start at the origin is read from its own
	// bounds: the stored 2 × 2 block at (1, 0) holds 2 3 / 5 6.
	for name, offset := range map[string]image.Image{"gray": source.SubImage(image.Rect(1, 0, 3, 2)), "RGBA": opaque.SubImage(image.Rect(1, 0, 3, 2))} {
		got := orient(offset, OrientationRotate90)
		for index, want := range []uint8{5, 2, 6, 3} {
			x, y := index%2, index/2
			if value := color.GrayModel.Convert(got.At(x, y)).(color.Gray); value.Y != want {
				t.Fatalf("offset %s source: pixel (%d, %d) = %d, want %d", name, x, y, value.Y, want)
			}
		}
	}
}

// TestDecodeOrientsGrayAndCMYKJPEGs exercises the gray and CMYK reader paths
// end to end through real JPEG data.
func TestDecodeOrientsGrayAndCMYKJPEGs(t *testing.T) {
	grayJPEG := encodeJPEG(t, image.NewGray(image.Rect(0, 0, 40, 24)))
	for name, content := range map[string][]byte{
		"gray.jpg": withSegments(grayJPEG, exifSegment(binary.BigEndian, uint32(OrientationRotate90), tiffTypeShort)),
		"cmyk.jpg": withSegments(readFixture(t, "cmyk.jpeg"), exifSegment(binary.LittleEndian, uint32(OrientationRotate270), tiffTypeLong)),
	} {
		decoded, err := Decode(context.Background(), writeFile(t, name, content))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		stored := image.Rect(0, 0, 40, 24)
		if name == "cmyk.jpg" {
			stored = image.Rect(0, 0, 150, 103)
		}
		if bounds := decoded.Pixels.Bounds(); bounds.Dx() != stored.Dy() || bounds.Dy() != stored.Dx() {
			t.Fatalf("%s: upright bounds %v for stored %v", name, bounds, stored)
		}
		_, gray := decoded.Pixels.(*image.Gray)
		_, rgba := decoded.Pixels.(*image.RGBA)
		if (name == "gray.jpg" && !gray) || (name == "cmyk.jpg" && !rgba) {
			t.Fatalf("%s: reoriented pixels are %T", name, decoded.Pixels)
		}
	}
}

func TestPNGIsNeverReoriented(t *testing.T) {
	// EXIF orientation is a JPEG concept here (RSZ-018); PNG sources always
	// report the stored dimensions.
	info, err := Inspect(context.Background(), writeFile(t, "wide.png", encodePNG(t, image.NewRGBA(image.Rect(0, 0, 9, 4)))))
	if err != nil {
		t.Fatal(err)
	}
	if info.Orientation != OrientationNormal || info.Width != 9 || info.Height != 4 {
		t.Fatalf("Inspect = %+v", info)
	}
}

func scan(content []byte) Orientation {
	orientation, _ := scanJPEG(bufio.NewReader(bytes.NewReader(content)))
	return orientation
}

func TestReadOrientationScansSegments(t *testing.T) {
	plain := encodeJPEG(t, image.NewGray(image.Rect(0, 0, 8, 8)))
	jfif := []byte{0xff, 0xe0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0, 1, 1, 0, 0, 1, 0, 1, 0, 0}
	xmp := app1(append([]byte("http://ns.adobe.com/xap/1.0/\x00"), []byte("<x:xmpmeta/>")...))
	rotate := exifSegment(binary.BigEndian, uint32(OrientationRotate90), tiffTypeShort)
	fill := append([]byte{0xff, 0xff}, rotate[1:]...)

	cases := []struct {
		name    string
		content []byte
		want    Orientation
	}{
		{"untagged", plain, OrientationNormal},
		{"after JFIF", withSegments(plain, jfif, rotate), OrientationRotate90},
		{"after XMP", withSegments(plain, xmp, rotate), OrientationRotate90},
		{"fill bytes", withSegments(plain, fill), OrientationRotate90},
		// The standard library decoder skips stray bytes between segments,
		// so the scan must too.
		{"padding after SOI", withSegments(plain, []byte{0, 0, 0}, rotate), OrientationRotate90},
		{"padding between segments", withSegments(plain, jfif, []byte{0x00, 0x12}, rotate), OrientationRotate90},
		{"escaped 0xFF between segments", withSegments(plain, jfif, []byte{0xff, 0x00}, rotate), OrientationRotate90},
		{"EXIF after the frame", beforeScan(t, plain, rotate), OrientationRotate90},
		{"LONG type", withSegments(plain, exifSegment(binary.LittleEndian, 3, tiffTypeLong)), OrientationRotate180},
		{"first EXIF wins", withSegments(plain, exifSegment(binary.BigEndian, 8, tiffTypeShort), rotate), OrientationRotate270},
		{"zero value", withSegments(plain, exifSegment(binary.BigEndian, 0, tiffTypeShort)), OrientationNormal},
		{"out of range", withSegments(plain, exifSegment(binary.BigEndian, 9, tiffTypeShort)), OrientationNormal},
		{"huge LONG", withSegments(plain, exifSegment(binary.BigEndian, 0x106, tiffTypeLong)), OrientationNormal},
		{"wrong type", withSegments(plain, exifSegment(binary.BigEndian, 6, 7)), OrientationNormal},
		{"not a JPEG", []byte("plain text"), OrientationNormal},
		{"truncated segment", withSegments(plain, rotate[:12])[:16], OrientationNormal},
	}
	for _, tc := range cases {
		if got := scan(tc.content); got != tc.want {
			t.Fatalf("%s: orientation %d, want %d", tc.name, got, tc.want)
		}
	}
}

// beforeScan inserts a segment directly before a JPEG's first SOS marker,
// after its frame header.
func beforeScan(t *testing.T, jpegData, segment []byte) []byte {
	t.Helper()
	index := bytes.Index(jpegData, []byte{0xff, markerSOS})
	if index < 0 {
		t.Fatal("SOS marker not found")
	}
	result := append([]byte(nil), jpegData[:index]...)
	result = append(result, segment...)
	return append(result, jpegData[index:]...)
}

// TestPaddedEXIFStillOrients pins that a file with stray bytes between its
// header segments — which the decoder accepts — keeps its orientation end
// to end, rather than decoding sideways.
func TestPaddedEXIFStillOrients(t *testing.T) {
	rotate := exifSegment(binary.BigEndian, uint32(OrientationRotate90), tiffTypeShort)
	content := withSegments(encodeJPEG(t, image.NewGray(image.Rect(0, 0, 40, 24))), []byte{0, 0}, rotate)
	info, err := Inspect(context.Background(), writeFile(t, "padded.jpg", content))
	if err != nil {
		t.Fatal(err)
	}
	if info.Orientation != OrientationRotate90 || info.Width != 24 || info.Height != 40 {
		t.Fatalf("Inspect = %+v, want the upright 24 × 40 of orientation 6", info)
	}
}

// TestScanJPEGReadsTheFrame pins the frame facts the memory estimate uses:
// the coding process and the sampling of each fixture.
func TestScanJPEGReadsTheFrame(t *testing.T) {
	cases := []struct {
		name    string
		content []byte
		want    jpegFrame
	}{
		{"gray", encodeJPEG(t, image.NewGray(image.Rect(0, 0, 8, 8))), jpegFrame{valid: true, mcuWidth: 8, mcuHeight: 8, samples: 1}},
		{"baseline", readFixture(t, "baseline.jpeg"), jpegFrame{valid: true, mcuWidth: 16, mcuHeight: 16, samples: 6}},
		{"progressive", readFixture(t, "progressive.jpeg"), jpegFrame{valid: true, progressive: true, mcuWidth: 16, mcuHeight: 16, samples: 6}},
		{"cmyk", readFixture(t, "cmyk.jpeg"), jpegFrame{valid: true, mcuWidth: 8, mcuHeight: 8, samples: 4}},
		{"no frame", []byte{0xff, markerSOI, 0xff, markerEOI}, jpegFrame{}},
	}
	for _, tc := range cases {
		if _, frame := scanJPEG(bufio.NewReader(bytes.NewReader(tc.content))); frame != tc.want {
			t.Fatalf("%s: frame = %+v, want %+v", tc.name, frame, tc.want)
		}
	}
}

func TestParseTIFFOrientationRejectsMalformedStructures(t *testing.T) {
	valid := exifSegment(binary.LittleEndian, 6, tiffTypeShort)[4+len(exifHeader):]
	if orientation, ok := parseTIFFOrientation(valid); !ok || orientation != OrientationRotate90 {
		t.Fatalf("valid structure = %d, %v", orientation, ok)
	}
	mutate := func(change func([]byte)) []byte {
		tiff := append([]byte(nil), valid...)
		change(tiff)
		return tiff
	}
	cases := map[string][]byte{
		"empty":           nil,
		"short header":    valid[:6],
		"bad byte order":  mutate(func(b []byte) { b[0], b[1] = 'X', 'X' }),
		"bad magic":       mutate(func(b []byte) { binary.LittleEndian.PutUint16(b[2:], 43) }),
		"IFD past end":    mutate(func(b []byte) { binary.LittleEndian.PutUint32(b[4:], 0xfffffff0) }),
		"IFD inside head": mutate(func(b []byte) { binary.LittleEndian.PutUint32(b[4:], 2) }),
		"entries past end": mutate(func(b []byte) {
			// The only real entry is not the orientation, so the scan
			// walks into the entries the count claims past the end.
			binary.LittleEndian.PutUint16(b[8:], 400)
			binary.LittleEndian.PutUint16(b[10:], 0x0110)
		}),
		"count not one":   mutate(func(b []byte) { binary.LittleEndian.PutUint32(b[14:], 2) }),
		"truncated entry": valid[:14],
		"other tag only":  mutate(func(b []byte) { binary.LittleEndian.PutUint16(b[10:], 0x0110) }),
	}
	for name, tiff := range cases {
		if orientation, ok := parseTIFFOrientation(tiff); ok {
			t.Fatalf("%s: parsed orientation %d from a malformed structure", name, orientation)
		}
	}
}

// TestMalformedEXIFStillDecodes pins that broken orientation metadata never
// rejects a source whose image data decodes: it is treated as upright.
func TestMalformedEXIFStillDecodes(t *testing.T) {
	broken := app1(append(append([]byte(nil), exifHeader...), []byte("MM\x00\x2a\xff\xff\xff\xff")...))
	content := withSegments(encodeJPEG(t, image.NewGray(image.Rect(0, 0, 10, 6))), broken)
	info, err := Inspect(context.Background(), writeFile(t, "broken-exif.jpg", content))
	if err != nil {
		t.Fatal(err)
	}
	if info.Orientation != OrientationNormal || info.Width != 10 || info.Height != 6 {
		t.Fatalf("Inspect = %+v", info)
	}
}
