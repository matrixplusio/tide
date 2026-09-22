package v1

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/go-playground/validator/v10"

	"tide/internal/i18n"
	"tide/internal/server/api/errcode"
	"tide/internal/validate"
)

// Request bodies are structs with `json`, `binding` (validator/v10) and
// `label` tags. bindJSON decodes strictly, trims strings via Normalize, runs
// tag rules, then the struct's own Validate for rules that need context
// (password policy, confirm match). All field errors come back at once.

type normalizer interface{ Normalize() }

type checker interface{ Check() error }

var registerOnce sync.Once

func validatorEngine() *validator.Validate {
	v := binding.Validator.Engine().(*validator.Validate)
	registerOnce.Do(func() {
		// Field names in errors are "jsonName|label" so we can build both the
		// JSON path and a human message.
		v.RegisterTagNameFunc(func(f reflect.StructField) string {
			name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			if name == "-" {
				return ""
			}
			if name == "" {
				name = f.Name
			}
			return name + "|" + f.Tag.Get("label")
		})
	})
	return v
}

// bindJSON returns an *errcode.Error (BadRequest or ValidationFailed) on failure.
func bindJSON(c *gin.Context, req any) error {
	dec := json.NewDecoder(c.Request.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(req); err != nil {
		var maxErr *http.MaxBytesError
		switch {
		case errors.Is(err, io.EOF):
			return errcode.NewKey(errcode.BadRequest, "b.bodyEmpty")
		case errors.As(err, &maxErr):
			return errcode.NewKey(errcode.BadRequest, "b.bodyTooBig")
		default:
			k, a := jsonErrorText(err)
			return errcode.WrapKey(errcode.BadRequest, err, k, a...)
		}
	}
	if dec.More() {
		return errcode.NewKey(errcode.BadRequest, "b.oneObject")
	}
	if n, ok := req.(normalizer); ok {
		n.Normalize()
	}
	var fields []errcode.FieldError
	if isStruct(req) {
		if err := validatorEngine().Struct(req); err != nil {
			var ves validator.ValidationErrors
			if !errors.As(err, &ves) {
				return errcode.Wrap(errcode.BadRequest, err, "")
			}
			for _, fe := range ves {
				fields = append(fields, translate(fe))
			}
		}
	}
	if ch, ok := req.(checker); ok {
		if err := ch.Check(); err != nil {
			var ves validate.Errors
			if !errors.As(err, &ves) {
				return err
			}
			seen := map[string]bool{}
			for _, f := range fields {
				seen[f.Field] = true
			}
			for _, f := range ves {
				if !seen[f.Field] { // tag rule already reported this field
					fields = append(fields, errcode.FieldError{Field: f.Field, Msg: f.Message, Key: f.Key, Args: f.Args})
				}
			}
		}
	}
	if len(fields) > 0 {
		return errcode.Invalid(fields...)
	}
	return nil
}

// jsonErrorText names what is wrong with the body, as a key and its
// arguments so the reason renders in the reader's language.
func jsonErrorText(err error) (i18n.Key, []any) {
	var te *json.UnmarshalTypeError
	var maxErr *http.MaxBytesError
	switch {
	case errors.As(err, &te):
		return "b.jsonWrongType", []any{te.Field}
	case strings.HasPrefix(err.Error(), "json: unknown field "):
		return "b.jsonUnknownField", []any{strings.Trim(strings.TrimPrefix(err.Error(), "json: unknown field "), `"`)}
	case errors.As(err, &maxErr):
		return "b.bodyTooBig", nil
	}
	return "b.jsonUnparsable", nil
}

// translate turns a validator error into a field error carrying a catalog
// key: the label comes from the struct's `label` tag, which holds a key too,
// so the sentence and the noun inside it render in one language at the edge.
func translate(fe validator.FieldError) errcode.FieldError {
	path, label := fieldPath(fe.Namespace())
	l := i18n.Key("label." + label)
	if label == "" {
		l = "b.thisField"
	}
	p := fe.Param()
	key := i18n.Key("b.format")
	args := []any{l}
	slice := fe.Kind() == reflect.Slice
	switch fe.Tag() {
	case "required":
		key = "b.required"
	case "max":
		key, args = pick(slice, "b.maxItems", "b.maxChars"), []any{l, p}
	case "min":
		key, args = pick(slice, "b.minItems", "b.minChars"), []any{l, p}
	case "gte":
		key, args = "b.gte", []any{l, p}
	case "lte":
		key, args = "b.lte", []any{l, p}
	case "oneof":
		key, args = "b.oneof", []any{l, strings.ReplaceAll(p, " ", " / ")}
	case "unique":
		key = "b.unique"
	}
	return errcode.FieldError{Field: path, Key: key, Args: args}
}

func pick(slice bool, forSlice, forScalar i18n.Key) i18n.Key {
	if slice {
		return forSlice
	}
	return forScalar
}

// fieldPath converts "loginReq.items[0].kargoUrl|kargoUrl" style namespaces
// into ("items.0.kargoUrl", "kargoUrl"). The label is a catalog key's last
// segment, not the whole key, so that it never contains a dot and cannot be
// mistaken for a step in the field path.
func fieldPath(ns string) (string, string) {
	parts := strings.Split(ns, ".")
	if len(parts) > 1 {
		parts = parts[1:] // drop the struct type name
	}
	var out []string
	label := ""
	for _, part := range parts {
		name, l, _ := strings.Cut(part, "|")
		idx := ""
		if i := strings.Index(l, "["); i >= 0 {
			l, idx = l[:i], l[i:]
		}
		if i := strings.Index(name, "["); i >= 0 {
			name, idx = name[:i], name[i:]
		}
		out = append(out, name)
		if idx != "" {
			out = append(out, strings.Trim(idx, "[]"))
		}
		label = l
	}
	return strings.Join(out, "."), label
}

func trim(ps ...*string) {
	for _, p := range ps {
		*p = strings.TrimSpace(*p)
	}
}

func isStruct(v any) bool {
	t := reflect.TypeOf(v)
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t != nil && t.Kind() == reflect.Struct
}
