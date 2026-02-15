package executor

import (
	"fmt"
)

// Merge merges source data into target data at the specified path.
// This function implements the recursive merge logic as described in the design document.
// If path is empty, it merges at the root level.
// If path points to a list, it merges source elements into corresponding target elements.
// If path points to an object, it merges source fields into the target object.
func Merge(target map[string]interface{}, source interface{}, path []string) error {
	// Base case: if path is empty, merge at root level
	if len(path) == 0 {
		sourceMap, ok := source.(map[string]interface{})
		if !ok {
			return fmt.Errorf("source must be a map when path is empty")
		}
		for k, v := range sourceMap {
			target[k] = v
		}
		return nil
	}

	// Recursive case: navigate the path
	key := path[0]
	remainingPath := path[1:]

	value, exists := target[key]
	if !exists {
		// If key doesn't exist and we have remaining path, we need to create intermediate structure
		if len(remainingPath) > 0 {
			// Create an empty object/array as placeholder
			// We'll determine the type based on the source
			target[key] = make(map[string]interface{})
			value = target[key]
		} else {
			// If this is the last segment, merge source directly
			target[key] = source
			return nil
		}
	}

	// Check if value is a list
	if list, ok := value.([]interface{}); ok {
		sourceList, ok := source.([]interface{})
		if !ok {
			return fmt.Errorf("source must be a list when target is a list at path %v, got %T", path, source)
		}

		if len(list) != len(sourceList) {
			return fmt.Errorf("source and target list lengths do not match at path %v: target=%d, source=%d", path, len(list), len(sourceList))
		}

		// Merge each element
		for i := 0; i < len(list); i++ {
			targetElem, ok := list[i].(map[string]interface{})
			if !ok {
				return fmt.Errorf("target list element at index %d is not a map", i)
			}

			if len(remainingPath) == 0 {
				// Merge source into the element directly
				sourceElem, ok := sourceList[i].(map[string]interface{})
				if !ok {
					return fmt.Errorf("source list element at index %d is not a map", i)
				}
				for k, v := range sourceElem {
					targetElem[k] = v
				}
			} else {
				// Recursively merge into the element
				if err := Merge(targetElem, sourceList[i], remainingPath); err != nil {
					return err
				}
			}
		}
		return nil
	}

	// Check if value is an object
	if obj, ok := value.(map[string]interface{}); ok {
		if len(remainingPath) == 0 {
			// Merge source into the object directly
			sourceMap, ok := source.(map[string]interface{})
			if !ok {
				return fmt.Errorf("source must be a map when merging into an object")
			}
			for k, v := range sourceMap {
				obj[k] = v
			}
			return nil
		}

		// Recursively merge into the object
		return Merge(obj, source, remainingPath)
	}

	return fmt.Errorf("unsupported type at path %v", path)
}