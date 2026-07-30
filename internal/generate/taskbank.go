package generate

// task is a coding job a synthetic session works on. Difficulty is
// model-relative (see DECISIONS): it sets the IRT difficulty b used to sample
// outcomes, and the prompt text carries keyword density that tracks it, so a
// predictive router can actually learn something from features.
type task struct {
	Title         string
	Tier          string  // "easy" | "medium" | "hard"
	BaseDifficulty float64 // IRT b; higher = harder
	OpenPrompt    string   // dense, no pleasantries
	Restate       string   // used by retry/switch follow-ups
	ErrorSnippet  string   // pasted back for the paste_error signal
	Subtasks      []string // introduced by moveon follow-ups
}

// taskBank is intentionally all software-engineering ("it's code") so topic
// classification collapses — difficulty, not topic, is the only useful axis.
var taskBank = []task{
	// ---- easy ----
	{
		Title: "reverse-string", Tier: "easy", BaseDifficulty: -0.3,
		OpenPrompt: "Write a Go function ReverseString(s string) string that returns the input reversed. Handle unicode correctly. [tools: editor]",
		Restate:    "reverse a string in Go, unicode-safe",
		ErrorSnippet: "panic: runtime error: index out of range [5] with length 5",
		Subtasks: []string{"Add a table-driven test for ReverseString.", "Now make it reverse words instead of runes."},
	},
	{
		Title: "sum-slice", Tier: "easy", BaseDifficulty: -0.1,
		OpenPrompt: "Implement Sum(xs []int) int in Go returning the total. Empty slice returns 0.",
		Restate:    "sum an int slice in Go",
		ErrorSnippet: "./main.go:8:9: undefined: total",
		Subtasks: []string{"Make it generic over any numeric type.", "Add an average helper that avoids integer truncation."},
	},
	{
		Title: "json-parse", Tier: "easy", BaseDifficulty: 0.2,
		OpenPrompt: "Parse this JSON config into a Go struct and print the port field. [tools: editor, shell]",
		Restate:    "unmarshal a small JSON config into a struct in Go",
		ErrorSnippet: "json: cannot unmarshal string into Go struct field Config.port of type int",
		Subtasks: []string{"Add validation that port is in 1..65535.", "Support loading the config from a file path arg."},
	},
	{
		Title: "cli-flag", Tier: "easy", BaseDifficulty: 0.3,
		OpenPrompt: "Add a --verbose bool flag to this Go CLI using the flag package and gate log output on it.",
		Restate:    "add a --verbose flag to a Go CLI",
		ErrorSnippet: "flag provided but not defined: -verbose",
		Subtasks: []string{"Also add --output to write results to a file.", "Print usage on -h with examples."},
	},

	// ---- medium ----
	{
		Title: "lru-cache", Tier: "medium", BaseDifficulty: 1.0,
		OpenPrompt: "Implement a thread-safe LRU cache in Go with Get/Put in O(1) using a map plus a doubly linked list. Guard it with a mutex. [tools: editor, shell, tests]",
		Restate:    "a thread-safe O(1) LRU cache in Go with a map and linked list",
		ErrorSnippet: "fatal error: concurrent map writes\n\ngoroutine 34 [running]:",
		Subtasks: []string{"Add a capacity-eviction unit test.", "Make eviction fire a callback with the evicted key."},
	},
	{
		Title: "worker-pool", Tier: "medium", BaseDifficulty: 1.2,
		OpenPrompt: "Build a bounded worker pool in Go: N goroutines draining a jobs channel, results on an out channel, clean shutdown via context cancellation. Avoid goroutine leaks. [tools: editor, tests]",
		Restate:    "a bounded worker pool with goroutines, channels and context cancellation",
		ErrorSnippet: "test timed out after 30s\ngoroutine leak detected: 4 goroutines still running",
		Subtasks: []string{"Add backpressure so producers block when the queue is full.", "Propagate the first worker error and cancel the rest."},
	},
	{
		Title: "sql-migrate", Tier: "medium", BaseDifficulty: 1.3,
		OpenPrompt: "Write an idempotent SQL migration to add a nullable 'archived_at' timestamp to the orders table and backfill it for soft-deleted rows in one transaction.",
		Restate:    "an idempotent transactional migration adding archived_at and backfilling it",
		ErrorSnippet: "ERROR: column \"archived_at\" of relation \"orders\" already exists (SQLSTATE 42701)",
		Subtasks: []string{"Add the matching down-migration.", "Add a partial index on archived_at where not null."},
	},
	{
		Title: "regex-extract", Tier: "medium", BaseDifficulty: 1.1,
		OpenPrompt: "Write a regex and Go code to extract semver versions (major.minor.patch with optional -prerelease) from log lines, ignoring versions inside code fences.",
		Restate:    "a regex to pull semver versions from logs, skipping fenced code",
		ErrorSnippet: "error parsing regexp: invalid nested repetition operator: `**`",
		Subtasks: []string{"Capture the prerelease tag into a named group.", "Return them de-duplicated in sorted semver order."},
	},

	// ---- hard ----
	{
		Title: "raft-elect", Tier: "hard", BaseDifficulty: 2.6,
		OpenPrompt: "Implement leader election for a Raft node in Go: randomized election timeouts, RequestVote RPC handling, term bookkeeping, and stepping down when a higher term is seen. Must be race-free under -race and maintain the election-safety invariant across a distributed cluster. [tools: editor, shell, tests]",
		Restate:    "Raft leader election with randomized timeouts, RequestVote, and term stepdown, race-free",
		ErrorSnippet: "WARNING: DATA RACE\nWrite at 0x00c0000b4010 by goroutine 12:\n  raft.(*Node).becomeLeader()",
		Subtasks: []string{"Add the AppendEntries heartbeat to suppress new elections.", "Prove no two leaders in the same term with a test harness."},
	},
	{
		Title: "lockfree-queue", Tier: "hard", BaseDifficulty: 2.8,
		OpenPrompt: "Implement a lock-free MPSC queue in Go using atomic compare-and-swap on unsafe.Pointer, ABA-safe, with correct memory ordering and no data races under -race. Explain the linearization points. [tools: editor, tests]",
		Restate:    "a lock-free ABA-safe MPSC queue with atomic CAS and correct memory ordering",
		ErrorSnippet: "WARNING: DATA RACE\nRead at 0x00c000140008 by goroutine 7:\n  queue.(*Queue).Pop()\nPrevious write by goroutine 9",
		Subtasks: []string{"Add a hazard-pointer scheme to reclaim nodes safely.", "Benchmark throughput vs a mutex queue under contention."},
	},
	{
		Title: "query-planner", Tier: "hard", BaseDifficulty: 2.4,
		OpenPrompt: "Write a cost-based join-order optimizer for a small SQL engine: dynamic-programming over the subset lattice, cardinality estimation, and a pluggable cost model. Must handle the NP-hard search with a bushy-plan pruning heuristic. [tools: editor, tests]",
		Restate:    "a DP cost-based join-order optimizer with cardinality estimation and pruning",
		ErrorSnippet: "panic: runtime: out of memory: cannot allocate 8589934592-byte block (subset lattice blew up)",
		Subtasks: []string{"Switch the DP from full bushy to left-deep to bound memory.", "Add histogram-based selectivity estimation."},
	},
	{
		Title: "gc-tune", Tier: "hard", BaseDifficulty: 2.5,
		OpenPrompt: "Diagnose and fix a memory leak in this long-running Go service: goroutines and heap grow unbounded under load. Find the retention path, fix it, and prove it with a pprof-based regression test. [tools: editor, shell, pprof, tests]",
		Restate:    "diagnose and fix an unbounded goroutine/heap memory leak, proven with pprof",
		ErrorSnippet: "runtime: goroutine stack exceeds 1000000000-byte limit\nfatal error: stack overflow",
		Subtasks: []string{"Add a leak-detecting test using runtime.NumGoroutine deltas.", "Cap the retained cache with a TTL to bound the heap."},
	},
}
