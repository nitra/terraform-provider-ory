package kratosadmin

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// Merge compares the previous configuration, new configuration, and current
// Kratos values. Arrays are one logical path; object members merge separately.
// A changed path is written only if Kratos still holds the previous value.
func Merge(previous, desired, live json.RawMessage) ([]Operation, error) {
	for _, doc := range []json.RawMessage{previous, desired, live} {
		if err := ValidateDocument(doc); err != nil {
			return nil, err
		}
	}
	var before, after, current map[string]any
	if json.Unmarshal(previous, &before) != nil || json.Unmarshal(desired, &after) != nil || json.Unmarshal(live, &current) != nil {
		return nil, errors.New("некоректний JSON identity")
	}
	var changes []Operation
	if err := compare("", before, after, current, &changes); err != nil {
		return nil, err
	}
	return changes, nil
}

func compare(path string, before, after, current any, changes *[]Operation) error {
	if reflect.DeepEqual(before, after) {
		return nil
	}
	b, bObject := before.(map[string]any)
	a, aObject := after.(map[string]any)
	c, cObject := current.(map[string]any)
	if before == nil && aObject && cObject {
		b = map[string]any{}
		bObject = true
	}
	if bObject && aObject && cObject {
		keys := make(map[string]struct{}, len(b)+len(a))
		for key := range b {
			keys[key] = struct{}{}
		}
		for key := range a {
			keys[key] = struct{}{}
		}
		ordered := make([]string, 0, len(keys))
		for key := range keys {
			ordered = append(ordered, key)
		}
		sort.Strings(ordered)
		for _, key := range ordered {
			old, oldExists := b[key]
			newValue, newExists := a[key]
			actual, actualExists := c[key]
			child := path + "/" + escape(key)
			if oldExists && newExists && actualExists {
				if err := compare(child, old, newValue, actual, changes); err != nil {
					return err
				}
				continue
			}
			if oldExists == newExists && reflect.DeepEqual(old, newValue) {
				continue
			}
			if actualExists == newExists && reflect.DeepEqual(actual, newValue) {
				continue
			}
			if actualExists != oldExists || !reflect.DeepEqual(actual, old) {
				return fmt.Errorf("конфлікт на %s: Kratos змінив цей шлях після попередньої конфігурації", child)
			}
			if newExists {
				*changes = append(*changes, Operation{Op: "add", Path: child, Value: newValue})
			} else {
				*changes = append(*changes, Operation{Op: "remove", Path: child})
			}
		}
		return nil
	}
	if reflect.DeepEqual(current, after) {
		return nil
	}
	if !reflect.DeepEqual(current, before) {
		return fmt.Errorf("конфлікт на %s: Kratos змінив цей шлях після попередньої конфігурації", path)
	}
	*changes = append(*changes, Operation{Op: "add", Path: path, Value: after})
	return nil
}

func escape(segment string) string {
	return strings.ReplaceAll(strings.ReplaceAll(segment, "~", "~0"), "/", "~1")
}
