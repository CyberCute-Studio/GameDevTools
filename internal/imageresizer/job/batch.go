package job

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"runtime/metrics"
	"slices"
	"sync"
	"sync/atomic"

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

// memoryBudget bounds the memory the items in flight may need at once, by
// each item's estimate (NFR-014). An item whose estimate alone exceeds it
// starts only when no other item is in flight. A source is decoded only
// within its estimate and resized only while the whole item fits its share,
// and the memory items drop is collected before their share of the budget
// is handed on once they allocated collectThreshold, as Run describes, so by
// the estimates the budget bounds the memory items hold, garbage included,
// rather than only the memory they keep reachable, but for less than
// collectThreshold that small items may leave to the collector.
const memoryBudget = 1 << 30

// collectThreshold is the memory the heap must have allocated while an item
// was processed, or while the items that ended since the last collection
// were, together, for the worker that ends one to collect garbage before it
// reports the item done.
const collectThreshold = 64 << 20

// bufferBytes is what an item may allocate beyond the pixel memory itemBytes
// counts from the estimates: the PNG decoder's row buffers, up to the 16 MiB
// its estimate leaves out, as decode.MaxDecodedBytes describes, and the
// encoder's compressor state and rows and the write buffer, a few MiB at the
// largest target.
const bufferBytes = 20 << 20

// maxReplans bounds how often one item is planned again because another
// program took its destination after planning, so a folder where names keep
// being taken cannot keep the item going round forever.
const maxReplans = 3

var (
	// ErrUnexpected is the cause of an item's failure that no step reported
	// as its own, such as a panic outside the steps that contain their own
	// (NFR-021).
	ErrUnexpected = errors.New("unexpected failure while processing the item")
	// ErrUnchecked is the error of an item whose Input has no Err but no
	// content format the job resizes either, as a source listed before its
	// content check finished has.
	ErrUnchecked = errors.New("the source's content has not been checked")
)

// State is where one item of a job stands (RSZ-043). Every item starts
// Pending and ends Completed, Skipped, Failed, or Cancelled; an item the job
// resizes is Processing in between, and Pending again while it waits to
// start over within more memory, as Run describes.
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
	// and so does one whose Err is nil but whose Info has no format the job
	// resizes, with ErrUnchecked. Either path is protected like every
	// source's, so no result is ever written over it (RSZ-026).
	Err error
}

// failure is why the input cannot be resized, or nil: its Err, or
// ErrUnchecked when its content check gave it no PNG or JPEG format.
func (in Input) failure() error {
	switch {
	case in.Err != nil:
		return in.Err
	case in.Info.Format != decode.FormatPNG && in.Info.Format != decode.FormatJPEG:
		return ErrUnchecked
	}
	return nil
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
	// saved; the destination a Pending item that started already waits to
	// write; the destination a Skipped item left untouched; the destination
	// a Cancelled or Failed item was planned to save, which is empty for an
	// item the job could not plan, such as a source that cannot be resized,
	// or for every item of a job cancelled before it was planned.
	Destination string
	// Reason is why the item did not get its natural destination, as
	// output.Reason describes: why a Completed item was saved under a
	// numbered name, or why an item was Skipped. It is output.ReasonNone
	// otherwise, and in every other state, even for an item whose
	// Destination is a numbered name.
	Reason output.Reason
	// Err is a Failed item's error: the failing step's own typed error, as
	// Process returns it, the plan's error for an item it could not name,
	// the output folder's error once the folder has gone, or the Input's
	// failure. An item of any other final state carries one only when a file
	// the error names was left behind: a Completed item's result was saved
	// but a file was left as output.Write describes, and a Skipped or
	// Cancelled item's temporary file could not be removed, which
	// output.Write joins to the error that ended the write. A Failed item's
	// error includes such a file's too, and an item planned again carries
	// those of its earlier attempts. It is nil otherwise. An Err that joins
	// several errors, as errors.Join does, lists first the error that ended
	// the item's last attempt, and after it only errors that name a file left
	// behind, so a consumer classifies the failure by the first.
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
	// job was cancelled before its destinations were planned, when nothing
	// was saved in it.
	Folder string
	// Items holds every input's final status, at its index in
	// Request.Inputs.
	Items []Status
	// Progress counts the final states; Pending and Processing are zero.
	Progress Progress
}

// processFunc processes one item within the memory the job set aside for
// it, as processWithin does.
type processFunc func(ctx context.Context, item output.Item, settings Settings, share reservation) (string, error)

