package sandbox

import (
	"fmt"
	"io"
	"log/slog"
	"reflect"
)

const redacted = "[REDACTED]"

// secret keeps a credential behind a pointer so reflection-based printing of a
// containing struct value shows only an address.
type secret struct{ value *string }

func newSecret(value string) secret {
	if value == "" {
		return secret{}
	}
	return secret{value: &value}
}

func (s secret) reveal() string {
	if s.value == nil {
		return ""
	}
	return *s.value
}

func (secret) String() string { return redacted }

func (secret) GoString() string { return redacted }

func (secret) Format(state fmt.State, _ rune) { _, _ = io.WriteString(state, redacted) }

func (secret) LogValue() slog.Value { return slog.StringValue(redacted) }

// String reports the client's endpoint without its auth token.
func (c *Client) String() string {
	if c == nil {
		return "<nil>"
	}
	return fmt.Sprintf("sandbox.Client{baseURL: %q, authToken: %s}", c.baseURL, redacted)
}

// GoString keeps %#v from dumping the auth token.
func (c *Client) GoString() string {
	return c.String()
}

// Format keeps every verb, including %+v and %#v, from dumping the auth token.
func (c *Client) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, c.String())
}

// LogValue keeps slog from dumping the auth token.
func (c *Client) LogValue() slog.Value {
	if c == nil {
		return slog.StringValue("<nil>")
	}
	return slog.GroupValue(slog.String("base_url", c.baseURL))
}

// String reports the session's exported fields without its data-plane credential.
func (s *Session) String() string {
	return fmt.Sprintf("%v", s)
}

// GoString keeps %#v from dumping the data-plane credential.
func (s *Session) GoString() string {
	return fmt.Sprintf("%#v", s)
}

// Format prints only exported fields so the data-plane credential never reaches fmt.
func (s *Session) Format(state fmt.State, verb rune) {
	if s == nil {
		_, _ = io.WriteString(state, "<nil>")
		return
	}
	formatExportedFields(state, verb, reflect.ValueOf(s).Elem())
}

func formatExportedFields(state fmt.State, verb rune, value reflect.Value) {
	goSyntax := verb == 'v' && state.Flag('#')
	withNames := goSyntax || (verb == 'v' && state.Flag('+'))
	fieldFormat := fmt.FormatString(state, verb)
	if goSyntax {
		_, _ = io.WriteString(state, "&"+value.Type().String()+"{")
	} else {
		_, _ = io.WriteString(state, "&{")
	}
	separator := " "
	if goSyntax {
		separator = ", "
	}
	first := true
	for i := range value.NumField() {
		field := value.Type().Field(i)
		if !field.IsExported() {
			continue
		}
		if !first {
			_, _ = io.WriteString(state, separator)
		}
		first = false
		if withNames {
			_, _ = io.WriteString(state, field.Name+":")
		}
		fieldValue := value.Field(i)
		if !isOpaquePointer(fieldValue) {
			_, _ = fmt.Fprintf(state, fieldFormat, fieldValue.Interface())
		} else if goSyntax {
			_, _ = fmt.Fprintf(state, "(%s)(%p)", fieldValue.Type(), fieldValue.Interface())
		} else {
			_, _ = fmt.Fprintf(state, "%p", fieldValue.Interface())
		}
	}
	_, _ = io.WriteString(state, "}")
}

// isOpaquePointer mirrors fmt's nested-pointer rule: print the address, not the
// pointee, so back-references such as Git.session never expand.
func isOpaquePointer(value reflect.Value) bool {
	if value.Kind() != reflect.Pointer || value.IsNil() {
		return false
	}
	switch value.Interface().(type) {
	case fmt.Formatter, fmt.Stringer:
		return false
	}
	return true
}
