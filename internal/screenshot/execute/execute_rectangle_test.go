package execute

import (
	"errors"
	"image"
	"reflect"
	"testing"

	"github.com/CyberCute-Studio/GameDevTools/internal/display"
	"github.com/CyberCute-Studio/GameDevTools/internal/screenshot"
	"github.com/CyberCute-Studio/GameDevTools/internal/screenshot/output"
)

func TestRectangleRunsCaptureRestoreDeliverInOrder(t *testing.T) {
	var events []string
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	region := display.Rect{Left: -100, Top: -50, Right: 700, Bottom: 450}
	options := screenshot.DefaultOptions(`C:\Screenshots`)
	service := &Service{
		Monitor: display.NewMonitor(),
		CaptureRect: func(monitor *display.Monitor, got display.Rect) (*image.RGBA, error) {
			events = append(events, "capture")
			if got != region {
				t.Errorf("captured region = %v, want %v", got, region)
			}
			return img, nil
		},
		Deliver: func(got *image.RGBA, gotOptions screenshot.CaptureOptions) output.Result {
			events = append(events, "deliver")
			if got != img {
				t.Error("delivery did not receive the captured image")
			}
			if gotOptions != options {
				t.Errorf("delivered options = %+v, want %+v", gotOptions, options)
			}
			return output.Result{Path: `C:\Screenshots\Screenshot_1.png`}
		},
	}

	result := service.Rectangle(region, func() { events = append(events, "restore") }, options)

	expected := []string{"capture", "restore", "deliver"}
	if !reflect.DeepEqual(events, expected) {
		t.Fatalf("event order = %v, want %v (the window is restored as soon as the pixels are read)", events, expected)
	}
	if result.Err != nil || result.Clipboard != nil || result.Save != nil {
		t.Fatalf("result errors = (%v, %v, %v)", result.Err, result.Clipboard, result.Save)
	}
	if result.Path != `C:\Screenshots\Screenshot_1.png` {
		t.Fatalf("result path = %q", result.Path)
	}
}

func TestRectangleCaptureFailureRestoresWindowAndSkipsDelivery(t *testing.T) {
	captureErr := errors.New("graphics driver rejected the capture")
	restored := false
	deliverCalled := false
	service := &Service{
		Monitor: display.NewMonitor(),
		CaptureRect: func(*display.Monitor, display.Rect) (*image.RGBA, error) {
			return nil, captureErr
		},
		Deliver: func(*image.RGBA, screenshot.CaptureOptions) output.Result {
			deliverCalled = true
			return output.Result{}
		},
	}

	result := service.Rectangle(display.Rect{Right: 10, Bottom: 10}, func() { restored = true }, screenshot.DefaultOptions(`C:\Screenshots`))

	if !errors.Is(result.Err, captureErr) {
		t.Fatalf("result error = %v, want %v", result.Err, captureErr)
	}
	if deliverCalled {
		t.Fatal("delivery ran after a failed capture")
	}
	if !restored {
		t.Fatal("window was not restored after a failed capture")
	}
}

func TestRectangleReportsIndependentDeliveryOutcomes(t *testing.T) {
	clipboardErr := errors.New("clipboard busy")
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	service := &Service{
		Monitor: display.NewMonitor(),
		CaptureRect: func(*display.Monitor, display.Rect) (*image.RGBA, error) {
			return img, nil
		},
		Deliver: func(*image.RGBA, screenshot.CaptureOptions) output.Result {
			return output.Result{Path: `C:\Screenshots\Screenshot_1.png`, Clipboard: clipboardErr}
		},
	}

	result := service.Rectangle(display.Rect{Right: 1, Bottom: 1}, func() {}, screenshot.DefaultOptions(`C:\Screenshots`))

	if result.Err != nil {
		t.Fatalf("result error = %v", result.Err)
	}
	if !errors.Is(result.Clipboard, clipboardErr) {
		t.Fatalf("clipboard outcome = %v, want %v", result.Clipboard, clipboardErr)
	}
	if result.Save != nil {
		t.Fatalf("save outcome = %v", result.Save)
	}
	if result.Path != `C:\Screenshots\Screenshot_1.png` {
		t.Fatalf("result path = %q", result.Path)
	}
}