// config is how a job runs; tests substitute its parts.
type config struct {
	workers int
	budget  uint64
	prepare func(context.Context, output.Request) (output.Plan, error)
	process processFunc
	// collect collects garbage, and allocated reports the bytes allocated on
	// the heap so far, both from the worker goroutines.
	collect   func()
	allocated func() uint64
}

func defaultConfig() config {
	return config{
		workers:   defaultWorkers(),
		budget:    memoryBudget,
		prepare:   output.Prepare,
		process:   processWithin,
		collect:   runtime.GC,
		allocated: heapAllocated,
	}
}

// heapAllocsMetric is the runtime's running total of bytes allocated on the
// heap.
const heapAllocsMetric = "/gc/heap/allocs:bytes"

// heapAllocated returns the bytes allocated on the heap since the process
// started, as decode.Inspect measures them, or 0 if the runtime does not
// report them.
func heapAllocated() uint64 {
	sample := []metrics.Sample{{Name: heapAllocsMetric}}
	metrics.Read(sample)
	if sample[0].Value.Kind() != metrics.KindUint64 {
		return 0
	}
	return sample[0].Value.Uint64()
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
// resized, all before the output folder is created, then creates the
// folder, and plans every destination as the filesystem stands now, so
// re-running a job plans around the results of earlier runs rather than
// assuming them (RSZ-049). A failure of any of these fails the whole job,
// including an output folder that can no longer be reached while the plan
// is made, after the folder was created: Run returns the step's own typed
// error — a *resize.Error, an *encode.Error, or an *output.Error — with an
// empty Summary, having processed nothing and reported no event.
//
// Every listed input is then an item. An input that cannot be resized ends
// Failed with its Err, or ErrUnchecked, and so does an item the plan could
// not name; an item whose destination exists ends Skipped under the Skip
// policy. The rest are processed in order by Process, a few at a time: at
// most one fewer than the processors the program may use, at most four, and
// only while the memory the items in flight need by their estimates — the
// decoded source, as its Input.Info reports it, the resized image, the
// filter's working memory, as resize.WorkingBytes bounds it for the
// source's dimensions, and 20 MiB for the decoder's and the encoder's
// buffers — stays within a budget of 1 GiB (RSZ-050, NFR-014); an item that
// needs more starts only when it would run alone. So the job never decodes
// every source at once. A source that changed since it was checked is
// decoded as it now is (RSZ-049), so one that has gone, or whose content is
// damaged now, fails then; one that now needs more memory than its estimate
// — to decode it, as decode.DecodeWithin describes, or, rewritten to another
// format or shape, for its resized image and the filter, which the item
// checks once its source is decoded — is not resized beside the others, but
// is reported Pending again and started over once it can run alone with the
// most any source needs set aside. A JPEG's conversion to RGB that a segment
// after its first scan requests is counted as decode.Info.DecodedBytes
// counts it. Once the heap allocated 64 MiB or more while an item was
// processed, or while the items that ended since the last collection were,
// together — counting what other items allocated meanwhile too, so it errs
// toward collecting — the item's worker collects garbage before the item's
// share of the budget goes to another, so the memory finished items drop
// does not pile up beside the memory the next ones take, while an item that
// failed before its source was decoded, such as one whose name was taken,
// adds next to nothing toward a collection.
//
// One item's failure never stops the others (RSZ-044): each failure is
// attached to its item as the failing step's own typed error, and a panic
// that escapes Process is contained as that item's ErrUnexpected failure
// (NFR-021). An output folder that has gone does stop them: once an item
// fails to write, or is to be planned again, and its output folder can no
// longer be reached, or is no longer a folder, as output.CheckFolder finds,
// the items still waiting end Failed with that folder's error rather than
// each after its source is decoded and resized (UX-010), and the folder is
// not created again. Under Skip, two sources with one destination are
// planned so that only the first writes it (output.ActionDefer): when that
// item fails, the next one is processed in its place, as
// output.Plan.Successor describes, and once the file is written the rest end
// Skipped, as they do when another program wrote a file at the name after
// planning (AC-R09). Under Rename and Overwrite, an item planned to create a
// name another program took after planning (output.ErrDestinationTaken) is
// planned again and saved under the first unused numbered name instead,
// never replacing that file: Overwrite replaces only the files that existed
// when the job was planned (RSZ-025, AC-R10). Once saved, it reports the
// reason the plan gave it for not having its natural destination; one that
// had it reports the new plan's reason, output.ReasonExists while the file
// that took the name is there. An item planned to replace a file that
// changed since planning replaces nothing and ends Failed with
// output.ErrDestinationChanged, as output.Write describes: the confirmation
// was given for that file as it was, and the error is what says where the
// file is when Write had moved it aside.
//
// Cancelling the context stops the job from starting further items, and the
// items in flight stop at the next point at which Process observes it,
// before their results take their names, so no partial file is left; an
// item already renaming its result finishes and ends Completed. Items that
// did not finish end Cancelled, including one whose name another program
// took, which would have been planned again; completed results are kept,
// and Run returns once no item is in flight (RSZ-045). A cancelled job is
// not a failure: Run returns its Summary and a nil error. A context
// cancelled before Run starts, or while the destinations are planned,
// cancels every item that could be resized.
//
// observe, which may be nil, is told of every change of an item's state:
// each item that is processed becomes Processing and then ends, while one
// that is not ends directly, and an item ends exactly once; an item planned
// again under a new name is reported Processing again with its new
// destination, and one started over within more memory is reported Pending
// and then Processing again. It is called on the goroutine that called Run,
// one event at a time and in order, never concurrently, and no item starts
// or ends while it runs, so it should only hand the event on — a view queues
// it and posts it to the GUI thread through the shell dispatcher (TECH-005).
// Each event carries the counts of every state, so a consumer that falls
// behind can apply the latest event of each item and the latest counts
// alone.
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
	// lost is the output folder's error when the item failed to write and
	// its worker found the folder gone.
	lost error
}

