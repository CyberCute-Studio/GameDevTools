package job

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"slices"
	"sync"

	"github.com/CyberCute-Studio/GameDevTools/internal/imageresizer/decode"
	"github.com/CyberCute-Studio/GameDevTools/internal/imageresizer/output"
	"github.com/CyberCute-Studio/GameDevTools/internal/imageresizer/resize"
)

// maxWorkers bounds how many items a job processes at once (RSZ-050,
// TECH-006). Each item decodes, resizes, and encodes on one goroutine, so
// items running side by side are what spreads a job across processors;
// beyond a few, the drive and the memory budget limit throughput more than
// the processors do.
const maxWorkers = 4

// memoryBudget bounds the pixel memory the items in flight may need at once,
// by each item's estimate (NFR-014). An item whose estimate alone exceeds it
// starts only when no other item is in flight, so the memory a job holds
// never exceeds the larger of the budget and the most a single item needs.
const memoryBudget = 1 << 30

// maxReplans bounds how often one item is planned again because another
// program took its destination after planning, so a folder where names keep
// being taken cannot keep the item going round forever.
const maxReplans = 3

// ErrUnexpected is the cause of an item's failure that no step reported as
// its own, such as a panic outside the steps that contain their own
// (NFR-021).
var ErrUnexpected = errors.New("unexpected failure while processing the item")

// State is where one item of a job stands (RSZ-043). Every item starts
// Pending and ends Completed, Skipped, Failed, or Cancelled; an item the job
// resizes is Processing in between.
type State uint8

const (
	// StatePending means the item has not started: it waits for a worker,
	// for the memory budget, or for the item it defers to.
	StatePending State = iota
	// StateProcessing means the item's source is being decoded, resized,
	// encoded, or saved.
	StateProcessing
	// StateCompleted means the item's result was saved at its Destination.
	StateCompleted
	// StateSkipped means the item's destination was left untouched under
	// the Skip policy, because a file already had its name.
	StateSkipped
	// StateFailed means the item could not be resized or saved; Err says
	// why.
	StateFailed
	// StateCancelled means the job was cancelled before the item finished,
	// and nothing was saved for it.
	StateCancelled
)

func (s State) String() string {
	switch s {
	case StatePending:
		return "pending"
	case StateProcessing:
		return "processing"
	case StateCompleted:
		return "completed"
	case StateSkipped:
		return "skipped"
	case StateFailed:
		return "failed"
	case StateCancelled:
		return "cancelled"
	}
	return fmt.Sprintf("State(%d)", int(s))
}

// Final reports whether an item in this state has ended: Completed,
// Skipped, Failed, or Cancelled.
func (s State) Final() bool {
	return s >= StateCompleted && s <= StateCancelled
}

// Input is one source listed for a job, in the source list's order.
type Input struct {
	// Path is the absolute source path, as listed.
	Path string
	// Info describes the source's content as its content check decoded it:
	// the format Keep Source Format encodes it in, and the upright
	// dimensions and decoded memory the job sizes its work by. It is used
	// only when Err is nil.
	Info decode.Info
	// Err is why the source cannot be resized, as the source list found
	// when it checked the file: it is unsupported, corrupt, too large, or
	// inaccessible. Such an item ends Failed with Err as its error (AC-R06),
	// and its path is protected like every source's, so no result is ever
	// written over it (RSZ-026).
	Err error
}

// Request describes one job (RSZ-040).
type Request struct {
	// Inputs lists the job's sources in order; that order decides which
	// of two sources with the same destination keeps the name, and the
	// order items start in.
	Inputs []Input
	// Folder is the absolute path of the output folder.
	Folder string
	// Format is the output format option.
	Format output.Format
	// Collision is the collision policy.
	Collision output.Collision
	// ConfirmOverwrite records the user's explicit confirmation, given for
	// this job, that existing destination files may be replaced.
	ConfirmOverwrite bool
	// Settings are the preset and the JPEG options applied to every item.
	Settings Settings
}

