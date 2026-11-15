package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// This file decodes JSON request bodies and checks them against a model, as
// FastAPI and pydantic do in 2.0: same status codes and the same error
// "type" and "loc" for field errors. Messages are pydantic's where that is
// free; positions in JSON syntax errors are Go's.

var utf8BOM = []byte("\xef\xbb\xbf")

// isJSONMediaType reports whether FastAPI reads a body with this
// Content-Type as JSON: application/json or application/*+json.
func isJSONMediaType(contentType string) bool {
	mt, _ := mediaType(contentType)
	return mt == "application/json" || strings.HasPrefix(mt, "application/") && strings.HasSuffix(mt, "+json")
}

// decodeJSONBody decodes a request body. Numbers stay json.Number, so that
// they are forwarded as written. A body that is not JSON by Content-Type is
// returned as a string, which every model then rejects, as in 2.0.
func decodeJSONBody(contentType string, body []byte) (any, *apiError) {
	if len(body) == 0 {
		return nil, errValidation([]validationIssue{missing([]any{"body"}, nil)})
	}
	if !isJSONMediaType(contentType) {
		return strings.ToValidUTF8(string(body), "�"), nil
	}
	body = bytes.TrimPrefix(body, utf8BOM)
	if !utf8.Valid(body) {
		return nil, errBodyParsing()
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var v any
	err := dec.Decode(&v)
	offset := int64(len(body))
	var syntax *json.SyntaxError
	switch {
	case errors.As(err, &syntax):
		// Go counts the bytes read, including the offending one; pydantic
		// points at it.
		offset = max(syntax.Offset-1, 0)
	case err == nil:
		if _, terr := dec.Token(); terr != io.EOF {
			offset, err = dec.InputOffset(), errors.New("extra data after the JSON value")
		}
	}
	if err != nil {
		if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
			err = errors.New("unexpected end of JSON input")
		}
		return nil, errValidation([]validationIssue{{
			Type: "json_invalid", Loc: []any{"body", offset}, Msg: "JSON decode error",
			Input: map[string]any{}, Ctx: map[string]any{"error": err.Error()},
		}})
	}
	return v, nil
}

// kind is the JSON type a model field accepts.
type kind int

const (
	kindAny kind = iota
	kindString
	kindInt
	kindFloat
	kindBool
	kindList
	kindDict
	kindModel
)

// valueType describes the values a field accepts.
type valueType struct {
	kind     kind
	nullable bool
	enum     []string // for kindString
	minLen   int      // for kindString
	elem     *valueType
	model    *model
}

// model is a request body model: the documented fields of a Go struct. It
// is built once from the struct that also documents the body in OpenAPI.
type model struct {
	fields []modelField
}

type modelField struct {
	name     string
	required bool
	typ      valueType
}

// modelOf describes struct type t from its tags: the json name, required
// ("required" tag, default false), enum ("enum", comma separated) and
// minLength. A pointer field accepts null. Unexported and "-" fields are
// skipped.
func modelOf(t reflect.Type) *model {
	m := &model{}
	for i := range t.NumField() {
		f := t.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if !f.IsExported() || name == "-" {
			continue
		}
		if name == "" {
			name = f.Name
		}
		typ := typeOf(f.Type)
		if e := f.Tag.Get("enum"); e != "" {
			typ.enum = strings.Split(e, ",")
		}
		if n, err := strconv.Atoi(f.Tag.Get("minLength")); err == nil {
			typ.minLen = n
		}
		m.fields = append(m.fields, modelField{name: name, required: f.Tag.Get("required") == "true", typ: typ})
	}
	return m
}

func typeOf(t reflect.Type) valueType {
	var vt valueType
	if t.Kind() == reflect.Pointer {
		vt.nullable = true
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.String:
		vt.kind = kindString
	case reflect.Int, reflect.Int32, reflect.Int64:
		vt.kind = kindInt
	case reflect.Float32, reflect.Float64:
		vt.kind = kindFloat
	case reflect.Bool:
		vt.kind = kindBool
	case reflect.Slice:
		elem := typeOf(t.Elem())
		vt.kind, vt.elem, vt.nullable = kindList, &elem, true // as nil
	case reflect.Map:
		vt.kind, vt.nullable = kindDict, true
	case reflect.Struct:
		vt.kind, vt.model = kindModel, modelOf(t)
	default:
		vt.kind = kindAny
	}
	return vt
}

// checkModel checks that v is an object whose documented fields have the
// documented types, and returns it. Fields the model does not document are
// kept: the caller decides what to do with them.
func checkModel(m *model, v any) (map[string]any, *apiError) {
	var issues []validationIssue
	obj := m.check(v, []any{"body"}, &issues)
	if issues != nil {
		return nil, errValidation(issues)
	}
	return obj, nil
}

// checkDict checks that v is an object (FastAPI's dict[str, Any] body).
func checkDict(v any) (map[string]any, *apiError) {
	obj, ok := v.(map[string]any)
	if !ok {
		return nil, errValidation([]validationIssue{{Type: "dict_type", Loc: []any{"body"}, Msg: "Input should be a valid dictionary", Input: v}})
	}
	return obj, nil
}