// batch is one running job. Only the goroutine that runs it touches its
// fields, but for what worker goroutines read and never change once execute
// starts — request, cfg, ctx, and plan.Folder — and uncollected, which they
// add to; they report through done.
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
	// records holds what the job keeps of each item, by plan index.
	records []record
	// lost is the output folder's error once the job found it gone.
	lost error
	// inflight and reserved are the items being processed and the memory
	// they hold.
	inflight int
	reserved uint64
	// uncollected is the memory the heap allocated while the items that
	// ended since the last collection were processed, which the workers add
	// to.
	uncollected atomic.Uint64
	done        chan finished
	workers     sync.WaitGroup
}

// record is what the job keeps of one plan item across its attempts.
type record struct {
	// held is the memory the item holds of the budget while it is in flight.
	held uint64
	// worstCase marks an item whose source turned out to need more memory
	// than its estimate, which is now given the most any source needs.
	worstCase bool
	// replans counts how often the item was planned again.
	replans int
	// leftovers are the errors of its earlier attempts that name a file left
	// behind.
	leftovers error
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
		if input.failure() == nil {
			b.planned[index] = len(b.inputs)
			b.inputs = append(b.inputs, index)
		}
	}
	b.records = make([]record, len(b.inputs))
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
			b.set(index, Status{State: StateFailed, Err: input.failure()})
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
			b.set(index, Status{State: StateFailed, Err: input.failure()})
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
		// cancelled while it started the others ends too, as do the items
		// waiting once the output folder is lost.
		switch {
		case ctx.Err() != nil:
			b.endQueued(Status{State: StateCancelled})
		case b.lost != nil:
			b.endQueued(Status{State: StateFailed, Err: b.lost})
		}
		if b.inflight == 0 {
			return
		}
		select {
		case result := <-b.done:
			b.settle(result)
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
	for len(b.queue) > 0 && b.inflight < b.cfg.workers && b.ctx.Err() == nil && b.lost == nil {
		p := b.queue[0]
		cost := b.cost(p)
		if b.inflight > 0 && b.reserved+cost > b.cfg.budget {
			return
		}
		b.queue = b.queue[1:]
		b.inflight++
		b.reserved += cost
		b.records[p].held = cost
		b.run(p)
	}
}

// run reports the item at p Processing and processes it on a worker of its
// own, within the memory it holds.
func (b *batch) run(p int) {
	item := b.plan.Items[p]
	b.set(b.inputs[p], Status{State: StateProcessing, Destination: item.Destination})
	b.workers.Add(1)
	go b.work(p, item, reservation{allowance: b.allowance(p), held: b.records[p].held})
}

// release frees the worker and the memory the item at p held.
func (b *batch) release(p int) {
	b.inflight--
	b.reserved -= b.records[p].held
	b.records[p].held = 0
}

// work processes one item on its own goroutine, within the memory the job
// set aside for it. done has room for every item in flight, so the send
// never blocks. An item that failed to write may have lost its output
// folder, which the worker checks, off the coordinating goroutine. Before it
// reports the item, it collects garbage when the heap allocated
// collectThreshold or more while the item was processed, or while it and the
// items ended since the last collection were, together, so the memory they
// dropped is free before their share of the budget goes to another item. The
// heap's count includes what other items allocated meanwhile, so it errs
// toward collecting, while an item that fails before its source is decoded,
// as one whose name was taken, adds next to nothing of its own.
func (b *batch) work(p int, item output.Item, share reservation) {
	defer b.workers.Done()
	before := b.cfg.allocated()
	path, err := b.process(item, share)
	result := finished{plan: p, path: path, err: err}
	if path == "" && writeFailed(err) {
		result.lost = output.CheckFolder(b.plan.Folder)
	}
	if grown := b.cfg.allocated() - before; grown >= collectThreshold || b.uncollected.Add(grown) >= collectThreshold {
		b.uncollected.Store(0)
		b.cfg.collect()
	}
	b.done <- result
}

