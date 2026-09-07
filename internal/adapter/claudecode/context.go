package claudecode

import (
	"bytes"
	"encoding/json"
)

// SessionStartContext encodes text for the runtime to add to the agent's
// context at the start of a session.
//
// The explicit form is used rather than bare text on stdout. Both are accepted
// by this runtime, but bare stdout is a channel this hook also uses for nothing
// else, and a future diagnostic printed by accident would become an instruction
// to the agent. Naming the field says what the bytes are for.
//
// Empty text produces no output at all, so a session with nothing to point at
// costs the agent nothing.
func SessionStartContext(text string) []byte {
	if text == "" {
		return nil
	}
	payload := map[string]any{
		"hookSpecificOutput": map[string]string{
			"hookEventName":     "SessionStart",
			"additionalContext": text,
		},
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(payload); err != nil {
		// There is no failure to report to: this runs inside a hook that must
		// exit 0 having said nothing rather than say something malformed.
		return nil
	}
	return buf.Bytes()
}
