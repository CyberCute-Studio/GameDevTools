package job

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/jpeg"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"runtime/metrics"
	"sync"
	"testing"
	"time"

	"github.com/CyberCute-Studio/GameDevTools/internal/imageresizer/decode"
	"github.com/CyberCute-Studio/GameDevTools/internal/imageresizer/output"
	"github.com/CyberCute-Studio/GameDevTools/internal/preset"
)

// The NFR-013 reference workload: 100 mixed 12-megapixel JPEG sources fit
// within 1024 × 1024. "Mixed" follows what a camera or phone produces: 40
// landscape 4000 × 3000 pictures, 30 stored as portrait 3000 × 4000, and 30
// stored as landscape with an EXIF orientation that makes them upright
// portraits, 6 or 8 in turn, all 4:2:0 at quality 90 as the standard
// library encodes color, with photo-like content of gradients, texture, and
// noise, so their entropy-coded data costs about what a photograph's does
// to decode. Seven distinct pictures are encoded, four landscape and three
// portrait, and copied under 100 names, since decoding a copy costs what
// decoding the original does.
const (
	referenceSources = 100
	referenceLong    = 4000
	referenceShort   = 3000
)

// referencePicture is a photo-like 4:2:0 picture: smooth luma gradients and
// texture with noise, and chroma that drifts across it, from the seed.
func referencePicture(width, height int, seed uint32) *image.YCbCr {
	img := image.NewYCbCr(image.Rect(0, 0, width, height), image.YCbCrSubsampleRatio420)
	phase := float64(seed) * 0.7
	across := make([]float64, width)
	for x := range across {
		across[x] = 55 * math.Sin(float64(x)/97+phase)
	}
	down := make([]float64, height)
	for y := range down {
		down[y] = math.Cos(float64(y)/61 - phase)
	}
	diagonal := make([]float64, width+height)
	for d := range diagonal {
		diagonal[d] = 25 * math.Sin(float64(d)/13+phase)
	}
	state := seed*2654435761 + 1
	for y := range height {
		row := img.Y[y*img.YStride : y*img.YStride+width]
		for x := range row {
			state ^= state << 13
			state ^= state >> 17
			state ^= state << 5
			value := 128 + across[x]*down[y] + diagonal[x+y] + float64(state%25) - 12
			row[x] = uint8(max(0, min(255, value)))
		}
	}
	for y := range (height + 1) / 2 {
		for x := range (width + 1) / 2 {
			offset := y*img.CStride + x
			img.Cb[offset] = uint8(96 + (x*64)/width + (y*32)/height + int(seed%16))
			img.Cr[offset] = uint8(160 - (y*64)/height - (x*16)/width - int(seed%8))
		}
	}
	return img
}

func encodeReference(b *testing.B, img image.Image) []byte {
	b.Helper()
	var buffer bytes.Buffer
	if err := jpeg.Encode(&buffer, img, &jpeg.Options{Quality: 90}); err != nil {
		b.Fatal(err)
	}
	return buffer.Bytes()
}

// referenceInputs writes the reference sources into a new folder and lists
// each as the source list does once its content check succeeds, returning
// the inputs, the bytes the sources take on disk, and the memory decoding
// every source at once would take by their checks' estimates.
func referenceInputs(b *testing.B) ([]Input, uint64, uint64) {
	b.Helper()
	var landscapes, portraits [][]byte
	for seed := range uint32(4) {
		landscapes = append(landscapes, encodeReference(b, referencePicture(referenceLong, referenceShort, seed)))
	}
	for seed := range uint32(3) {
		portraits = append(portraits, encodeReference(b, referencePicture(referenceShort, referenceLong, seed+4)))
	}
	folder := b.TempDir()
	inputs := make([]Input, 0, referenceSources)
	var onDisk, decodeAll uint64
	for index := range referenceSources {
		var content []byte
		switch {
		case index%10 < 4:
			content = landscapes[index%len(landscapes)]
		case index%10 < 7:
			content = portraits[index%len(portraits)]
		default:
			orientation := uint16(6)
			if index%2 == 1 {
				orientation = 8
			}
			content = withOrientation(landscapes[index%len(landscapes)], orientation)
		}
		path := filepath.Join(folder, fmt.Sprintf("photo-%03d.jpg", index))
		if err := os.WriteFile(path, content, 0o644); err != nil {
			b.Fatal(err)
		}
		info, err := decode.Inspect(context.Background(), path)
		if err != nil {
			b.Fatalf("inspect %s: %v", path, err)
		}
		inputs = append(inputs, Input{Path: path, Info: info})
		onDisk += uint64(len(content))
		decodeAll += info.DecodedBytes
	}
	return inputs, onDisk, decodeAll
}

