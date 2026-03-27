package merger

import (
	"fmt"

	"github.com/n9te9/go-graphql-federation-gateway/federation/planner"
)

type Merger interface {
	Merge(target map[string]any, source any, path []string) error
	MergeEntities(rootResult, stepResult map[string]any, step *planner.StepV2) (map[string]any, error)
}

type merger struct{}

var _ Merger = (*merger)(nil)

func NewMerger() *merger {
	return &merger{}
}

// Merge merges source data into target data at the specified path.
// This function implements the recursive merge logic as described in the design document.
// If path is empty, it merges at the root level.
// If path points to a list, it merges source elements into corresponding target elements.
// If path points to an object, it merges source fields into the target object.
func (m *merger) Merge(target map[string]any, source any, path []string) error {
	// Base case: if path is empty, merge at root level
	if len(path) == 0 {
		sourceMap, ok := source.(map[string]any)
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
			target[key] = make(map[string]any)
			value = target[key]
		} else {
			// If this is the last segment, merge source directly
			target[key] = source
			return nil
		}
	}

	// Check if value is a list
	if list, ok := value.([]any); ok {
		sourceList, ok := source.([]any)
		if !ok {
			return fmt.Errorf("source must be a list when target is a list at path %v, got %T", path, source)
		}

		if len(list) != len(sourceList) {
			return fmt.Errorf("source and target list lengths do not match at path %v: target=%d, source=%d", path, len(list), len(sourceList))
		}

		// Merge each element
		for i := 0; i < len(list); i++ {
			targetElem, ok := list[i].(map[string]any)
			if !ok {
				return fmt.Errorf("target list element at index %d is not a map", i)
			}

			if len(remainingPath) == 0 {
				// Merge source into the element directly
				sourceElem, ok := sourceList[i].(map[string]any)
				if !ok {
					return fmt.Errorf("source list element at index %d is not a map", i)
				}
				for k, v := range sourceElem {
					targetElem[k] = v
				}
			} else {
				// Recursively merge into the element
				if err := m.Merge(targetElem, sourceList[i], remainingPath); err != nil {
					return err
				}
			}
		}
		return nil
	}

	// Check if value is an object
	if obj, ok := value.(map[string]any); ok {
		if len(remainingPath) == 0 {
			// Merge source into the object directly
			sourceMap, ok := source.(map[string]any)
			if !ok {
				return fmt.Errorf("source must be a map when merging into an object")
			}
			for k, v := range sourceMap {
				obj[k] = v
			}
			return nil
		}

		// Recursively merge into the object
		return m.Merge(obj, source, remainingPath)
	}

	return fmt.Errorf("unsupported type at path %v", path)
}

func (m *merger) MergeEntities(rootResult, result map[string]any, step *planner.StepV2) (map[string]any, error) {
	rootResultData, ok := rootResult["data"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("root result does not contain 'data' field or is not a map")
	}

	stepResultData, ok := result["data"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("step result does not contain 'data' field or is not a map")
	}

	entitiesData, ok := stepResultData["_entities"]
	if !ok {
		return rootResult, nil
	}

	mergePath := step.MergePath()

	current, index := m.extractFirstArray(mergePath, rootResultData)
	if index >= 0 {
		// We encountered an array - need to handle nested array merging
		entities, ok := entitiesData.([]any)
		if !ok {
			return nil, fmt.Errorf("entities data is not an array")
		}

		array, ok := current.([]any)
		if !ok {
			return nil, fmt.Errorf("expected array at merge path %v", mergePath[:index+1])
		}

		// The remaining path after the array
		remainingPath := mergePath[index+1:]

		// Merge entities into the nested structure
		entityIndex := 0
		for _, elem := range array {
			elemMap, ok := elem.(map[string]any)
			if !ok {
				continue
			}

			entityIndex = m.mergeIntoNestedArrays(elemMap, entities, remainingPath, entityIndex, step)
		}
	} else if current == nil {
		// Path doesn't exist yet, treat as single object and let Merge handle it
		entities, ok := entitiesData.([]any)
		if !ok || len(entities) == 0 {
			return nil, nil
		}

		firstEntity, ok := entities[0].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("first entity is not a map")
		}

		if err := m.Merge(rootResultData, firstEntity, mergePath); err != nil {
			return nil, fmt.Errorf("failed to merge entity object: %w", err)
		}
	} else if _, isArray := current.([]any); isArray {
		// Target is an array, merge entities directly
		if err := m.Merge(rootResultData, entitiesData, mergePath); err != nil {
			return nil, fmt.Errorf("failed to merge entities array: %w", err)
		}
	} else {
		// Target is a single object, merge first entity
		entities, ok := entitiesData.([]any)
		if !ok || len(entities) == 0 {
			return nil, nil
		}

		// For single object, merge the first entity's fields
		firstEntity, ok := entities[0].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("first entity is not a map")
		}

		if err := m.Merge(rootResultData, firstEntity, mergePath); err != nil {
			return nil, fmt.Errorf("failed to merge entity object: %w", err)
		}
	}

	return rootResult, nil
}

// mergeIntoNestedArrays recursively merges entities into potentially nested array structures
// Returns the next entity index to use
func (m *merger) mergeIntoNestedArrays(
	current map[string]any,
	entities []any,
	path []string,
	entityIndex int,
	step *planner.StepV2,
) int {
	if len(path) == 0 {
		// Reached the target - merge the entity here
		if entityIndex < len(entities) {
			if entityMap, ok := entities[entityIndex].(map[string]any); ok {
				// Deep merge entity fields into current
				// Use the Merger interface to properly handle nested structures
				m.Merge(current, entityMap, []string{})
			}
			return entityIndex + 1
		}
		return entityIndex
	}

	segment := path[0]
	remainingPath := path[1:]

	next, exists := current[segment]
	if !exists {
		return entityIndex
	}

	// Check if next is an array
	if arr, isArray := next.([]any); isArray {
		// Process each array element
		for _, elem := range arr {
			if elemMap, ok := elem.(map[string]any); ok {
				entityIndex = m.mergeIntoNestedArrays(elemMap, entities, remainingPath, entityIndex, step)
			}
		}
	} else if nextMap, ok := next.(map[string]any); ok {
		// Continue navigating
		entityIndex = m.mergeIntoNestedArrays(nextMap, entities, remainingPath, entityIndex, step)
	}

	return entityIndex
}

// extractFirstArray navigates to the target field to check if it's an array or object
// Also collect all array positions in the path for nested array handling
func (m *merger) extractFirstArray(mergePath []string, rootResultData map[string]any) (any, int) {
	var current any = rootResultData
	var firstArrayIndex = -1

	for i, segment := range mergePath {
		if currentMap, ok := current.(map[string]any); ok {
			if next, exists := currentMap[segment]; exists {
				current = next

				if _, isArray := current.([]any); isArray {
					if firstArrayIndex < 0 {
						firstArrayIndex = i
					}
					break
				}
			} else {
				current = nil
				break
			}
		} else {
			current = nil
			break
		}
	}

	return current, firstArrayIndex
}