// checkModelList checks that v is a list of objects of model m.
func checkModelList(m *model, v any) ([]map[string]any, *apiError) {
	list, ok := v.([]any)
	if !ok {
		return nil, errValidation([]validationIssue{{Type: "list_type", Loc: []any{"body"}, Msg: "Input should be a valid list", Input: v}})
	}
	var issues []validationIssue
	out := make([]map[string]any, len(list))
	for i, item := range list {
		out[i] = m.check(item, []any{"body", i}, &issues)
	}
	if issues != nil {
		return nil, errValidation(issues)
	}
	return out, nil
}

func (m *model) check(v any, loc []any, issues *[]validationIssue) map[string]any {
	obj, ok := v.(map[string]any)
	if !ok {
		*issues = append(*issues, validationIssue{
			Type: "model_attributes_type", Loc: loc, Input: v,
			Msg: "Input should be a valid dictionary or object to extract fields from",
		})
		return nil
	}
	for _, f := range m.fields {
		fv, present := obj[f.name]
		floc := append(slices.Clip(loc), f.name)
		switch {
		case !present && f.required:
			*issues = append(*issues, missing(floc, obj))
		case present:
			f.typ.check(fv, floc, issues)
		}
	}
	return obj
}

func (t *valueType) check(v any, loc []any, issues *[]validationIssue) {
	add := func(typ, msg string, ctx map[string]any) {
		*issues = append(*issues, validationIssue{Type: typ, Loc: loc, Msg: msg, Input: v, Ctx: ctx})
	}
	if v == nil {
		switch {
		case t.nullable || t.kind == kindAny:
		case len(t.enum) > 0:
			expected := enumText(t.enum)
			add("enum", "Input should be "+expected, map[string]any{"expected": expected})
		default:
			add(nullType(t.kind), nullMessage(t.kind), nil)
		}
		return
	}
	switch t.kind {
	case kindString:
		s, ok := v.(string)
		switch {
		case !ok:
			add("string_type", "Input should be a valid string", nil)
		case len(t.enum) > 0 && !slices.Contains(t.enum, s):
			expected := enumText(t.enum)
			add("enum", "Input should be "+expected, map[string]any{"expected": expected})
		case utf8.RuneCountInString(s) < t.minLen:
			add("string_too_short", fmt.Sprintf("String should have at least %d character%s", t.minLen, plural(t.minLen)),
				map[string]any{"min_length": t.minLen})
		}
	case kindInt:
		switch n := v.(type) {
		case json.Number:
			if _, err := n.Int64(); err != nil {
				add("int_from_float", "Input should be a valid integer, got a number with a fractional part", nil)
			}
		case string:
			add("int_parsing", "Input should be a valid integer, unable to parse string as an integer", nil)
		default:
			add("int_type", "Input should be a valid integer", nil)
		}
	case kindFloat:
		switch v.(type) {
		case json.Number:
		case string:
			add("float_parsing", "Input should be a valid number, unable to parse string as a number", nil)
		default:
			add("float_type", "Input should be a valid number", nil)
		}
	case kindBool:
		switch v.(type) {
		case bool:
		case string:
			add("bool_parsing", "Input should be a valid boolean, unable to interpret input", nil)
		default:
			add("bool_type", "Input should be a valid boolean", nil)
		}
	case kindList:
		list, ok := v.([]any)
		if !ok {
			add("list_type", "Input should be a valid list", nil)
			return
		}
		for i, item := range list {
			t.elem.check(item, append(slices.Clip(loc), i), issues)
		}
	case kindDict:
		if _, ok := v.(map[string]any); !ok {
			add("dict_type", "Input should be a valid dictionary", nil)
		}
	case kindModel:
		t.model.check(v, loc, issues)
	}
}

func nullType(k kind) string {
	switch k {
	case kindString:
		return "string_type"
	case kindInt:
		return "int_type"
	case kindFloat:
		return "float_type"
	case kindBool:
		return "bool_type"
	case kindList:
		return "list_type"
	case kindDict:
		return "dict_type"
	}
	return "model_attributes_type"
}

func nullMessage(k kind) string {
	switch k {
	case kindString:
		return "Input should be a valid string"
	case kindInt:
		return "Input should be a valid integer"
	case kindFloat:
		return "Input should be a valid number"
	case kindBool:
		return "Input should be a valid boolean"
	case kindList:
		return "Input should be a valid list"
	case kindDict:
		return "Input should be a valid dictionary"
	}
	return "Input should be a valid dictionary or object to extract fields from"
}

// enumText is pydantic's list of expected values: 'a', 'b' or 'c'.
func enumText(values []string) string {
	quoted := make([]string, len(values))
	for i, v := range values {
		quoted[i] = "'" + v + "'"
	}
	if len(quoted) == 1 {
		return quoted[0]
	}
	return strings.Join(quoted[:len(quoted)-1], ", ") + " or " + quoted[len(quoted)-1]
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// marshalJSON encodes v like 2.0's responses: compact, no HTML escaping,
// no trailing newline.
func marshalJSON(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(b.Bytes(), []byte("\n")), nil
}
