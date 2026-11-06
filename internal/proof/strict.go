package proof

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
)

// DecodeJSON decodes one JSON document with numbers kept as json.Number so
// integers outside the float64-safe range survive canonicalization exactly.
// Trailing data after the document is rejected.
func DecodeJSON(raw []byte, v any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(v); err != nil {
		return err
	}
	return expectEOF(decoder)
}

// DecodeStrict decodes a portable proof document without the ambiguities of
// encoding/json defaults. It rejects duplicate object keys anywhere in the
// document, keys in schema objects that are unknown or differ from the declared
// field name only by case, numbers whose text is not canonical, and trailing
// data. Numbers inside free-form values are kept as json.Number.
//
// Without these checks a bundle could carry both "payload" and "PAYLOAD":
// encoding/json would hash one value while another reader trusts the other.
func DecodeStrict(raw []byte, v any) error {
	target := reflect.TypeOf(v)
	if target == nil || target.Kind() != reflect.Pointer {
		return errors.New("strict decode requires a pointer")
	}
	scanner := json.NewDecoder(bytes.NewReader(raw))
	scanner.UseNumber()
	if err := checkStrictValue(scanner, target.Elem(), "$"); err != nil {
		return err
	}
	if err := expectEOF(scanner); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	return decoder.Decode(v)
}

func expectEOF(decoder *json.Decoder) error {
	if _, err := decoder.Token(); err != io.EOF {
		return errors.New("unexpected data after JSON document")
	}
	return nil
}

var jsonUnmarshalerType = reflect.TypeFor[json.Unmarshaler]()

// checkStrictValue walks one JSON value alongside the Go type it will decode
// into. A nil type means free-form JSON (interface{} values).
func checkStrictValue(decoder *json.Decoder, target reflect.Type, path string) error {
	for target != nil && target.Kind() == reflect.Pointer {
		target = target.Elem()
	}
	if target != nil && (target.Kind() == reflect.Interface || reflect.PointerTo(target).Implements(jsonUnmarshalerType)) {
		target = nil
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	switch value := token.(type) {
	case json.Delim:
		switch value {
		case '{':
			var fields map[string]reflect.Type
			var elem reflect.Type
			if target != nil && target.Kind() == reflect.Struct {
				fields = structFields(target)
			} else if target != nil && target.Kind() == reflect.Map {
				elem = target.Elem()
			}
			seen := map[string]struct{}{}
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return err
				}
				key, _ := keyToken.(string)
				if _, duplicate := seen[key]; duplicate {
					return fmt.Errorf("duplicate key %q at %s", key, path)
				}
				seen[key] = struct{}{}
				child := elem
				if fields != nil {
					fieldType, known := fields[key]
					if !known {
						return fmt.Errorf("unknown or non-canonical field %q at %s", key, path)
					}
					child = fieldType
				}
				if err = checkStrictValue(decoder, child, path+"."+key); err != nil {
					return err
				}
			}
		case '[':
			var elem reflect.Type
			if target != nil && (target.Kind() == reflect.Slice || target.Kind() == reflect.Array) {
				elem = target.Elem()
			}
			for index := 0; decoder.More(); index++ {
				if err = checkStrictValue(decoder, elem, fmt.Sprintf("%s[%d]", path, index)); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("unexpected %q at %s", value, path)
		}
		_, err = decoder.Token()
		return err
	case json.Number:
		canonical, err := canonicalNumber(value)
		if err != nil || canonical != value.String() {
			return fmt.Errorf("non-canonical number %s at %s", value, path)
		}
	}
	return nil
}

func structFields(target reflect.Type) map[string]reflect.Type {
	fields := map[string]reflect.Type{}
	for i := 0; i < target.NumField(); i++ {
		field := target.Field(i)
		tag := field.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, _, _ := strings.Cut(tag, ",")
		if field.Anonymous && name == "" {
			embedded := field.Type
			if embedded.Kind() == reflect.Pointer {
				embedded = embedded.Elem()
			}
			if embedded.Kind() == reflect.Struct {
				for key, value := range structFields(embedded) {
					fields[key] = value
				}
				continue
			}
		}
		if !field.IsExported() {
			continue
		}
		if name == "" {
			name = field.Name
		}
		fields[name] = field.Type
	}
	return fields
}
