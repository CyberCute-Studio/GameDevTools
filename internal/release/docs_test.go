package release

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/CyberCute-Studio/GameDevTools/internal/screenshot"
	"github.com/CyberCute-Studio/GameDevTools/internal/ui"
)

var (
	prdRequirement  = regexp.MustCompile(`\*\*((?:APP|PRESET|SCR|RSZ|SET|UX|NFR|TECH)-\d+) \((P\d)\)`)
	prdScenario     = regexp.MustCompile(`(?m)^\*\*(AC-[SRA]\d+) `)
	traceabilityRow = regexp.MustCompile(`^\| ((?:APP|PRESET|SCR|RSZ|SET|UX|NFR|TECH)-\d+|AC-[SRA]\d+) \|`)
	planItem        = regexp.MustCompile(`(?m)^\d+\. \*\*([A-Z]+-\d+[A-Z]?) — `)
	evidenceToken   = regexp.MustCompile("`([^`]+)`")
	testFunction    = regexp.MustCompile(`(?m)^func ((?:Test|Benchmark)\w+)\(`)
	manualCheck     = regexp.MustCompile(`\bM\d+\b`)
	manualCheckRow  = regexp.MustCompile(`^\| (M\d+) \|`)
	requirementID   = regexp.MustCompile(`(?:APP|PRESET|SCR|RSZ|SET|UX|NFR|TECH)-\d+|AC-[SRA]\d+`)
	requirementSpan = regexp.MustCompile(`([A-Z]+(?:-[SRA])?-?)(\d+) through ([A-Z]+(?:-[SRA])?-?)(\d+)`)
	recordedRun     = regexp.MustCompile(`^\| \d{4}-\d{2}-\d{2} \| (M\d+|QLT-04A) \| [0-9a-f]{7,40} \| ([^|]+) \|$`)
)

// hardwareGated are the requirements that wait on QLT-04A's hardware, which
// no row may call complete until docs/testing.md records a run that passed
// it. It is written out rather than read from the Open column, so that
// clearing a row's Open cell cannot remove its guard.
var hardwareGated = []string{
	"SCR-020", "SCR-026", "SCR-028", "SCR-042", "SCR-043", "SCR-054",
	"NFR-001", "NFR-003", "NFR-010", "NFR-011", "NFR-012", "NFR-013",
	"TECH-002", "TECH-003", "TECH-009", "AC-S02", "AC-S12",
}

// manualGated maps each requirement whose defining condition only manual
// checks exercise, in a real application, to those checks. No row may call it
// complete until docs/testing.md records each of them passing it. It is
// written out so that editing a check's Covers cell cannot remove the guard.
var manualGated = map[string][]string{
	"SCR-002": {"M10"}, "SCR-005": {"M10"},
	"SCR-010": {"M15"}, "SCR-011": {"M15"}, "SCR-012": {"M15"}, "AC-S05": {"M15"},
	"SCR-021": {"M9"}, "SCR-022": {"M9"}, "SCR-023": {"M9"}, "SCR-055": {"M9"},
	"AC-S03": {"M9"}, "AC-S04": {"M9"},
	"SCR-062": {"M8"},
	"SCR-063": {"M16"}, "RSZ-001": {"M16"}, "RSZ-002": {"M16"},
	"RSZ-008": {"M16"}, "RSZ-020": {"M16"}, "RSZ-048": {"M16"},
	"SCR-069": {"M16", "M18"},
	"AC-A04":  {"M12"},
	"APP-005": {"M17"},
	"SCR-031": {"M18"}, "SCR-060": {"M18"}, "AC-S08": {"M18"}, "AC-S09": {"M18"},
	"UX-006": {"M2"}, "AC-A03": {"M2"},
	"SCR-014": {"M15"}, "SCR-044": {"M9", "M15"}, "SCR-068": {"M9", "M15"}, "AC-S01": {"M9"},
	"SCR-040": {"M19"}, "AC-S06": {"M19"},
}

