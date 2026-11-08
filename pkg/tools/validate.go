package tools

import (
	"fmt"
	"math"
	"reflect"
	"regexp"
	"unicode/utf8"
)

// validateToolArgs validates args against a JSON Schema-like map.
// schema is expected to have optional keys: "properties", "required", "additionalProperties".
func validateToolArgs(schema map[string]any, args map[string]any) error {
	if len(schema) == 0 {
		return nil
	}

	if args == nil {
		args = map[string]any{}
	}

	if err := checkRequired(schema, args); err != nil {
		return err
	}

	propsRaw, ok := schema["properties"]
	if !ok {
		return nil // no properties defined — accept any args
	}

	props, ok := propsRaw.(map[string]any)
	if !ok {
		return nil
	}

	additional := allowsAdditional(schema)

	for key, val := range args {
		propSchemaRaw, known := props[key]
		if !known {
			if !additional {
				return fmt.Errorf("unexpected property %q", key)
			}
			continue
		}
		propSchema, ok := propSchemaRaw.(map[string]any)
		if !ok {
			continue // can't validate without a proper schema map
		}
		if err := checkType(key, val, propSchema); err != nil {
			return err
		}
	}

	return nil
}

// checkRequired verifies that every field listed in schema["required"] is present in args.
func checkRequired(schema map[string]any, args map[string]any) error {
	reqRaw, ok := schema["required"]
	if !ok {
		return nil
	}

	var required []string

	switch r := reqRaw.(type) {
	case []string:
		required = r
	case []any:
		for _, v := range r {
			s, ok := v.(string)
			if ok {
				required = append(required, s)
			}
		}
	default:
		return nil
	}

	for _, field := range required {
		if _, present := args[field]; !present {
			return fmt.Errorf("missing required property %q", field)
		}
	}
	return nil
}

// allowsAdditional returns true when the schema explicitly sets
// "additionalProperties" to true, or when the key is absent (default: reject extras).
func allowsAdditional(schema map[string]any) bool {
	v, ok := schema["additionalProperties"]
	if !ok {
		return false
	}
	b, ok := v.(bool)
	return ok && b
}

// checkType validates that val matches the JSON Schema type declared in propSchema.
func checkType(key string, val any, propSchema map[string]any) error {
	if err := checkOneOf(key, val, propSchema); err != nil {
		return err
	}
	if constant, ok := propSchema["const"]; ok && !reflect.DeepEqual(val, constant) {
		return fmt.Errorf("property %q: expected constant %v", key, constant)
	}
	typeRaw, ok := propSchema["type"]
	if !ok {
		return nil // no type constraint
	}
	typeName, ok := typeRaw.(string)
	if !ok {
		return nil
	}

	switch typeName {
	case "string":
		text, ok := val.(string)
		if !ok {
			return fmt.Errorf("property %q: expected string, got %T", key, val)
		}
		if err := checkStringConstraints(key, text, propSchema); err != nil {
			return err
		}
	case "integer":
		switch v := val.(type) {
		case float64:
			if v != math.Trunc(v) {
				return fmt.Errorf("property %q: expected integer, got float64 with fractional part", key)
			}
			if err := checkNumericRange(key, v, propSchema); err != nil {
				return err
			}
		case int:
			if err := checkNumericRange(key, float64(v), propSchema); err != nil {
				return err
			}
		case int64:
			if err := checkNumericRange(key, float64(v), propSchema); err != nil {
				return err
			}
		default:
			return fmt.Errorf("property %q: expected integer, got %T", key, val)
		}
	case "number":
		switch number := val.(type) {
		case float64, int, int64:
			if err := checkNumericRange(key, numberAsFloat(number), propSchema); err != nil {
				return err
			}
		default:
			return fmt.Errorf("property %q: expected number, got %T", key, val)
		}
	case "boolean":
		if _, ok := val.(bool); !ok {
			return fmt.Errorf("property %q: expected boolean, got %T", key, val)
		}
	case "array":
		arr, ok := val.([]any)
		if !ok {
			return fmt.Errorf("property %q: expected array, got %T", key, val)
		}
		if err := checkArrayItems(key, arr, propSchema); err != nil {
			return err
		}
	case "object":
		obj, ok := val.(map[string]any)
		if !ok {
			return fmt.Errorf("property %q: expected object, got %T", key, val)
		}
		if err := validateToolArgs(propSchema, obj); err != nil {
			return fmt.Errorf("property %q: %w", key, err)
		}
	}

	if err := checkEnum(key, val, propSchema); err != nil {
		return err
	}

	return nil
}

