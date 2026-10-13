package storage

import (
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"errors"
	"fmt"

	"github.com/nerdwave-nick/lit/internal/protocol"
)

// strict rejects unknown members; a set and members hold only plain fields.
var strict = jsonv2.RejectUnknownMembers(true)

// storedJSON decodes the intents and results storage wrote itself in one pass
// each. protocol.Decode validates a request strictly by decoding it again at
// every level that has its own UnmarshalJSON, which scans each body an intent
// sets about ten times; for the history of a long-lived issue that was most of
// the read. The stored rows passed that validation when they were written, so
// these unmarshalers only rebuild what the methods add: a null parent_id or
// assignee as a pointer to nil, and the members' present fields. Unknown
// members, duplicate names and invalid UTF-8 still fail.
var storedJSON = jsonv2.JoinOptions(strict, jsonv2.WithUnmarshalers(jsonv2.JoinUnmarshalers(
	jsonv2.UnmarshalFromFunc(func(d *jsontext.Decoder, v *protocol.Intent) error {
		type plain protocol.Intent
		return jsonv2.UnmarshalDecode(d, (*plain)(v))
	}),
	jsonv2.UnmarshalFromFunc(func(d *jsontext.Decoder, v *protocol.ProjectSet) error {
		type plain protocol.ProjectSet
		return jsonv2.UnmarshalDecode(d, (*plain)(v))
	}),
	// A set's parent_id and assignee (its only **string fields) keep a null
	// as a pointer to nil, which clears them; other values decode as usual.
	jsonv2.UnmarshalFromFunc(func(d *jsontext.Decoder, v ***string) error {
		if d.PeekKind() != 'n' {
			return errors.ErrUnsupported
		}
		if _, e := d.ReadToken(); e != nil {
			return e
		}
		*v = new(*string)
		return nil
	}),
	jsonv2.UnmarshalFromFunc(func(d *jsontext.Decoder, v *protocol.ProjectMembers) error {
		raw, e := d.ReadValue()
		if e != nil {
			return e
		}
		type plain protocol.ProjectMembers
		var fields map[string]jsontext.Value
		if e = jsonv2.Unmarshal(raw, &fields); e != nil {
			return e
		}
		if e = jsonv2.Unmarshal(raw, (*plain)(v), strict); e != nil {
			return e
		}
		v.Fields = map[string]bool{}
		for k := range fields {
			v.Fields[k] = true
		}
		return nil
	}),
)))

// decodeTransaction decodes a stored transaction's intent and results.
func decodeTransaction(intent, results string) (protocol.Intent, []protocol.ChangedObject, error) {
	return decodeStored([]byte(intent), []byte(results))
}

// decodeStored is decodeTransaction of the stored JSON as bytes.
func decodeStored(intent, results []byte) (protocol.Intent, []protocol.ChangedObject, error) {
	var i protocol.Intent
	var c []protocol.ChangedObject
	if e := jsonv2.Unmarshal(intent, &i, storedJSON); e != nil {
		return i, nil, fmt.Errorf("stored intent: %w", e)
	}
	if e := jsonv2.Unmarshal(results, &c, storedJSON); e != nil {
		return i, nil, fmt.Errorf("stored results: %w", e)
	}
	return i, c, nil
}
