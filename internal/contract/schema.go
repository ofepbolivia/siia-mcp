package contract

import (
	"reflect"
	"strings"
)

// Schema is a JSON Schema value following draft-2020-12. Type may be a string
// or a slice of strings (e.g. ["string","null"]) to express nullable values.
// Every object schema is closed with additionalProperties=false.
type Schema struct {
	Type                 any                `json:"type,omitempty"`
	Properties           map[string]*Schema `json:"properties,omitempty"`
	Required             []string           `json:"required,omitempty"`
	Items                *Schema            `json:"items,omitempty"`
	Enum                 []any              `json:"enum,omitempty"`
	AdditionalProperties *bool              `json:"additionalProperties,omitempty"`
	MinLength            *int               `json:"minLength,omitempty"`
	MaxLength            *int               `json:"maxLength,omitempty"`
	Minimum              *int               `json:"minimum,omitempty"`
	Maximum              *int               `json:"maximum,omitempty"`
}

func ptr(v int) *int { return &v }

type schemaTag struct {
	required  bool
	nullable  bool
	minLength *int
	maxLength *int
	minimum   *int
	maximum   *int
	enum      []any
}

func parseTag(tag string) schemaTag {
	var st schemaTag
	for _, part := range strings.Split(tag, ",") {
		switch {
		case part == "required":
			st.required = true
		case part == "nullable":
			st.nullable = true
		case strings.HasPrefix(part, "minlength="):
			st.minLength = ptr(atoi(part[len("minlength="):]))
		case strings.HasPrefix(part, "maxlength="):
			st.maxLength = ptr(atoi(part[len("maxlength="):]))
		case strings.HasPrefix(part, "minimum="):
			st.minimum = ptr(atoi(part[len("minimum="):]))
		case strings.HasPrefix(part, "maximum="):
			st.maximum = ptr(atoi(part[len("maximum="):]))
		case strings.HasPrefix(part, "enum="):
			var vals []any
			for _, v := range strings.Split(part[len("enum="):], "|") {
				vals = append(vals, v)
			}
			st.enum = vals
		}
	}
	return st
}

func atoi(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			break
		}
		n = n*10 + int(r-'0')
	}
	return n
}

// toolInputSchema derives the input schema for a tool argument struct.
func toolInputSchema(v any) *Schema {
	root := &Schema{Type: "object", AdditionalProperties: boolPtr(false)}
	buildObject(reflect.TypeOf(v), root)
	return root
}

func forType(t reflect.Type, st *schemaTag) *Schema {
	if st == nil {
		st = &schemaTag{}
	}
	nullable := st.nullable

	switch t.Kind() {
	case reflect.Struct:
		s := &Schema{Type: "object", AdditionalProperties: boolPtr(false)}
		buildObject(t, s)
		return maybeNullable(s, nullable)
	case reflect.String:
		s := &Schema{Type: "string"}
		s.MaxLength = st.maxLength
		s.MinLength = st.minLength
		s.Enum = st.enum
		return maybeNullable(s, nullable)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		s := &Schema{Type: "integer"}
		s.Minimum = st.minimum
		s.Maximum = st.maximum
		return maybeNullable(s, nullable)
	case reflect.Bool:
		return maybeNullable(&Schema{Type: "boolean"}, nullable)
	case reflect.Slice, reflect.Array:
		if t.Elem().Kind() == reflect.Uint8 {
			return maybeNullable(&Schema{Type: "string"}, nullable) // []byte
		}
		s := &Schema{Type: "array", Items: forType(t.Elem(), nil)}
		return maybeNullable(s, nullable)
	case reflect.Pointer:
		return forType(t.Elem(), st)
	case reflect.Interface:
		// interface{} / any accepts any JSON value; leave the schema open.
		return maybeNullable(&Schema{}, nullable)
	default:
		return maybeNullable(&Schema{Type: "string"}, nullable)
	}
}

func buildObject(t reflect.Type, s *Schema) {
	s.Properties = map[string]*Schema{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" {
			continue
		}
		name := f.Tag.Get("json")
		tag := parseTag(f.Tag.Get("schemajson"))
		if name == "" || name == "-" {
			continue
		}
		name = strings.Split(name, ",")[0]
		fieldType := f.Type
		nullable := tag.nullable
		if fieldType.Kind() == reflect.Pointer {
			nullable = true
			fieldType = fieldType.Elem()
		}
		s.Properties[name] = forType(fieldType, &tag)
		if nullable {
			s.Properties[name] = maybeNullable(s.Properties[name], true)
		}
		if isRequired(name, tag) {
			s.Required = append(s.Required, name)
		}
	}
}

func isRequired(name string, tag schemaTag) bool {
	return tag.required
}

func maybeNullable(s *Schema, nullable bool) *Schema {
	if !nullable {
		return s
	}
	switch t := s.Type.(type) {
	case string:
		s.Type = []any{string(t), "null"}
	case []any:
		// already nullable
	}
	return s
}

func boolPtr(b bool) *bool { return &b }
