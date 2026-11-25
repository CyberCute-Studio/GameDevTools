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

func testRequest() screenshot.CaptureRequest {
	return screenshot.CaptureRequest{
		Window:     1234,
		DeviceName: `\\.\DISPLAY1`,
		Options:    screenshot.DefaultOptions(`C:\Screenshots`),
	}
}

func TestFullDisplayRunsHideCaptureRestoreDeliverInOrder(t *testing.T) {
	var events []string
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	request := testRequest()
	service := &Service{
		Monitor: display.NewMonitor(),
		Hide: func(windows ...uintptr) (func(), error) {
			events = append(events, "hide")
			if !reflect.DeepEqual(windows, []uintptr{request.Window}) {
				t.Errorf("hidden windows = %v, want [%d]", windows, request.Window)
			}
			return func() { events = append(events, "restore") }, nil
		},
		Capture: func(monitor *display.Monitor, deviceName string) (*image.RGBA, error) {
			events = append(events, "capture")
			if deviceName != request.DeviceName {
				t.Errorf("captured display = %q, want %q", deviceName, request.DeviceName)
			}
			return img, nil
		},
		Deliver: func(got *image.RGBA, options screenshot.CaptureOptions) output.Result {
			events = append(events, "deliver")
			if got != img {
				t.Error("delivery did not receive the captured image")
			}
			if options != request.Options {
				t.Errorf("delivered options = %+v, want %+v", options, request.Options)
			}
			return output.Result{Path: `C:\Screenshots\Screenshot_1.png`}
		},
	}

	result := service.FullDisplay(request)

	expected := []string{"hide", "capture", "restore", "deliver"}
	if !reflect.DeepEqual(events, expected) {
		t.Fatalf("event order = %v, want %v", events, expected)
	}
	if result.Err != nil || result.Clipboard != nil || result.Save != nil {
		t.Fatalf("result errors = (%v, %v, %v)", result.Err, result.Clipboard, result.Save)
	}
	if result.Path != `C:\Screenshots\Screenshot_1.png` {
		t.Fatalf("result path = %q", result.Path)
	}
}

func TestFullDisplayHideFailureSkipsCaptureAndDelivery(t *testing.T) {
	hideErr := errors.New("compositor flush failed")
	captureCalled := false
	deliverCalled := false
	service := &Service{
		Monitor: display.NewMonitor(),
		Hide: func(...uintptr) (func(), error) {
			return nil, hideErr
		},
		Capture: func(*display.Monitor, string) (*image.RGBA, error) {
			captureCalled = true
			return nil, nil
		},
		Deliver: func(*image.RGBA, screenshot.CaptureOptions) output.Result {
			deliverCalled = true
			return output.Result{}
		},
	}

	result := service.FullDisplay(testRequest())

	if !errors.Is(result.Err, hideErr) {
		t.Fatalf("result error = %v, want %v", result.Err, hideErr)
	}
	if captureCalled || deliverCalled {
		t.Fatal("capture or delivery ran after a failed hide")
	}
}

func TestFullDisplayCaptureFailureRestoresWindowAndSkipsDelivery(t *testing.T) {
	captureErr := errors.New("display is no longer connected")
	restored := false
	deliverCalled := false
	service := &Service{
		Monitor: display.NewMonitor(),
		Hide: func(...uintptr) (func(), error) {
			return func() { restored = true }, nil
		},
		Capture: func(*display.Monitor, string) (*image.RGBA, error) {
			return nil, captureErr
		},
		Deliver: func(*image.RGBA, screenshot.CaptureOptions) output.Result {
			deliverCalled = true
			return output.Result{}
		},
	}

	result := service.FullDisplay(testRequest())

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

func TestFullDisplayReportsIndependentDeliveryOutcomes(t *testing.T) {
	clipboardErr := errors.New("clipboard busy")
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	service := &Service{
		Monitor: display.NewMonitor(),
		Hide: func(...uintptr) (func(), error) {
			return func() {}, nil
		},
		Capture: func(*display.Monitor, string) (*image.RGBA, error) {
			return img, nil
		},
		Deliver: func(*image.RGBA, screenshot.CaptureOptions) output.Result {
			return output.Result{Path: `C:\Screenshots\Screenshot_1.png`, Clipboard: clipboardErr}
		},
	}

	result := service.FullDisplay(testRequest())

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

// TestFullDisplayRunsCapturedOnceTheScreenIsRead pins when the request's
// Captured runs, which ends the view's hold on the application's dialogs: once
// the display is read and the window restored, before delivery, so a dialog
// may open while the file is written but not while the screen is read; and
// once on a failed hide or capture, so a failure cannot keep the dialogs
// closed.
func TestFullDisplayRunsCapturedOnceTheScreenIsRead(t *testing.T) {
	for _, test := range []struct {
		name       string
		hideErr    error
		captureErr error
		want       []string
	}{
		{"read", nil, nil, []string{"hide", "capture", "restore", "captured", "deliver"}},
		{"capture failed", nil, errors.New("BitBlt failed"), []string{"hide", "capture", "restore", "captured"}},
		{"hide failed", errors.New("compositor flush failed"), nil, []string{"hide", "captured"}},
	} {
		var events []string
		request := testRequest()
		request.Captured = func() { events = append(events, "captured") }
		service := &Service{
			Monitor: display.NewMonitor(),
			Hide: func(...uintptr) (func(), error) {
				events = append(events, "hide")
				if test.hideErr != nil {
					return nil, test.hideErr
				}
				return func() { events = append(events, "restore") }, nil
			},
			Capture: func(*display.Monitor, string) (*image.RGBA, error) {
				events = append(events, "capture")
				if test.captureErr != nil {
					return nil, test.captureErr
				}
				return image.NewRGBA(image.Rect(0, 0, 2, 2)), nil
			},
			Deliver: func(*image.RGBA, screenshot.CaptureOptions) output.Result {
				events = append(events, "deliver")
				return output.Result{Path: `C:\Screenshots\Screenshot_1.png`}
			},
		}
		service.FullDisplay(request)
		if !reflect.DeepEqual(events, test.want) {
			t.Fatalf("%s: events = %v, want %v", test.name, events, test.want)
		}
	}
}