func checkNumericRange(key string, value float64, schema map[string]any) error {
	if minimum, ok := numberAsFloatOK(schema["minimum"]); ok && value < minimum {
		return fmt.Errorf("property %q: value %v is below minimum %v", key, value, minimum)
	}
	if maximum, ok := numberAsFloatOK(schema["maximum"]); ok && value > maximum {
		return fmt.Errorf("property %q: value %v is above maximum %v", key, value, maximum)
	}
	return nil
}

func checkOneOf(key string, val any, schema map[string]any) error {
	for _, field := range []string{"anyOf", "oneOf"} {
		raw, ok := schema[field]
		if !ok {
			continue
		}
		alternatives, ok := raw.([]any)
		if !ok {
			continue
		}
		matched := 0
		for _, alternative := range alternatives {
			candidate, ok := alternative.(map[string]any)
			if ok && checkType(key, val, candidate) == nil {
				matched++
			}
		}
		if matched == 0 || (field == "oneOf" && matched != 1) {
			return fmt.Errorf("property %q: value does not match %s", key, field)
		}
	}
	return nil
}

func checkStringConstraints(key, value string, schema map[string]any) error {
	length := utf8.RuneCountInString(value)
	if minimum, ok := numberAsFloatOK(schema["minLength"]); ok && length < int(minimum) {
		return fmt.Errorf("property %q: shorter than minLength %v", key, minimum)
	}
	if maximum, ok := numberAsFloatOK(schema["maxLength"]); ok && length > int(maximum) {
		return fmt.Errorf("property %q: longer than maxLength %v", key, maximum)
	}
	if pattern, ok := schema["pattern"].(string); ok && pattern != "" {
		compiled, err := regexp.Compile(pattern)
		if err != nil {
			return fmt.Errorf("property %q: schema pattern is invalid", key)
		}
		if !compiled.MatchString(value) {
			return fmt.Errorf("property %q: does not match pattern", key)
		}
	}
	return nil
}

func numberAsFloat(value any) float64 {
	number, _ := numberAsFloatOK(value)
	return number
}

func numberAsFloatOK(value any) (float64, bool) {
	switch number := value.(type) {
	case float64:
		return number, true
	case int:
		return float64(number), true
	case int64:
		return float64(number), true
	default:
		return 0, false
	}
}

// checkArrayItems validates each element of arr against the "items" sub-schema.
func checkArrayItems(key string, arr []any, propSchema map[string]any) error {
	itemsRaw, ok := propSchema["items"]
	if !ok {
		return nil
	}
	itemSchema, ok := itemsRaw.(map[string]any)
	if !ok {
		return nil
	}
	for i, elem := range arr {
		elemKey := fmt.Sprintf("%s[%d]", key, i)
		if err := checkType(elemKey, elem, itemSchema); err != nil {
			return err
		}
	}
	return nil
}

// checkEnum validates that val is one of the allowed enum values in propSchema.
func checkEnum(key string, val any, propSchema map[string]any) error {
	enumRaw, ok := propSchema["enum"]
	if !ok {
		return nil
	}

	switch ev := enumRaw.(type) {
	case []any:
		for _, allowed := range ev {
			if val == allowed {
				return nil
			}
		}
	case []string:
		s, ok := val.(string)
		if ok {
			for _, allowed := range ev {
				if s == allowed {
					return nil
				}
			}
		}
	default:
		return nil // unknown enum format, skip
	}

	return fmt.Errorf("property %q: value %v is not in enum", key, val)
}
