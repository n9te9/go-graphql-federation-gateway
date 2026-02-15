package executor_test

import (
	"testing"

	"github.com/n9te9/go-graphql-federation-gateway/federation/executor"
)

func TestMerge(t *testing.T) {
	tests := []struct {
		name     string
		target   map[string]interface{}
		source   interface{}
		path     []string
		expected map[string]interface{}
	}{
		{
			name: "Simple merge at root level",
			target: map[string]interface{}{
				"product": map[string]interface{}{
					"id": "1",
				},
			},
			source: map[string]interface{}{
				"reviews": []interface{}{
					map[string]interface{}{
						"body": "Great product",
					},
				},
			},
			path: []string{},
			expected: map[string]interface{}{
				"product": map[string]interface{}{
					"id": "1",
				},
				"reviews": []interface{}{
					map[string]interface{}{
						"body": "Great product",
					},
				},
			},
		},
		{
			name: "Merge into nested object",
			target: map[string]interface{}{
				"product": map[string]interface{}{
					"id": "1",
				},
			},
			source: map[string]interface{}{
				"name": "Product 1",
			},
			path: []string{"product"},
			expected: map[string]interface{}{
				"product": map[string]interface{}{
					"id":   "1",
					"name": "Product 1",
				},
			},
		},
		{
			name: "Merge into array elements",
			target: map[string]interface{}{
				"products": []interface{}{
					map[string]interface{}{
						"id": "1",
					},
					map[string]interface{}{
						"id": "2",
					},
				},
			},
			source: []interface{}{
				map[string]interface{}{
					"name": "Product 1",
				},
				map[string]interface{}{
					"name": "Product 2",
				},
			},
			path: []string{"products"},
			expected: map[string]interface{}{
				"products": []interface{}{
					map[string]interface{}{
						"id":   "1",
						"name": "Product 1",
					},
					map[string]interface{}{
						"id":   "2",
						"name": "Product 2",
					},
				},
			},
		},
		{
			name: "Merge deeply nested field",
			target: map[string]interface{}{
				"user": map[string]interface{}{
					"id": "1",
					"posts": []interface{}{
						map[string]interface{}{
							"id": "10",
						},
						map[string]interface{}{
							"id": "20",
						},
					},
				},
			},
			source: []interface{}{
				map[string]interface{}{
					"title": "Post 1",
				},
				map[string]interface{}{
					"title": "Post 2",
				},
			},
			path: []string{"user", "posts"},
			expected: map[string]interface{}{
				"user": map[string]interface{}{
					"id": "1",
					"posts": []interface{}{
						map[string]interface{}{
							"id":    "10",
							"title": "Post 1",
						},
						map[string]interface{}{
							"id":    "20",
							"title": "Post 2",
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := executor.Merge(tt.target, tt.source, tt.path)
			if err != nil {
				t.Fatalf("Merge() error = %v", err)
			}

			// Compare the result
			if !deepEqual(tt.target, tt.expected) {
				t.Errorf("Merge() result mismatch.\nExpected: %+v\nGot: %+v", tt.expected, tt.target)
			}
		})
	}
}

// deepEqual compares two interface{} values deeply
func deepEqual(a, b interface{}) bool {
	switch aVal := a.(type) {
	case map[string]interface{}:
		bMap, ok := b.(map[string]interface{})
		if !ok {
			return false
		}
		if len(aVal) != len(bMap) {
			return false
		}
		for k, v := range aVal {
			if !deepEqual(v, bMap[k]) {
				return false
			}
		}
		return true
	case []interface{}:
		bSlice, ok := b.([]interface{})
		if !ok {
			return false
		}
		if len(aVal) != len(bSlice) {
			return false
		}
		for i := range aVal {
			if !deepEqual(aVal[i], bSlice[i]) {
				return false
			}
		}
		return true
	default:
		return a == b
	}
}