// process runs Process, containing a panic that escapes it as the item's
// failure: a panic on a worker goroutine would end the whole program, not
// one item (NFR-021). The steps contain their own panics; this guard is for
// whatever escapes them.
func (b *batch) process(item output.Item, share reservation) (path string, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			path, err = "", fmt.Errorf("%w: %v", ErrUnexpected, recovered)
			if panicErr, ok := recovered.(error); ok {
				err = fmt.Errorf("%w: %w", ErrUnexpected, panicErr)
			}
		}
	}()
	return b.cfg.process(b.ctx, item, b.request.Settings, share)
}

// writeFailed reports whether err is output's failure to write a result,
// which an output folder that has gone fails with.
func writeFailed(err error) bool {
	return hasKind(err, output.KindWrite)
}

// hasKind reports whether err is an *output.Error of the given kind.
func hasKind(err error, kind output.Kind) bool {
	var outputErr *output.Error
	return errors.As(err, &outputErr) && outputErr.Kind == kind
}

// outgrown reports whether err says the item's source, as it now is, needs
// more memory than the job set aside for it: to decode it
// (decode.ErrOverAllowance), or for the whole item once decoded
// (errOutgrown).
func outgrown(err error) bool {
	return errors.Is(err, decode.ErrOverAllowance) || errors.Is(err, errOutgrown)
}

// settle takes one processed item's result: it starts the item over when it
// may still save its result — with the most any source needs set aside when
// its source needed more memory than its estimate, or under a new name when
// another program took its destination — and ends it otherwise.
func (b *batch) settle(result finished) {
	p := result.plan
	if result.lost != nil && b.lost == nil {
		b.lost = result.lost
	}
	switch {
	case !b.records[p].worstCase && outgrown(result.err):
		// Its source changed since it was checked. It waits until it can run
		// alone, as cost now gives it the most any source needs.
		b.release(p)
		b.records[p].worstCase = true
		b.set(b.inputs[p], Status{State: StatePending, Destination: b.plan.Items[p].Destination})
		b.enqueue(p)
		return
	case b.ctx.Err() == nil && b.retryable(result):
		if b.rename(&result) {
			return
		}
	}
	b.release(p)
	b.end(p, b.outcome(result))
}

// retryable reports whether a processed item may still save its result
// under a new name: another program took its destination after planning,
// under Rename or Overwrite, the output folder is not lost, and the item was
// planned again fewer than maxReplans times. Under Skip the existing file is
// left untouched and the item ends Skipped instead, as outcome reports.
func (b *batch) retryable(result finished) bool {
	return result.path == "" && errors.Is(result.err, output.ErrDestinationTaken) &&
		b.request.Collision != output.CollisionSkip && b.lost == nil && b.records[result.plan].replans < maxReplans
}

// rename plans the item again whose destination another program took after
// planning, and starts it again under the name the new plan gives it, in
// the worker slot and memory it already holds. Under Rename the result takes
// the first unused numbered name, as Rename does for a name taken at
// planning (RSZ-025). Under Overwrite it does too: the user confirmed
// replacing the files that existed when the job was planned, and a file
// written since is none of them (AC-R10). The item keeps the reason the plan
// gave it, as Run describes, and the error of the attempt is kept when it
// names a file left behind. It reports whether the item started again; when
// it did not, result carries the error the item ends with: planning it again
// failed, or the new plan names no file for it. An output folder that can no
// longer be reached, as replan finds, is lost for the whole job.
func (b *batch) rename(result *finished) bool {
	p := result.plan
	b.records[p].leftovers = join(b.records[p].leftovers, leftBehind(result.err))
	b.records[p].replans++
	item, err := b.replan(p)
	if err != nil {
		if hasKind(err, output.KindFolder) && b.lost == nil {
			b.lost = err
		}
		result.err = err
		return false
	}
	if reason := b.plan.Items[p].Reason; reason != output.ReasonNone {
		item.Reason = reason
	}
	b.plan.Items[p] = item
	if !item.Writes() {
		result.err = item.Err
		return false
	}
	b.run(p)
	return true
}

