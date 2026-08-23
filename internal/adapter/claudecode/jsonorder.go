package claudecode

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// object is a JSON object that remembers the order of its keys and the exact
// bytes of the values nobody asked to change.
//
// This exists for one reason: editing somebody's configuration file should
// change the part that was asked for and nothing else. Decoding into a map and
// re-encoding would reorder every key and reflow every nested block, so the
// diff of "install a hook" would touch the whole file -- and a tool that
// rewrites your configuration wholesale to make one addition has not earned the
// trust it is asking for.
type object struct {
	keys []string
	vals map[string]json.RawMessage
}

func newObject() *object {
	return &object{vals: map[string]json.RawMessage{}}
}

func parseObject(b []byte) (*object, error) {
	o := newObject()
	if len(bytes.TrimSpace(b)) == 0 {
		return o, nil
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, fmt.Errorf("expected a JSON object")
	}
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := kt.(string)
		if !ok {
			return nil, fmt.Errorf("expected an object key")
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, err
		}
		o.set(key, raw)
	}
	return o, nil
}

func (o *object) set(key string, raw json.RawMessage) {
	if _, seen := o.vals[key]; !seen {
		o.keys = append(o.keys, key)
	}
	o.vals[key] = raw
}

func (o *object) get(key string) (json.RawMessage, bool) {
	v, ok := o.vals[key]
	return v, ok
}

func (o *object) delete(key string) {
	if _, ok := o.vals[key]; !ok {
		return
	}
	delete(o.vals, key)
	for i, k := range o.keys {
		if k == key {
			o.keys = append(o.keys[:i], o.keys[i+1:]...)
			break
		}
	}
}

func (o *object) empty() bool { return len(o.keys) == 0 }

// encode writes the object back out, keeping every untouched value byte for
// byte and matching the indentation the file already used.
func (o *object) encode(indent string) ([]byte, error) {
	if o.empty() {
		return []byte("{}\n"), nil
	}
	var b bytes.Buffer
	b.WriteString("{\n")
	for i, k := range o.keys {
		key, err := json.Marshal(k)
		if err != nil {
			return nil, err
		}
		b.WriteString(indent)
		b.Write(key)
		b.WriteString(": ")
		b.Write(reindent(o.vals[k], indent))
		if i < len(o.keys)-1 {
			b.WriteByte(',')
		}
		b.WriteByte('\n')
	}
	b.WriteString("}\n")
	return b.Bytes(), nil
}

// reindent leaves a raw value alone. Values arriving from the file already carry
// their original layout; values this program built are produced with the same
// indentation, so neither needs touching.
func reindent(raw json.RawMessage, _ string) []byte { return raw }

// detectIndent reads the file's own indentation off its first indented line, so
// that anything added matches what is already there.
func detectIndent(b []byte) string {
	for _, line := range strings.Split(string(b), "\n") {
		trimmed := strings.TrimLeft(line, " \t")
		if trimmed == "" || len(trimmed) == len(line) {
			continue
		}
		return line[:len(line)-len(trimmed)]
	}
	return "  "
}

// canonical reduces JSON to a form that compares by content rather than layout.
// Used to answer one question: has anyone changed this file since we touched it?
func canonical(b []byte) string {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return string(b)
	}
	out, err := json.Marshal(v)
	if err != nil {
		return string(b)
	}
	return string(out)
}