// memorySampler records, while a job runs, the most memory the Go runtime
// held: the heap's objects, garbage not yet swept included, and every byte
// the runtime has mapped and not handed back to the operating system — for
// a program with no C code, all of its memory but the executable's and the
// operating system's own. It reads the runtime's metrics every millisecond,
// which stops nothing.
type memorySampler struct {
	stop      chan struct{}
	done      sync.WaitGroup
	heap      uint64
	footprint uint64
}

func startSampling() *memorySampler {
	s := &memorySampler{stop: make(chan struct{})}
	samples := []metrics.Sample{
		{Name: "/memory/classes/heap/objects:bytes"},
		{Name: "/memory/classes/total:bytes"},
		{Name: "/memory/classes/heap/released:bytes"},
	}
	read := func() {
		metrics.Read(samples)
		s.heap = max(s.heap, samples[0].Value.Uint64())
		s.footprint = max(s.footprint, samples[1].Value.Uint64()-samples[2].Value.Uint64())
	}
	s.done.Add(1)
	go func() {
		defer s.done.Done()
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
		for {
			read()
			select {
			case <-s.stop:
				read()
				return
			case <-ticker.C:
			}
		}
	}()
	return s
}

// finish stops sampling and returns the peaks.
func (s *memorySampler) finish() (heap, footprint uint64) {
	close(s.stop)
	s.done.Wait()
	return s.heap, s.footprint
}

const mebibyte = 1 << 20

// BenchmarkRun100Mixed12MegapixelJPEGsFit1024 measures NFR-013 end to end:
// one op is a whole job resizing the reference workload, a fresh output
// folder each time, through Run with the job's own worker count and memory
// budget — content checks excluded, as the source list makes them when
// files are added — so ms/op is the duration NFR-013 bounds at two
// minutes. It also records NFR-014's memory observations: the peak heap and
// runtime footprint while the job runs, beside what decoding every source at
// once would take by their estimates, and fails if the heap ever outgrew the
// job's 1 GiB budget. The sources take 370 MiB of disk; generating them takes
// about 20 seconds before the timer starts.
func BenchmarkRun100Mixed12MegapixelJPEGsFit1024(b *testing.B) {
	inputs, onDisk, decodeAll := referenceInputs(b)
	request := Request{
		Inputs:    inputs,
		Format:    output.FormatKeepSource,
		Collision: output.CollisionRename,
		Settings:  settings(1024, 1024, preset.ResizeFit, white),
	}
	var peakHeap, peakFootprint uint64
	b.ResetTimer()
	for range b.N {
		b.StopTimer()
		request.Folder = filepath.Join(b.TempDir(), "Resized")
		runtime.GC()
		sampler := startSampling()
		b.StartTimer()
		summary, err := Run(context.Background(), request, nil)
		b.StopTimer()
		heap, footprint := sampler.finish()
		peakHeap, peakFootprint = max(peakHeap, heap), max(peakFootprint, footprint)
		if err != nil {
			b.Fatalf("Run: %v", err)
		}
		if summary.Progress.Completed != referenceSources {
			b.Fatalf("summary = %+v, want every source completed", summary.Progress)
		}
		for index, want := range map[int]image.Point{0: {1024, 768}, 4: {768, 1024}, 7: {768, 1024}, 8: {768, 1024}} {
			result, err := decode.Decode(context.Background(), summary.Items[index].Destination)
			if err != nil || result.Width != want.X || result.Height != want.Y {
				b.Fatalf("%s decodes as %+v, %v, want %d × %d", summary.Items[index].Destination, result, err, want.X, want.Y)
			}
		}
		b.StartTimer()
	}
	b.StopTimer()
	b.ReportMetric(float64(b.Elapsed().Milliseconds())/float64(b.N*referenceSources), "ms/image")
	b.ReportMetric(float64(defaultWorkers()), "workers")
	b.ReportMetric(float64(peakHeap)/mebibyte, "peak-heap-MiB")
	b.ReportMetric(float64(peakFootprint)/mebibyte, "peak-runtime-MiB")
	b.ReportMetric(float64(decodeAll)/mebibyte, "decode-all-MiB")
	b.ReportMetric(float64(onDisk)/mebibyte, "sources-MiB")
	if peakHeap > memoryBudget {
		b.Fatalf("the heap peaked at %d MiB, beyond the job's %d MiB budget", peakHeap/mebibyte, memoryBudget/mebibyte)
	}
}