// gatingChecks are the manual checks that are the only real run of part of a
// requirement.
var gatingChecks = []string{"M2", "M8", "M9", "M10", "M12", "M15", "M16", "M17", "M18", "M19"}

// confidenceOnly names the gating check a row may cite while it stays Pass,
// because automated checks already cover that row: the job's thread and its
// coalesced progress are what keep the GUI responsive (RSZ-041), and M12's
// drag belongs to AC-A04.
var confidenceOnly = map[string]string{"RSZ-041": "M12"}

type traceRow struct {
	id, priority, items, evidence, status, open string
}

func readTraceability(t *testing.T) []traceRow {
	t.Helper()
	var rows []traceRow
	for _, line := range strings.Split(string(repositoryFile(t, "docs", "traceability.md")), "\n") {
		if !traceabilityRow.MatchString(line) {
			continue
		}
		cells := strings.Split(strings.Trim(strings.TrimSpace(line), "|"), "|")
		if len(cells) != 7 {
			t.Fatalf("row has %d cells, want 7: %s", len(cells), line)
		}
		for i := range cells {
			cells[i] = strings.TrimSpace(cells[i])
		}
		rows = append(rows, traceRow{cells[0], cells[1], cells[3], cells[4], cells[5], cells[6]})
	}
	return rows
}

