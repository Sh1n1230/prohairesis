// Package event is the observability layer: an append-only record of what an
// agent session did.
//
// The rules this layer lives under are narrow and worth stating, because every
// tempting feature violates one of them:
//
//   - It never blocks. A failure to record is a failure of this package, never
//     of the action being recorded.
//   - It never decides. There is no classification and no verdict here, and the
//     schema has no field for one.
//   - It never keeps what it does not need. Command lines are hashed rather than
//     stored: metrics ask whether two actions were the same, never what they
//     were, and a log that answers the second question becomes a new place for
//     secrets to collect.
package event

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
)

// Schema is the contract this package emits. The JSON Schema in schema/ is
// authoritative; the round-trip test keeps the two from drifting.
const Schema = "harness.event.v1"

// Type distinguishes the three things worth recording. A checkpoint is not one
// of them: meta.json is the single record of what a checkpoint is, and
// CheckpointRef points at it.
type Type string

const (
	SessionStart Type = "session_start"
	SessionEnd   Type = "session_end"
	Tool         Type = "tool"
)

// Kind is the provider-independent vocabulary for what an action did. Opaque is
// a real answer: an adapter that cannot map a tool says so, rather than folding
// it into exec and manufacturing the appearance of full coverage.
type Kind string

const (
	KindRead        Kind = "read"
	KindWrite       Kind = "write"
	KindDelete      Kind = "delete"
	KindExec        Kind = "exec"
	KindNetwork     Kind = "network"
	KindAgentConfig Kind = "agent_config"
	KindOpaque      Kind = "opaque"
)

// Event is one observation. Schema: harness.event.v1.
type Event struct {
	Schema    string    `json:"schema"`
	TS        time.Time `json:"ts"`
	Seq       int       `json:"seq"`
	SessionID string    `json:"session_id"`
	RepoKey   string    `json:"repo_key,omitempty"`
	Type      Type      `json:"type"`
	Actor     Actor     `json:"actor"`

	Action  *Action  `json:"action,omitempty"`
	Outcome *Outcome `json:"outcome,omitempty"`

	// HookMS is time spent inside the hook process. It excludes process start,
	// which is most of the cost -- see docs/METRICS.md. Recorded, not budgeted.
	HookMS int `json:"hook_ms,omitempty"`

	CheckpointRef *CheckpointRef `json:"checkpoint_ref,omitempty"`
	Truncated     bool           `json:"truncated,omitempty"`
}

// Actor is who produced the event. Several agent sessions may attach to one
// prohairesis session, so the runtime's own identifier is kept alongside ours.
type Actor struct {
	Adapter        string `json:"adapter"`
	AgentSessionID string `json:"agent_session_id,omitempty"`
	// Grade is the adapter's capability grade for the hook behind this event.
	// It says how complete this record can possibly be, which a reader cannot
	// otherwise know.
	Grade string `json:"grade,omitempty"`
}

// Action is the canonical vocabulary. It names no provider.
type Action struct {
	Kind Kind   `json:"kind"`
	Tool string `json:"tool"`
	// Command is argv[0] only. The rest is represented by ArgvSHA256.
	Command    string   `json:"command,omitempty"`
	ArgvSHA256 string   `json:"argv_sha256,omitempty"`
	Paths      []string `json:"paths,omitempty"`
	Host       string   `json:"host,omitempty"`
}

// Outcome is what happened, as reported by the runtime. Absent when unknown --
// which is different from succeeded.
type Outcome struct {
	Status     string `json:"status,omitempty"`
	DurationMS int    `json:"duration_ms,omitempty"`
}

// CheckpointRef answers one question: to get the tree back to how it was at this
// event, which checkpoint do you start from? That question has an answer for
// every event, so the field is written on every event -- not only on the ones
// that happened to take a checkpoint. Leaving it off elsewhere would push the
// reader into inferring what the log can simply state.
type CheckpointRef struct {
	Seq    int    `json:"seq"`
	Commit string `json:"commit,omitempty"`
	// TakenHere marks the event that took this checkpoint.
	TakenHere bool `json:"taken_here,omitempty"`
}

// New starts an event with the fields every event has.
func New(t Type, sessionID string, actor Actor) Event {
	return Event{
		Schema:    Schema,
		TS:        time.Now().UTC().Truncate(time.Millisecond),
		SessionID: sessionID,
		Type:      t,
		Actor:     actor,
	}
}

// DigestArgv reduces a command line to an identity. Two identical commands
// produce the same digest; nothing about either can be recovered from it.
func DigestArgv(s string) string {
	if strings.TrimSpace(s) == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
