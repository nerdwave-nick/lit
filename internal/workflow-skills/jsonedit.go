package workflowskills

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// object is a JSON object whose key order and member texts survive an edit.
// Host files such as settings.json are edited by the host too, so setup changes
// only the members it owns and leaves the rest as the user and the host wrote
// them: member values stay byte-for-byte (apart from indentation) and keep
// their order.
type object struct {
	keys []string
	vals map[string]json.RawMessage
}

func newObject() *object { return &object{vals: map[string]json.RawMessage{}} }

// parseObject reads a document that must be exactly one JSON object with
// distinct keys.
func parseObject(b []byte) (*object, error) {
	d := json.NewDecoder(bytes.NewReader(b))
	tok, err := d.Token()
	if err != nil {
		return nil, fmt.Errorf("not a JSON object: %w", err)
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		return nil, errors.New("not a JSON object")
	}
	o := newObject()
	for d.More() {
		tok, err = d.Token()
		if err != nil {
			return nil, err
		}
		key, ok := tok.(string)
		if !ok {
			return nil, errors.New("not a JSON object")
		}
		if _, dup := o.vals[key]; dup {
			return nil, fmt.Errorf("duplicate key %q", key)
		}
		var raw json.RawMessage
		if err = d.Decode(&raw); err != nil {
			return nil, err
		}
		o.keys = append(o.keys, key)
		o.vals[key] = raw
	}
	if _, err = d.Token(); err != nil {
		return nil, err
	}
	if _, err = d.Token(); err != io.EOF {
		return nil, errors.New("unexpected data after the JSON object")
	}
	return o, nil
}

func (o *object) get(k string) (json.RawMessage, bool) {
	v, ok := o.vals[k]
	return v, ok
}

// set replaces the value of k in place, or appends the member.
func (o *object) set(k string, v json.RawMessage) {
	if _, ok := o.vals[k]; !ok {
		o.keys = append(o.keys, k)
	}
	o.vals[k] = v
}

func (o *object) del(k string) {
	if _, ok := o.vals[k]; !ok {
		return
	}
	delete(o.vals, k)
	for i, key := range o.keys {
		if key == k {
			o.keys = append(o.keys[:i:i], o.keys[i+1:]...)
			break
		}
	}
}

// raw is the object as one compact JSON value, to embed in another object.
func (o *object) raw() json.RawMessage {
	var compact bytes.Buffer
	compact.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			compact.WriteByte(',')
		}
		compact.Write(encodeString(k))
		compact.WriteByte(':')
		compact.Write(o.vals[k])
	}
	compact.WriteByte('}')
	return compact.Bytes()
}

// bytes writes the object with two-space indentation and a final newline, the
// way the hosts write their own files.
func (o *object) bytes() []byte {
	var out bytes.Buffer
	if err := json.Indent(&out, o.raw(), "", "  "); err != nil {
		panic(fmt.Sprintf("workflowskills: invalid JSON member: %v", err))
	}
	out.WriteByte('\n')
	return out.Bytes()
}

// encodeString is a JSON string without HTML escaping, which would rewrite
// characters the user wrote.
func encodeString(s string) []byte {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	_ = e.Encode(s)
	return bytes.TrimSuffix(b.Bytes(), []byte("\n"))
}