// replan plans the item at p again, under Rename, as a job of its own source
// whose protected paths are every other listed path and every other item's
// destination, so its new name is none of theirs. Under Rename nothing is
// replaced, so planning reads no file identities, and it runs on the
// coordinating goroutine, which keeps the destinations it avoids current.
// Another item's destination reads to that plan as a listed path, which is
// why the item keeps its own plan's reason. The output folder is checked
// first, as output.CheckFolder checks it, since Prepare would create a
// folder that has gone since the item's name was found taken.
func (b *batch) replan(p int) (output.Item, error) {
	if err := output.CheckFolder(b.plan.Folder); err != nil {
		return output.Item{}, err
	}
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

// allowance is the pixel memory set aside for decoding the item at p's
// source: the estimate its content check made, or the decoder's limit when
// there is none, or once the source turned out to need more.
func (b *batch) allowance(p int) uint64 {
	decoded := b.request.Inputs[b.inputs[p]].Info.DecodedBytes
	if b.records[p].worstCase || decoded == 0 || decoded > decode.MaxDecodedBytes {
		return decode.MaxDecodedBytes
	}
	return decoded
}

// cost is the memory the item at p may need while it is processed, as
// itemBytes counts it for its source as its content check found it, within
// its allowance. A source that turned out to need more than its estimate may
// differ from its check in every way, so it is given the most any source
// needs: the decoder's limit, the resized image at 8 bytes a pixel,
// resize.MaxWorkingBytes, and bufferBytes, which processWithin's check of
// the decoded source never finds exceeded.
func (b *batch) cost(p int) uint64 {
	info := b.request.Inputs[b.inputs[p]].Info
	target := b.request.Settings.Target
	if b.records[p].worstCase {
		return decode.MaxDecodedBytes + resultBytes(decode.FormatPNG, target) + resize.MaxWorkingBytes + bufferBytes
	}
	return itemBytes(info, b.allowance(p), target)
}

// itemBytes is the memory an item may need while it is processed, for a
// source of the content info describes whose decoding needs decoded bytes:
// the decoded source; the resized image; the filter's working memory, as
// resize.WorkingBytes bounds it for the source's dimensions; and bufferBytes
// for the decoder's and the encoder's buffers. The encoder works in the
// resized image's memory or streams from it.
func itemBytes(info decode.Info, decoded uint64, target resize.Target) uint64 {
	return decoded + resultBytes(info.Format, target) + resize.WorkingBytes(info.Width, info.Height, target) + bufferBytes
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
// is a cancellation, and so is a taken name the item would have been planned
// again for. Under Skip, a destination another program took after planning
// is an existing destination, which Skip leaves untouched. An error that
// names a file left behind stays with the item whatever it ends in.
func (b *batch) outcome(result finished) Status {
	p := result.plan
	item := b.plan.Items[p]
	earlier := b.records[p].leftovers
	switch {
	case result.err == nil || result.path != "":
		destination := result.path
		if destination == "" {
			destination = item.Destination
		}
		return Status{State: StateCompleted, Destination: destination, Reason: item.Reason, Err: join(result.err, earlier)}
	case canceled(b.ctx, result.err) || (b.ctx.Err() != nil && b.retryable(result)):
		return Status{State: StateCancelled, Destination: item.Destination, Err: join(leftBehind(result.err), earlier)}
	case b.request.Collision == output.CollisionSkip && errors.Is(result.err, output.ErrDestinationTaken):
		return Status{State: StateSkipped, Destination: item.Destination, Reason: output.ReasonExists, Err: join(leftBehind(result.err), earlier)}
	}
	return Status{State: StateFailed, Destination: item.Destination, Err: join(result.err, earlier)}
}

// leftBehind returns a failed write's error when it names a temporary file
// left behind, which output.Write marks with output.ErrLeftBehind and joins
// to the error that ended the write, and nil otherwise.
func leftBehind(err error) error {
	if errors.Is(err, output.ErrLeftBehind) {
		return err
	}
	return nil
}

// join joins two errors, either of which may be nil, keeping a lone one as
// it is.
func join(first, second error) error {
	switch {
	case first == nil:
		return second
	case second == nil:
		return first
	}
	return errors.Join(first, second)
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

// endQueued ends every waiting item in the given state, with its own
// destination: Cancelled once the job is cancelled, with the items deferred
// to them, or Failed with the folder's error once the output folder is lost,
// when the item deferred to one is promoted and then ended in its turn.
func (b *batch) endQueued(status Status) {
	for len(b.queue) > 0 {
		p := b.queue[0]
		b.queue = b.queue[1:]
		status.Destination = b.plan.Items[p].Destination
		b.end(p, status)
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