// Status is one item's state, with what the job knows of it.
type Status struct {
	State State
	// Destination is the file a Processing item writes or a Completed item
	// saved; the destination a Skipped item left untouched; the destination a
	// Cancelled or Failed item was planned to save, which is empty for an
	// item the job could not plan, such as a source that cannot be resized.
	Destination string
	// Reason is why the item did not get its natural destination, as
	// output.Reason describes: why a Completed item was saved under a
	// numbered name, or why an item was Skipped. It is output.ReasonNone
	// otherwise.
	Reason output.Reason
	// Err is a Failed item's error: the failing step's own typed error, as
	// Process returns it, the plan's error for an item it could not name,
	// or the Input's Err. A Completed item carries one only when its result
	// was saved but a file the error names was left behind, as output.Write
	// describes. It is nil otherwise.
	Err error
}

// Progress counts a job's items by state (RSZ-042, RSZ-046).
type Progress struct {
	Total      int
	Pending    int
	Processing int
	Completed  int
	Skipped    int
	Failed     int
	Cancelled  int
}

// Done is the number of items that have ended.
func (p Progress) Done() int {
	return p.Completed + p.Skipped + p.Failed + p.Cancelled
}

func (p *Progress) count(state State, delta int) {
	switch state {
	case StatePending:
		p.Pending += delta
	case StateProcessing:
		p.Processing += delta
	case StateCompleted:
		p.Completed += delta
	case StateSkipped:
		p.Skipped += delta
	case StateFailed:
		p.Failed += delta
	case StateCancelled:
		p.Cancelled += delta
	}
}

// Event reports that one item entered a state. It holds values only, so it
// can be handed to another goroutine, such as the GUI thread, as it is.
type Event struct {
	// Index is the item's index in Request.Inputs.
	Index int
	// Source is the item's source path: the file being processed when the
	// state is Processing (RSZ-042).
	Source string
	Status Status
	// Progress counts the items in every state once this change is made.
	Progress Progress
}

// Summary is a finished job's outcome (RSZ-046).
type Summary struct {
	// Folder is the output folder as the plan cleaned it, or empty when the
	// job was cancelled before its destinations were planned.
	Folder string
	// Items holds every input's final status, at its index in
	// Request.Inputs.
	Items []Status
	// Progress counts the final states; Pending and Processing are zero.
	Progress Progress
}

// config is how a job runs; tests substitute its parts.
type config struct {
	workers int
	budget  uint64
	prepare func(context.Context, output.Request) (output.Plan, error)
	process func(context.Context, output.Item, Settings) (string, error)
}

func defaultConfig() config {
	return config{
		workers: defaultWorkers(),
		budget:  memoryBudget,
		prepare: output.Prepare,
		process: Process,
	}
}

// defaultWorkers leaves one processor to the GUI thread and the rest of the
// application, so the window stays responsive while a job runs (RSZ-041),
// and never runs more than maxWorkers items at once.
func defaultWorkers() int {
	return max(1, min(runtime.GOMAXPROCS(0)-1, maxWorkers))
}

