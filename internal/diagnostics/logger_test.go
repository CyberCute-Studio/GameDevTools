package diagnostics

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestLoggerRotatesStructuredEvents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs", "gamedevtools.log")
	logger, err := OpenWithLimits(path, 240, 2)
	if err != nil {
		t.Fatal(err)
	}
	logger.now = func() time.Time { return time.Date(2026, 11, 11, 12, 30, 0, 0, time.UTC) }
	for i := 0; i < 9; i++ {
		if err := logger.Record(Event{Level: LevelWarning, Operation: "settings.load", Code: "recovered-with-warnings"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := logger.Close(); err != nil {
		t.Fatal(err)
	}

	files, err := filepath.Glob(path + "*")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 {
		t.Fatalf("rotated log files = %v, want active plus two backups", files)
	}
	for _, name := range files {
		assertStructuredLog(t, name)
	}
}

func TestLoggerRejectsFreeFormMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gamedevtools.log")
	logger, err := OpenWithLimits(path, 1024, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Close()
	for _, event := range []Event{
		{Level: LevelError, Operation: "C:\\images\\secret.png", Code: "failed"},
		{Level: LevelError, Operation: "settings.save", Code: "clipboard contents"},
		{Level: "fatal", Operation: "settings.save", Code: "failed"},
	} {
		if err := logger.Record(event); err == nil {
			t.Fatalf("Record(%+v) succeeded", event)
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != 0 {
		t.Fatalf("invalid events wrote %d bytes", info.Size())
	}
}

func TestLoggerConcurrentRecordsRemainValid(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gamedevtools.log")
	logger, err := OpenWithLimits(path, 4096, 1)
	if err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	for i := 0; i < 20; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			if err := logger.Record(Event{Level: LevelInfo, Operation: "app.shell", Code: "started"}); err != nil {
				t.Error(err)
			}
		}()
	}
	workers.Wait()
	if err := logger.Close(); err != nil {
		t.Fatal(err)
	}
	assertStructuredLog(t, path)
}

func assertStructuredLog(t *testing.T, path string) {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	lines := 0
	for scanner.Scan() {
		lines++
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(scanner.Bytes(), &fields); err != nil {
			t.Fatalf("%s contains invalid JSON: %v", path, err)
		}
		if len(fields) != 4 || fields["time"] == nil || fields["level"] == nil || fields["operation"] == nil || fields["code"] == nil {
			t.Fatalf("%s contains unexpected fields: %v", path, fields)
		}
		if strings.Contains(scanner.Text(), ".png") || strings.Contains(scanner.Text(), "clipboard") {
			t.Fatalf("%s contains user data: %s", path, scanner.Text())
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if lines == 0 {
		t.Fatalf("%s is empty", path)
	}
}