// repositoryTests returns every test and benchmark as package.Name, where an
// internal package is its folder's name, or parent/folder for the internal
// packages that share a folder name, and a command is cmd/folder, so that a
// row cannot cite one package's test for another and a command's tests cannot
// rename an internal package's.
func repositoryTests(t *testing.T) map[string]bool {
	t.Helper()
	found := map[string]bool{}
	byFolder := map[string][]string{}
	for _, top := range []string{"internal", "cmd"} {
		root := filepath.Join("..", "..", top)
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() || !strings.HasSuffix(path, "_test.go") {
				return err
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			folder := filepath.Dir(path)
			for _, match := range testFunction.FindAllSubmatch(data, -1) {
				if top == "cmd" {
					found["cmd/"+filepath.Base(folder)+"."+string(match[1])] = true
				} else {
					byFolder[folder] = append(byFolder[folder], string(match[1]))
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	uses := map[string]int{}
	for folder := range byFolder {
		uses[filepath.Base(folder)]++
	}
	for folder, names := range byFolder {
		name := filepath.Base(folder)
		if uses[name] > 1 {
			name = filepath.Base(filepath.Dir(folder)) + "/" + name
		}
		for _, test := range names {
			found[name+"."+test] = true
		}
	}
	return found
}

// expandSpans writes out each "X-1 through X-3" in text as its requirements,
// so that a range hides none of the IDs inside it.
func expandSpans(text string) string {
	return requirementSpan.ReplaceAllStringFunc(text, func(span string) string {
		match := requirementSpan.FindStringSubmatch(span)
		first, errFirst := strconv.Atoi(match[2])
		last, errLast := strconv.Atoi(match[4])
		if match[1] != match[3] || errFirst != nil || errLast != nil || last < first {
			return span
		}
		ids := make([]string, 0, last-first+1)
		for number := first; number <= last; number++ {
			ids = append(ids, fmt.Sprintf("%s%0*d", match[1], len(match[2]), number))
		}
		return strings.Join(ids, ", ")
	})
}

// TestTraceabilityCoversEveryRequirement holds docs/traceability.md to the
// PRD, the plan, and the tests, and keeps a requirement that still waits on a
// hardware run from being presented as complete.
func TestTraceabilityCoversEveryRequirement(t *testing.T) {
	prd := string(repositoryFile(t, "PRD.md"))
	want := map[string]string{}
	for _, match := range prdRequirement.FindAllStringSubmatch(prd, -1) {
		want[match[1]] = match[2]
	}
	for _, match := range prdScenario.FindAllStringSubmatch(prd, -1) {
		want[match[1]] = "—"
	}
	if len(want) == 0 {
		t.Fatal("found no requirements in the PRD")
	}

	plan := string(repositoryFile(t, "IMPLEMENTATION_PLAN.md"))
	items := map[string]bool{}
	for _, match := range planItem.FindAllStringSubmatch(plan, -1) {
		items[match[1]] = true
	}
	tests := repositoryTests(t)

	rows := readTraceability(t)
	seen := map[string]traceRow{}
	counts := map[string][2]int{}
	for _, row := range rows {
		if _, again := seen[row.id]; again {
			t.Errorf("%s is listed twice", row.id)
		}
		seen[row.id] = row
		priority, known := want[row.id]
		if !known {
			t.Errorf("%s is not in the PRD", row.id)
			continue
		}
		if row.priority != priority {
			t.Errorf("%s has priority %q, the PRD says %q", row.id, row.priority, priority)
		}
		kind := priority
		if kind == "—" {
			kind = "AC"
		}
		tally := counts[kind]
		switch row.status {
		case "Pass":
			tally[0]++
		case "**Partial**":
			tally[1]++
		default:
			t.Errorf("%s has status %q", row.id, row.status)
		}
		counts[kind] = tally
		if strings.Contains(row.open, "QLT-04A") {
			if row.status != "**Partial**" {
				t.Errorf("%s waits on QLT-04A but is not Partial", row.id)
			}
			if !slices.Contains(hardwareGated, row.id) {
				t.Errorf("%s waits on QLT-04A but is not in hardwareGated", row.id)
			}
		}
		if strings.Contains(row.open, "REL-02A") && row.status != "**Partial**" {
			t.Errorf("%s waits on the fix in REL-02A but is not Partial", row.id)
		}
		if row.status == "**Partial**" && row.open == "" {
			t.Errorf("%s is Partial but its Open cell does not say what remains", row.id)
		}
		if row.items == "" {
			t.Errorf("%s names no plan item", row.id)
		}
		for _, item := range strings.Split(row.items, ", ") {
			if !items[item] {
				t.Errorf("%s names plan item %q, which does not exist", row.id, item)
			}
		}
		if row.evidence == "" {
			t.Errorf("%s has no evidence", row.id)
		}
		for _, match := range evidenceToken.FindAllStringSubmatch(row.evidence, -1) {
			if !tests[match[1]] {
				t.Errorf("%s names %s, which does not exist", row.id, match[1])
			}
		}
	}
	for id := range want {
		if _, ok := seen[id]; !ok {
			t.Errorf("%s is missing from the matrix", id)
		}
	}

	matrix := string(repositoryFile(t, "docs", "traceability.md"))
	total := [2]int{}
	for _, summary := range []struct{ kind, label string }{{"P0", "P0 requirements"}, {"P1", "P1 requirements"}, {"AC", "Acceptance scenarios"}} {
		tally := counts[summary.kind]
		total[0] += tally[0]
		total[1] += tally[1]
		line := fmt.Sprintf("| %s | %d | %d | %d |", summary.label, tally[0]+tally[1], tally[0], tally[1])
		if !strings.Contains(matrix, line) {
			t.Errorf("the summary has no line %q", line)
		}
	}
	if line := fmt.Sprintf("| **All** | **%d** | **%d** | **%d** |", total[0]+total[1], total[0], total[1]); !strings.Contains(matrix, line) {
		t.Errorf("the summary has no line %q", line)
	}

	covers := manualCheckCoverage(t)
	recorded := recordedRuns(t, covers)
	for _, id := range hardwareGated {
		if seen[id].status != "**Partial**" && !recorded[id]["QLT-04A"] {
			t.Errorf("%s must stay Partial until docs/testing.md records a QLT-04A run that passed it", id)
		}
	}
	for id, checks := range manualGated {
		for _, check := range checks {
			if seen[id].status != "**Partial**" && !recorded[id][check] {
				t.Errorf("%s must stay Partial until docs/testing.md records a %s run that passed it", id, check)
			}
		}
	}
	required := regexp.MustCompile(`\*\*QLT-04A — [^\n]*\n[^\n]*\n\s+- \*\*Requirements:\*\* ([^\n]*)`).FindStringSubmatch(plan)
	if required == nil {
		t.Fatal("the plan lists no requirements for QLT-04A")
	}
	for _, id := range requirementID.FindAllString(expandSpans(required[1]), -1) {
		if !slices.Contains(hardwareGated, id) {
			t.Errorf("QLT-04A requires %s, which is not in hardwareGated", id)
		}
	}

	checkBlockerLists(t, matrix, rows)
	checkManualChecks(t, rows, covers, recorded)
}

// checkBlockerLists holds the matrix's named lists of Partial requirements to
// its rows, in row order. An empty list is written as None, as it is once the
// last blocker has passed.
func checkBlockerLists(t *testing.T, matrix string, rows []traceRow) {
	t.Helper()
	var blockers, deferrable []string
	for _, row := range rows {
		switch {
		case row.status != "**Partial**":
		case row.priority == "P1":
			deferrable = append(deferrable, row.id)
		default:
			blockers = append(blockers, row.id)
		}
	}
	listed := func(ids []string, none string) string {
		if len(ids) == 0 {
			return none
		}
		return strings.Join(ids, ", ")
	}
	if line := "\n" + listed(blockers, "None") + ".\n"; !strings.Contains(matrix, line) {
		t.Errorf("the list of release blockers is not %q", strings.TrimSpace(line))
	}
	if line := "The Partial P1 requirements are " + listed(deferrable, "none") + "."; !strings.Contains(matrix, line) {
		t.Errorf("the summary has no line %q", line)
	}
}

// manualCheckCoverage reads which requirements each manual check in
// docs/testing.md covers.
func manualCheckCoverage(t *testing.T) map[string][]string {
	t.Helper()
	covers := map[string][]string{}
	for _, line := range strings.Split(string(repositoryFile(t, "docs", "testing.md")), "\n") {
		match := manualCheckRow.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		cells := strings.Split(strings.Trim(strings.TrimSpace(line), "|"), "|")
		if len(cells) != 4 {
			t.Fatalf("%s has %d cells, want 4", match[1], len(cells))
		}
		ids := requirementID.FindAllString(expandSpans(cells[3]), -1)
		slices.Sort(ids)
		covers[match[1]] = ids
	}
	if len(covers) == 0 {
		t.Fatal("found no manual checks in docs/testing.md")
	}
	return covers
}

// recordedRuns reads every line of docs/testing.md that has the form of a
// Recorded runs row, wherever it is, into, for each requirement, the runs that
// passed it. A run can pass only what it covers,
// and its Passed cell holds requirement IDs and nothing else, so that a note
// such as "SCR-021 failed" cannot read as a pass.
func recordedRuns(t *testing.T, covers map[string][]string) map[string]map[string]bool {
	t.Helper()
	recorded := map[string]map[string]bool{}
	for _, line := range strings.Split(string(repositoryFile(t, "docs", "testing.md")), "\n") {
		match := recordedRun.FindStringSubmatch(strings.TrimSpace(line))
		if match == nil {
			continue
		}
		run := match[1]
		// A check that covers no requirement leaves Passed empty, and a
		// formatter may pad that cell with spaces.
		if strings.TrimSpace(match[2]) == "" {
			continue
		}
		for _, id := range strings.Split(expandSpans(match[2]), ",") {
			id = strings.TrimSpace(id)
			if id == "" || requirementID.FindString(id) != id {
				t.Errorf("docs/testing.md records %s as passing %q, which is not a requirement ID", run, id)
				continue
			}
			if run == "QLT-04A" && !slices.Contains(hardwareGated, id) || run != "QLT-04A" && !slices.Contains(covers[run], id) {
				t.Errorf("docs/testing.md records %s as passing %s, which it does not cover", run, id)
				continue
			}
			if recorded[id] == nil {
				recorded[id] = map[string]bool{}
			}
			recorded[id][run] = true
		}
	}
	return recorded
}

// checkManualChecks holds the matrix's Open column and the guide's table of
// manual checks to each other, and keeps a row that names a gating check
// under that check's guard. Open names a check only until a recorded run has
// passed that row, as the matrix's legend says an entry there is not done.
func checkManualChecks(t *testing.T, rows []traceRow, covers map[string][]string, recorded map[string]map[string]bool) {
	t.Helper()
	named := map[string][]string{}
	for _, row := range rows {
		for _, check := range manualCheck.FindAllString(row.open, -1) {
			if _, ok := covers[check]; !ok {
				t.Errorf("%s names %s, which docs/testing.md does not define", row.id, check)
			}
			named[check] = append(named[check], row.id)
			if slices.Contains(gatingChecks, check) && !slices.Contains(manualGated[row.id], check) && confidenceOnly[row.id] != check {
				t.Errorf("%s names %s, which only a real run can satisfy, but is not in manualGated for it", row.id, check)
			}
		}
	}
	for check, ids := range covers {
		open := slices.DeleteFunc(slices.Clone(ids), func(id string) bool { return recorded[id][check] })
		got := named[check]
		slices.Sort(got)
		if !slices.Equal(got, open) {
			t.Errorf("%s has not passed %v according to docs/testing.md, but the matrix's Open column names it for %v", check, open, got)
		}
	}
}

// label reduces a control's text to the words a document names it by: no
// access-key marker, ellipsis, or parenthetical hint, but its pixel unit.
func label(text string) string {
	text = strings.NewReplacer("&", "", "…", "").Replace(text)
	if at := strings.Index(text, " ("); at >= 0 && !strings.HasSuffix(text, "(px)") {
		text = text[:at]
	}
	return text
}

// takeLabel reports whether text names the label as whole words, and returns
// text without those mentions, so that a shorter label cannot be satisfied by
// a longer one that contains it, as Capture is by Start Capture.
func takeLabel(text, name string) (string, bool) {
	isWord := func(b byte) bool {
		return b == '_' || b >= '0' && b <= '9' || b >= 'A' && b <= 'Z' || b >= 'a' && b <= 'z'
	}
	var kept strings.Builder
	last, found := 0, false
	for start := 0; ; {
		at := strings.Index(text[start:], name)
		if at < 0 {
			break
		}
		at += start
		end := at + len(name)
		start = end
		if (at > 0 && isWord(text[at-1])) || (end < len(text) && isWord(text[end])) {
			continue
		}
		kept.WriteString(text[last:at])
		kept.WriteByte(0)
		last, found = end, true
	}
	kept.WriteString(text[last:])
	return kept.String(), found
}

// TestUserGuideNamesTheApplicationsControls fails when a control or status is
// renamed and the packaged guide still calls it by its old name.
func TestUserGuideNamesTheApplicationsControls(t *testing.T) {
	guide := strings.Join(strings.Fields(string(repositoryFile(t, "packaging", "USER-GUIDE.txt"))), " ")
	texts := []string{
		ui.ScreenshotNavigation, ui.ImageResizerNavigation, ui.SettingsNavigation,
		// Capture is also a heading, so only the instruction to press it counts.
		ui.StartCaptureAction, "Press " + ui.CaptureAction, ui.CancelAction, ui.OpenFolderAction,
		ui.CaptureModeLabel, ui.OutputFormatLabel, ui.BrowseFolderAction,
		ui.CaptureModeFreeRectangle, ui.CaptureModePresetRectangle, ui.CaptureModeFullDisplay,
		ui.DisplayLabel, ui.GlobalShortcutLabel, ui.OutputFolderLabel, ui.JPEGQualityLabel,
		ui.SelectionWidthLabel, ui.SelectionHeightLabel,
		ui.SourceImagesLabel, ui.SourcePathLabel,
		ui.PresetNameLabel, ui.PresetWidthLabel, ui.PresetHeightLabel, ui.PresetResizeModeLabel,
		ui.AddImagesAction, ui.RemoveSelectedAction, ui.ClearListAction,
		ui.StartResizeAction, ui.CancelResizeAction, ui.OpenOutputFolderAction,
		ui.SourceStatusReady, ui.SourceStatusChecking, ui.SourceStatusUnsupported,
		ui.SourceStatusCorrupt, ui.SourceStatusInaccessible, ui.SourceStatusTooLarge,
		ui.ResizeModeFit, ui.ResizeModeStretch, ui.ResizeModeCropToFill,
		ui.ResizeFormatLabel, ui.ResizeFormatKeepSource, ui.ResizeBackgroundLabel,
		ui.ResizeCollisionLabel, ui.ResizeCollisionRename, ui.ResizeCollisionSkip, ui.ResizeCollisionOverwrite,
		ui.ResizeStatePending, ui.ResizeStateProcessing, ui.ResizeStateCompleted,
		ui.ResizeStateSkipped, ui.ResizeStateFailed, ui.ResizeStateCancelled,
		ui.PresetLibraryLabel, ui.PresetCreateAction, ui.PresetRenameAction,
		ui.PresetDuplicateAction, ui.PresetDeleteAction,
		ui.RestoreDefaultsAction, ui.ResizeModeSummaryLabel,
		ui.ErrorWriteEncodeLabel, ui.ErrorShortcutRegistrationLabel,
		// The troubleshooting section quotes these messages by how they begin.
		strings.Split(ui.CaptureGraphicsFailed, ":")[0],
		strings.Split(ui.CaptureDisplayChanged, " or ")[0],
		strings.Split(ui.OutputFolderNameInvalid, ":")[0],
		strings.Split(ui.DisplayUnavailable, ":")[0],
		screenshot.DefaultShortcut().String(),
	}
	names := make([]string, 0, len(texts))
	for _, text := range texts {
		names = append(names, label(text))
	}
	// Longest first, so each label's mentions are taken before those of any
	// shorter label inside it.
	slices.SortFunc(names, func(a, b string) int {
		if len(a) != len(b) {
			return len(b) - len(a)
		}
		return strings.Compare(a, b)
	})
	for _, name := range slices.Compact(names) {
		if name == "" {
			t.Error("a control's text reduces to an empty label")
			continue
		}
		var found bool
		if guide, found = takeLabel(guide, name); !found {
			t.Errorf("USER-GUIDE.txt does not mention %q on its own", name)
		}
	}
}

// TestPackagedDocumentsAgree holds what ships beside the executable together:
// the package check expects each document, the README points to the guide and
// the notes, the notes name this version, and the text reads in any viewer.
func TestPackagedDocumentsAgree(t *testing.T) {
	verify := string(repositoryFile(t, "scripts", "verify-package.ps1"))
	for _, name := range PackagedDocuments {
		data := repositoryFile(t, "packaging", name)
		if !strings.Contains(verify, `"`+name+`"`) {
			t.Errorf("verify-package.ps1 does not expect %s", name)
		}
		for offset, b := range data {
			if b >= 0x80 {
				t.Errorf("%s has a non-ASCII byte at offset %d", name, offset)
				break
			}
		}
	}
	readme := string(repositoryFile(t, "packaging", "README.txt"))
	for _, name := range []string{"USER-GUIDE.txt", "RELEASE-NOTES.txt"} {
		if !strings.Contains(readme, name) {
			t.Errorf("README.txt does not mention %s", name)
		}
	}
	notes := string(repositoryFile(t, "packaging", "RELEASE-NOTES.txt"))
	if !strings.Contains(notes, "Version "+Version) {
		t.Errorf("RELEASE-NOTES.txt does not name version %s", Version)
	}
}