// Run runs a job to its end and returns every item's outcome. It blocks, so
// callers run it off the GUI thread (TECH-005).
//
// Run first checks what the job applies to every item (RSZ-040): the preset,
// and the JPEG quality and background when any result is encoded as JPEG;
// output.Prepare then checks the output folder, format, and collision
// policy, the Overwrite confirmation, and that at least one input can be
// resized, creates the output folder, and plans every destination as the
// filesystem stands now, so re-running a job plans around the results of
// earlier runs rather than assuming them (RSZ-049). A failure of any of
// these fails the whole job: Run returns the step's own typed error — a
// *resize.Error, an *encode.Error, or an *output.Error — with an empty
// Summary, having processed nothing and reported no event.
//
// Every listed input is then an item. An input that cannot be resized ends
// Failed with its Err, and so does an item the plan could not name; an item
// whose destination exists ends Skipped under the Skip policy. The rest are
// processed in order by Process, a few at a time: at most one fewer than the
// processors the program may use, at most four, and only while the memory
// the items in flight need by their estimates — the decoded source, as its
// Input.Info reports it, the resized image, and the filter's working memory
// — stays within a budget of 1 GiB (RSZ-050, NFR-014); an item that needs
// more starts only when it would run alone. So the job never decodes every
// source at once, and its pixel memory stays bounded; the garbage collector
// reclaims what finished items drop at its own pace. A source that changed
// since it was checked is decoded as it now is, so one that has gone, or
// whose content is damaged now, fails then (RSZ-049).
//
// One item's failure never stops the others (RSZ-044): each failure is
// attached to its item as the failing step's own typed error, and a panic
// that escapes Process is contained as that item's ErrUnexpected failure
// (NFR-021). Under Skip, two sources with one destination are planned so
// that only the first writes it (output.ActionDefer): when that item fails,
// the next one is processed in its place, as output.Plan.Successor
// describes, and once the file is written the rest end Skipped, as they do
// when another program wrote a file at the name after planning (AC-R09).
// Under Rename and Overwrite, an item whose destination another program took
// after planning is planned again and saved under the first unused numbered
// name instead, never replacing that file: Overwrite replaces only the files
// that existed when the job was planned (RSZ-025, AC-R10).
//
// Cancelling the context stops the job from starting further items, and the
// items in flight stop at the next point at which Process observes it,
// before their results take their names, so no partial file is left; an
// item already renaming its result finishes and ends Completed. Items that
// did not finish end Cancelled, completed results are kept, and Run returns
// once no item is in flight (RSZ-045). A cancelled job is not a failure:
// Run returns its Summary and a nil error. A context cancelled before Run
// starts, or while the destinations are planned, cancels every item that
// could be resized.
//
// observe, which may be nil, is told of every change of an item's state:
// each item that is processed becomes Processing and then ends, while one
// that is not ends directly, and an item ends exactly once; an item planned
// again under a new name is reported Processing again with its new
// destination. It is called on
// the goroutine that called Run, one event at a time and in order, never
// concurrently, and no item starts or ends while it runs, so it should only
// hand the event on — a view queues it and posts it to the GUI thread
// through the shell dispatcher (TECH-005). Each event carries the counts of
// every state, so a consumer that falls behind can apply the latest event
// of each item and the latest counts alone.
func Run(ctx context.Context, request Request, observe func(Event)) (Summary, error) {
	return run(ctx, request, observe, defaultConfig())
}

func run(ctx context.Context, request Request, observe func(Event), cfg config) (Summary, error) {
	b := newBatch(request, observe, cfg)
	if ctx.Err() != nil {
		b.cancelAll()
		return b.summary(), nil
	}
	if err := b.preflight(); err != nil {
		return Summary{}, err
	}
	plan, err := cfg.prepare(ctx, b.planRequest())
	if err != nil {
		if canceled(ctx, err) {
			b.cancelAll()
			return b.summary(), nil
		}
		return Summary{}, err
	}
	// Run never returns while an item is in flight, even when observe
	// panics: the items are cancelled and waited for first.
	defer b.workers.Wait()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	b.execute(ctx, plan)
	return b.summary(), nil
}

// canceled reports whether err is the context's own cancellation.
func canceled(ctx context.Context, err error) bool {
	ctxErr := ctx.Err()
	return ctxErr != nil && errors.Is(err, ctxErr)
}

// finished is one processed item's result, sent from its worker goroutine.
type finished struct {
	plan int
	path string
	err  error
	cost uint64
}

// batch is one running job. Only the goroutine that runs it touches its
// fields; worker goroutines report through done.
type batch struct {
	request Request
	observe func(Event)
	cfg     config
	ctx     context.Context

	// planned maps each input to its plan item's index, or -1 for an input
	// that cannot be resized; inputs maps each plan item back to its input.
	planned []int
	inputs  []int
	plan    output.Plan

	statuses []Status
	progress Progress

	// queue holds the plan indices of the items waiting to start, in order.
	queue []int
	// replans counts, by plan index, how often an item was planned again.
	replans map[int]int
	// inflight and reserved are the items being processed and the memory
	// their estimates add up to.
	inflight int
	reserved uint64
	done     chan finished
	workers  sync.WaitGroup
}

func newBatch(request Request, observe func(Event), cfg config) *batch {
	cfg.workers = max(1, cfg.workers)
	b := &batch{
		request:  request,
		observe:  observe,
		cfg:      cfg,
		planned:  make([]int, len(request.Inputs)),
		statuses: make([]Status, len(request.Inputs)),
		progress: Progress{Total: len(request.Inputs), Pending: len(request.Inputs)},
		done:     make(chan finished, cfg.workers),
	}
	for index, input := range request.Inputs {
		b.planned[index] = -1
		if input.Err == nil {
			b.planned[index] = len(b.inputs)
			b.inputs = append(b.inputs, index)
		}
	}
	return b
}

