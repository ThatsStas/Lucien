// Package notestore is the sole reader and writer of note files
// (DESIGN.md 2). Everything else — API, worker — goes through Store.
package notestore

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// Note is the full note document as stored in <id>.json (DESIGN.md 3.2).
// JSON field names here are the on-disk schema and must stay in sync.
type Note struct {
	ID             string     `json:"id"`
	Created        time.Time  `json:"created"`
	Modified       time.Time  `json:"modified"`
	State          string     `json:"state"`
	StateChangedAt time.Time  `json:"state_changed_at"`
	Tags           []Tag      `json:"tags"`
	Priority       int        `json:"priority"`
	Source         Source     `json:"source"`
	Entries        []Entry    `json:"entries"`
	Processing     Processing `json:"processing"`
	RemindAt       *time.Time `json:"remind_at"`
	DeferUntil     *time.Time `json:"defer_until"`
	Dedup          Dedup      `json:"dedup"`
}

type Tag struct {
	Name string `json:"name"`
	By   string `json:"by"` // "agent" | "human"
}

type Source struct {
	Kind string  `json:"kind"` // text | audio | link | youtube | ribbon
	URL  *string `json:"url"`
}

type Entry struct {
	ID   int       `json:"id"`
	TS   time.Time `json:"ts"`
	Kind string    `json:"kind"` // text | transcript | excerpt
	Text string    `json:"text"`
}

type Processing struct {
	Step        string     `json:"step"` // transcribe|extract|tag|embed|none
	Status      string     `json:"status"`
	RetryCount  int        `json:"retry_count"`
	NextRetryAt *time.Time `json:"next_retry_at"`
	LastError   *string    `json:"last_error"`
}

type Dedup struct {
	URLHash     *string     `json:"url_hash"`
	ContentHash string      `json:"content_hash"`
	Resaved     []time.Time `json:"resaved"`
}

// Processing step and status values (DESIGN.md 4, 5).
const (
	StepTranscribe = "transcribe"
	StepExtract    = "extract"
	StepTag        = "tag"
	StepEmbed      = "embed"
	StepNone       = "none"

	StatusPending = "pending"
	StatusRunning = "running"
	StatusFailed  = "failed"
)

var (
	states       = set("inbox", "todo", "in_progress", "done", "someday", "archived")
	sourceKinds  = set("text", "audio", "link", "youtube", "ribbon")
	entryKinds   = set("text", "transcript", "excerpt")
	tagBy        = set("agent", "human")
	procSteps    = set(StepTranscribe, StepExtract, StepTag, StepEmbed, StepNone)
	procStatuses = set(StatusPending, StatusRunning, StatusFailed)

	// chains is the normative pipeline order per DESIGN.md 4. Steps whose
	// pipelines are not registered in a given phase simply stay pending;
	// the worker skips them (see DoD report for this design gap).
	chains = map[string][]string{
		"audio":   {StepTranscribe, StepTag, StepEmbed},
		"link":    {StepExtract, StepTag, StepEmbed},
		"youtube": {StepExtract, StepTag, StepEmbed},
		"text":    {StepTag, StepEmbed},
		"ribbon":  {StepTag, StepEmbed},
	}
)

func set(vs ...string) map[string]bool {
	m := make(map[string]bool, len(vs))
	for _, v := range vs {
		m[v] = true
	}
	return m
}

// FirstStep is the chain entry point for a source kind ("none" if unknown).
func FirstStep(sourceKind string) string {
	c := chains[sourceKind]
	if len(c) == 0 {
		return StepNone
	}
	return c[0]
}

// nextStep returns the step after current for the source kind's chain,
// or "none" for the terminal rest state (including unknown steps).
func nextStep(sourceKind, current string) string {
	c := chains[sourceKind]
	for i, s := range c {
		if s == current && i+1 < len(c) {
			return c[i+1]
		}
	}
	return StepNone
}

// ContentHash is sha256 over the entry texts joined by "\n", prefixed
// "sha256:". DESIGN.md names content_hash but does not define its inputs;
// entry-text concatenation is the implementation choice recorded in the
// DoD report. Empty notes hash the empty string.
func ContentHash(entries []Entry) string {
	parts := make([]string, len(entries))
	for i, e := range entries {
		parts[i] = e.Text
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// validate rejects schema violations. On the read path the caller wraps
// these as ErrCorrupt; on the write path they surface as user errors.
func validate(n *Note) error {
	if n.ID == "" {
		return fmt.Errorf("note id empty")
	}
	if !states[n.State] {
		return fmt.Errorf("invalid state %q", n.State)
	}
	if !sourceKinds[n.Source.Kind] {
		return fmt.Errorf("invalid source kind %q", n.Source.Kind)
	}
	if n.Priority < -20 || n.Priority > 19 {
		return fmt.Errorf("priority %d outside -20..19", n.Priority)
	}
	for _, t := range n.Tags {
		if t.Name == "" {
			return fmt.Errorf("tag with empty name")
		}
		if !tagBy[t.By] {
			return fmt.Errorf("tag %q has invalid by %q", t.Name, t.By)
		}
	}
	for _, e := range n.Entries {
		if e.ID < 1 {
			return fmt.Errorf("entry id %d below 1", e.ID)
		}
		if !entryKinds[e.Kind] {
			return fmt.Errorf("entry %d has invalid kind %q", e.ID, e.Kind)
		}
	}
	if !procSteps[n.Processing.Step] {
		return fmt.Errorf("invalid processing step %q", n.Processing.Step)
	}
	if !procStatuses[n.Processing.Status] {
		return fmt.Errorf("invalid processing status %q", n.Processing.Status)
	}
	return nil
}