// preflight checks what the job applies to every item before anything is
// planned or created. output.Prepare checks the rest of the request.
func (b *batch) preflight() error {
	settings := b.request.Settings
	if err := settings.Target.Validate(); err != nil {
		return err
	}
	if !b.request.Format.Valid() {
		// output.Prepare reports the unknown format.
		return nil
	}
	jpegResults := false
	for _, index := range b.inputs {
		if output.EncodedFormat(b.request.Format, b.request.Inputs[index].Info.Format) == decode.FormatJPEG {
			jpegResults = true
			break
		}
	}
	// A PNG result ignores the JPEG options, as Process does.
	if jpegResults {
		return settings.encodeOptions(decode.FormatJPEG).Validate()
	}
	return nil
}

// planRequest is the output.Request for the job: the inputs that can be
// resized are its sources, and the others are protected.
func (b *batch) planRequest() output.Request {
	request := output.Request{
		Sources:          make([]output.Source, 0, len(b.inputs)),
		Folder:           b.request.Folder,
		Format:           b.request.Format,
		Collision:        b.request.Collision,
		ConfirmOverwrite: b.request.ConfirmOverwrite,
	}
	for index, input := range b.request.Inputs {
		if b.planned[index] < 0 {
			request.Protected = append(request.Protected, input.Path)
			continue
		}
		request.Sources = append(request.Sources, output.Source{Path: input.Path, Format: input.Info.Format})
	}
	return request
}

// cancelAll ends a job cancelled before its destinations were planned:
// every input that could be resized is Cancelled, and every other Failed.
func (b *batch) cancelAll() {
	for index, input := range b.request.Inputs {
		if b.planned[index] < 0 {
			b.set(index, Status{State: StateFailed, Err: input.Err})
			continue
		}
		b.set(index, Status{State: StateCancelled})
	}
}

// execute ends the items the plan settles, then processes the rest until
// none is waiting or in flight.
func (b *batch) execute(ctx context.Context, plan output.Plan) {
	b.ctx, b.plan = ctx, plan
	for index, input := range b.request.Inputs {
		p := b.planned[index]
		if p < 0 {
			b.set(index, Status{State: StateFailed, Err: input.Err})
			continue
		}
		item := plan.Items[p]
		switch item.Action {
		case output.ActionCreate, output.ActionReplace:
			b.enqueue(p)
		case output.ActionSkip:
			b.end(p, Status{State: StateSkipped, Destination: item.Destination, Reason: item.Reason})
		case output.ActionFail:
			b.end(p, Status{State: StateFailed, Destination: item.Destination, Err: item.Err})
		case output.ActionDefer:
			// The item waits for the one it defers to.
		default:
			b.end(p, Status{State: StateFailed, Destination: item.Destination, Err: fmt.Errorf("%w: the plan gave the item the action %v", ErrUnexpected, item.Action)})
		}
	}
	stop := ctx.Done()
	for {
		b.start()
		// Checked after start, so an item left waiting because the job was
		// cancelled while it started the others is cancelled too.
		if ctx.Err() != nil {
			b.cancelQueued()
		}
		if b.inflight == 0 {
			return
		}
		select {
		case result := <-b.done:
			if b.restart(&result) {
				continue
			}
			b.inflight--
			b.reserved -= result.cost
			b.end(result.plan, b.outcome(result))
		case <-stop:
			// The next pass cancels the waiting items; the ones in flight
			// observe the context themselves.
			stop = nil
		}
	}
}

// start starts waiting items in order while a worker is free and the next
// item's memory estimate fits the budget, or nothing is in flight. With
// nothing in flight it always starts one, so a waiting item never waits
// forever.
func (b *batch) start() {
	for len(b.queue) > 0 && b.inflight < b.cfg.workers && b.ctx.Err() == nil {
		p := b.queue[0]
		cost := b.cost(p)
		if b.inflight > 0 && b.reserved+cost > b.cfg.budget {
			return
		}
		b.queue = b.queue[1:]
		b.inflight++
		b.reserved += cost
		item := b.plan.Items[p]
		b.set(b.inputs[p], Status{State: StateProcessing, Destination: item.Destination, Reason: item.Reason})
		b.workers.Add(1)
		go b.work(p, item, cost)
	}
}

// work processes one item on its own goroutine. done has room for every
// item in flight, so the send never blocks.
func (b *batch) work(p int, item output.Item, cost uint64) {
	defer b.workers.Done()
	path, err := b.process(item)
	b.done <- finished{plan: p, path: path, err: err, cost: cost}
}

// process runs Process, containing a panic that escapes it as the item's
// failure: a panic on a worker goroutine would end the whole program, not
// one item (NFR-021). The steps contain their own panics; this guard is for
// whatever escapes them.
func (b *batch) process(item output.Item) (path string, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			path, err = "", fmt.Errorf("%w: %v", ErrUnexpected, recovered)
			if panicErr, ok := recovered.(error); ok {
				err = fmt.Errorf("%w: %w", ErrUnexpected, panicErr)
			}
		}
	}()
	return b.cfg.process(b.ctx, item, b.request.Settings)
}

// restart plans an item again whose destination another program took after
// planning, and processes it again under the name the new plan gives it, in
// the worker slot and memory it already holds. Under Rename the result takes
// the first unused numbered name, as Rename does for a name taken at
// planning (RSZ-025). Under Overwrite it does too: the user confirmed
// replacing the files that existed when the job was planned, and a file
// written since is none of them (AC-R10). Under Skip the existing file is
// left untouched and the item ends Skipped instead, as outcome reports. It
// reports false when the item is not restarted: it saved its result, failed
// otherwise, was cancelled, or was planned again maxReplans times already;
// when planning it again failed, result then carries that failure.
func (b *batch) restart(result *finished) bool {
	if result.path != "" || !errors.Is(result.err, output.ErrDestinationTaken) ||
		b.request.Collision == output.CollisionSkip || b.ctx.Err() != nil || b.replans[result.plan] >= maxReplans {
		return false
	}
	if b.replans == nil {
		b.replans = make(map[int]int)
	}
	b.replans[result.plan]++
	item, err := b.replan(result.plan)
	if err != nil {
		result.err = err
		return false
	}
	b.plan.Items[result.plan] = item
	if !item.Writes() {
		result.err = item.Err
		return false
	}
	b.set(b.inputs[result.plan], Status{State: StateProcessing, Destination: item.Destination, Reason: item.Reason})
	b.workers.Add(1)
	go b.work(result.plan, item, result.cost)
	return true
}

// replan plans the item at p again, under Rename, as a job of its own source
// whose protected paths are every other listed path and every other item's
// destination, so its new name is none of theirs. Under Rename nothing is
// replaced, so planning reads no file identities, and it runs on the
// coordinating goroutine, which keeps the destinations it avoids current.
func (b *batch) replan(p int) (output.Item, error) {
	index := b.inputs[p]
	input := b.request.Inputs[index]
	request := output.Request{
		Sources:   []output.Source{{Path: input.Path, Format: input.Info.Format}},
		Protected: make([]string, 0, len(b.request.Inputs)+len(b.plan.Items)),
		Folder:    b.plan.Folder,
		Format:    b.request.Format,
		Collision: output.CollisionRename,
	}
	for other, listed := range b.request.Inputs {
		if other != index {
			request.Protected = append(request.Protected, listed.Path)
		}
	}
	for other, item := range b.plan.Items {
		if other != p {
			request.Protected = append(request.Protected, item.Destination)
		}
	}
	plan, err := b.cfg.prepare(b.ctx, request)
	if err != nil {
		return output.Item{}, err
	}
	return plan.Items[0], nil
}

// cost is the memory an item may need while it is processed: its decoded
// source, by the estimate its content check made, or the decoder's limit
// when there is none; the resized image; and the filter's working memory.
// The encoder works in the resized image's memory or streams from it.
func (b *batch) cost(p int) uint64 {
	info := b.request.Inputs[b.inputs[p]].Info
	decoded := info.DecodedBytes
	if decoded == 0 || decoded > decode.MaxDecodedBytes {
		decoded = decode.MaxDecodedBytes
	}
	return decoded + resultBytes(info.Format, b.request.Settings.Target) + resize.MaxWorkingBytes
}

// resultBytes bounds the resized image's memory. Every fitting mode gives at
// most the target's pixels. A JPEG source resizes to at most 4 bytes a
// pixel, 8-bit color, while a PNG source may keep 16 bits per channel with
// alpha, 8 bytes a pixel, as resize.Resize describes.
func resultBytes(format decode.Format, target resize.Target) uint64 {
	perPixel := uint64(8)
	if format == decode.FormatJPEG {
		perPixel = 4
	}
	return perPixel * uint64(target.Width) * uint64(target.Height)
}

// outcome is the status a processed item ends in. A saved result is
// Completed whatever else happened. A failure the job's cancellation caused
// is a cancellation. Under Skip, a destination another program took after
// planning is an existing destination, which Skip leaves untouched.
func (b *batch) outcome(result finished) Status {
	item := b.plan.Items[result.plan]
	switch {
	case result.err == nil || result.path != "":
		destination := result.path
		if destination == "" {
			destination = item.Destination
		}
		return Status{State: StateCompleted, Destination: destination, Reason: item.Reason, Err: result.err}
	case canceled(b.ctx, result.err):
		return Status{State: StateCancelled, Destination: item.Destination, Reason: item.Reason}
	case b.request.Collision == output.CollisionSkip && errors.Is(result.err, output.ErrDestinationTaken):
		return Status{State: StateSkipped, Destination: item.Destination, Reason: output.ReasonExists}
	}
	return Status{State: StateFailed, Destination: item.Destination, Reason: item.Reason, Err: result.err}
}

// end ends the plan item at p, and settles the items deferred to the
// destination it was to create, as output.Plan.Successor describes: once it
// is written, or a file another program wrote has the name, they end
// Skipped; when the item fails without writing it, the next of them is
// processed in its place, unless the job is cancelled; and when the item is
// cancelled, so are they.
func (b *batch) end(p int, status Status) {
	b.set(b.inputs[p], status)
	next, ok := b.plan.Successor(p)
	if !ok {
		return
	}
	switch status.State {
	case StateCompleted:
		b.endDeferred(next, Status{State: StateSkipped, Reason: output.ReasonDuplicate})
	case StateSkipped:
		b.endDeferred(next, Status{State: StateSkipped, Reason: output.ReasonExists})
	case StateFailed:
		if b.ctx.Err() == nil {
			b.promote(next)
			return
		}
		b.endDeferred(next, Status{State: StateCancelled})
	default:
		b.endDeferred(next, Status{State: StateCancelled})
	}
}

// endDeferred ends the deferred item at p and every item deferred after it
// to the same destination, each with its own destination.
func (b *batch) endDeferred(p int, status Status) {
	for ok := true; ok; p, ok = b.plan.Successor(p) {
		status.Destination = b.plan.Items[p].Destination
		b.set(b.inputs[p], status)
	}
}

// promote gives the deferred item at p its turn to create the destination
// the item it deferred to did not write: it is processed as an ActionCreate
// item at its natural destination, which it now saves under its own name.
func (b *batch) promote(p int) {
	item := &b.plan.Items[p]
	item.Action, item.Reason, item.DuplicateOf = output.ActionCreate, output.ReasonNone, -1
	b.enqueue(p)
}

// enqueue adds the plan item at p to the waiting items, keeping them in
// order.
func (b *batch) enqueue(p int) {
	at, _ := slices.BinarySearch(b.queue, p)
	b.queue = slices.Insert(b.queue, at, p)
}

// cancelQueued ends every waiting item Cancelled, and with them the items
// deferred to them.
func (b *batch) cancelQueued() {
	queued := b.queue
	b.queue = nil
	for _, p := range queued {
		item := b.plan.Items[p]
		b.end(p, Status{State: StateCancelled, Destination: item.Destination, Reason: item.Reason})
	}
}

// set records an item's new status and reports it.
func (b *batch) set(index int, status Status) {
	b.progress.count(b.statuses[index].State, -1)
	b.progress.count(status.State, 1)
	b.statuses[index] = status
	if b.observe != nil {
		b.observe(Event{Index: index, Source: b.request.Inputs[index].Path, Status: status, Progress: b.progress})
	}
}

func (b *batch) summary() Summary {
	return Summary{Folder: b.plan.Folder, Items: b.statuses, Progress: b.progress}
}
