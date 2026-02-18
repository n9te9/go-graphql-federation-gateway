package executor

import (
	"fmt"
	"strings"

	"github.com/n9te9/go-graphql-federation-gateway/federation/graph"
	"github.com/n9te9/go-graphql-federation-gateway/federation/planner"
	"github.com/n9te9/graphql-parser/ast"
)

// QueryBuilderV2 builds GraphQL queries from steps.
type QueryBuilderV2 struct {
	superGraph *graph.SuperGraphV2
}

// NewQueryBuilderV2 creates a new QueryBuilderV2 instance.
func NewQueryBuilderV2(superGraph *graph.SuperGraphV2) *QueryBuilderV2 {
	return &QueryBuilderV2{
		superGraph: superGraph,
	}
}

// Build generates a GraphQL query string and variables from a step.
// For root queries (StepTypeQuery), it generates a regular query or mutation.
// For entity queries (StepTypeEntity), it generates an _entities query with representations.
func (qb *QueryBuilderV2) Build(
	step *planner.StepV2,
	representations []map[string]interface{},
	variables map[string]interface{},
	operationType string,
) (string, map[string]interface{}, error) {
	if step.StepType == planner.StepTypeQuery {
		return qb.buildRootQuery(step, variables, operationType)
	}
	return qb.buildEntityQuery(step, representations, variables)
}

// buildRootQuery builds a root query or mutation from selections.
func (qb *QueryBuilderV2) buildRootQuery(
	step *planner.StepV2,
	variables map[string]interface{},
	operationType string,
) (string, map[string]interface{}, error) {
	var sb strings.Builder

	// Collect variables used in the selection set
	varNames := qb.collectVariables(step.SelectionSet)

	// Default to "query" if not specified
	if operationType == "" {
		operationType = "query"
	}

	// Build query/mutation header with variable definitions
	sb.WriteString(operationType)
	if len(varNames) > 0 {
		sb.WriteString(" (")
		first := true
		for _, varName := range varNames {
			if !first {
				sb.WriteString(", ")
			}
			first = false
			sb.WriteString("$")
			sb.WriteString(varName)
			sb.WriteString(": ")
			// Infer type from variable value or use default
			varType := qb.inferVariableType(varName, variables, step)
			sb.WriteString(varType)
		}
		sb.WriteString(")")
	}
	sb.WriteString(" {\n")

	// Write selections
	for _, sel := range step.SelectionSet {
		if err := qb.writeSelection(&sb, sel, "\t", step, step.ParentType); err != nil {
			return "", nil, err
		}
	}

	sb.WriteString("}")
	return sb.String(), variables, nil
}

// collectVariables collects all variable names used in the selection set.
func (qb *QueryBuilderV2) collectVariables(selections []ast.Selection) []string {
	vars := make(map[string]bool)
	qb.collectVariablesRecursive(selections, vars)

	// Convert map to sorted slice for consistent output
	result := make([]string, 0, len(vars))
	for v := range vars {
		result = append(result, v)
	}
	return result
}

// collectVariablesRecursive recursively collects variables from selections.
func (qb *QueryBuilderV2) collectVariablesRecursive(selections []ast.Selection, vars map[string]bool) {
	for _, sel := range selections {
		switch s := sel.(type) {
		case *ast.Field:
			// Check arguments for variables
			for _, arg := range s.Arguments {
				qb.collectVariablesFromValue(arg.Value, vars)
			}
			// Recurse into sub-selections
			if len(s.SelectionSet) > 0 {
				qb.collectVariablesRecursive(s.SelectionSet, vars)
			}
		case *ast.InlineFragment:
			if len(s.SelectionSet) > 0 {
				qb.collectVariablesRecursive(s.SelectionSet, vars)
			}
		}
	}
}

// collectVariablesFromValue collects variables from a value.
func (qb *QueryBuilderV2) collectVariablesFromValue(val ast.Value, vars map[string]bool) {
	switch v := val.(type) {
	case *ast.Variable:
		vars[v.Name] = true
	case *ast.ListValue:
		for _, item := range v.Values {
			qb.collectVariablesFromValue(item, vars)
		}
	case *ast.ObjectValue:
		for _, field := range v.Fields {
			qb.collectVariablesFromValue(field.Value, vars)
		}
	}
}

// inferVariableType infers the type of a variable from its value or schema.
func (qb *QueryBuilderV2) inferVariableType(varName string, variables map[string]interface{}, step *planner.StepV2) string {
	// Try to get type from schema if SubGraph is available
	if step.SubGraph != nil && step.SubGraph.Schema != nil {
		if varType := qb.getVariableTypeFromSchema(varName, step); varType != "" {
			return varType
		}
	}

	// Fallback: infer from value
	if val, ok := variables[varName]; ok {
		switch val.(type) {
		case string:
			return "String"
		case int, int32, int64:
			return "Int"
		case float32, float64:
			return "Float"
		case bool:
			return "Boolean"
		}
	}

	// Default to String
	return "String"
}

// getVariableTypeFromSchema gets the variable type from the schema.
func (qb *QueryBuilderV2) getVariableTypeFromSchema(varName string, step *planner.StepV2) string {
	// Find the argument that uses this variable
	for _, sel := range step.SelectionSet {
		if field, ok := sel.(*ast.Field); ok {
			for _, arg := range field.Arguments {
				if variable, ok := arg.Value.(*ast.Variable); ok && variable.Name == varName {
					// Get the argument type from schema
					return qb.getArgumentTypeFromSchema(step, step.ParentType, field.Name.String(), arg.Name.String())
				}
			}
		}
	}
	return ""
}

// getArgumentTypeFromSchema gets the argument type from schema.
func (qb *QueryBuilderV2) getArgumentTypeFromSchema(step *planner.StepV2, parentType, fieldName, argName string) string {
	if step.SubGraph == nil || step.SubGraph.Schema == nil {
		return ""
	}

	// Find the parent type definition
	for _, def := range step.SubGraph.Schema.Definitions {
		if objType, ok := def.(*ast.ObjectTypeDefinition); ok && objType.Name.String() == parentType {
			// Find the field
			for _, field := range objType.Fields {
				if field.Name.String() == fieldName {
					// Find the argument
					for _, arg := range field.Arguments {
						if arg.Name.String() == argName {
							return arg.Type.String()
						}
					}
				}
			}
		}
	}

	return ""
}

// getFieldType gets the field type name from schema.
func (qb *QueryBuilderV2) getFieldType(step *planner.StepV2, parentType, fieldName string) string {
	if step.SubGraph == nil || step.SubGraph.Schema == nil {
		return ""
	}

	// Find the parent type definition
	for _, def := range step.SubGraph.Schema.Definitions {
		if objType, ok := def.(*ast.ObjectTypeDefinition); ok && objType.Name.String() == parentType {
			// Find the field
			for _, field := range objType.Fields {
				if field.Name.String() == fieldName {
					// Extract the base type name (without [] or !)
					return qb.extractBaseTypeName(field.Type.String())
				}
			}
		}
	}

	return ""
}

// extractBaseTypeName extracts the base type name from a type string.
// For example: "[Product!]!" -> "Product", "String!" -> "String"
func (qb *QueryBuilderV2) extractBaseTypeName(typeStr string) string {
	// Remove [ ] and !
	cleaned := strings.Trim(typeStr, "[]!")
	cleaned = strings.ReplaceAll(cleaned, "[", "")
	cleaned = strings.ReplaceAll(cleaned, "]", "")
	cleaned = strings.ReplaceAll(cleaned, "!", "")
	return cleaned
}

// buildEntityQuery builds an _entities query with representations.
func (qb *QueryBuilderV2) buildEntityQuery(
	step *planner.StepV2,
	representations []map[string]interface{},
	variables map[string]interface{},
) (string, map[string]interface{}, error) {
	if len(representations) == 0 {
		return "", nil, fmt.Errorf("representations cannot be empty for entity query")
	}

	var sb strings.Builder
	sb.WriteString("query ($representations: [_Any!]!) {\n")
	sb.WriteString("\t_entities(representations: $representations) {\n")

	// Write inline fragment
	sb.WriteString("\t\t... on ")
	sb.WriteString(step.ParentType)
	sb.WriteString(" {\n")

	// Write selections
	for _, sel := range step.SelectionSet {
		if err := qb.writeSelection(&sb, sel, "\t\t\t", step, step.ParentType); err != nil {
			return "", nil, err
		}
	}

	sb.WriteString("\t\t}\n")
	sb.WriteString("\t}\n")
	sb.WriteString("}")

	// Add representations to variables
	newVariables := make(map[string]interface{})
	for k, v := range variables {
		newVariables[k] = v
	}
	newVariables["representations"] = representations

	return sb.String(), newVariables, nil
}

// writeSelection writes a selection to the string builder.
func (qb *QueryBuilderV2) writeSelection(sb *strings.Builder, sel ast.Selection, indent string, step *planner.StepV2, parentType string) error {
	switch s := sel.(type) {
	case *ast.Field:
		fieldName := s.Name.String()

		// Note: We don't skip boundary fields here because the planner has already
		// divided the query into appropriate steps. Each step only contains selections
		// that should be executed on that subgraph.

		sb.WriteString(indent)

		// Write alias if present
		if s.Alias != nil && s.Alias.String() != "" {
			sb.WriteString(s.Alias.String())
			sb.WriteString(": ")
		}

		sb.WriteString(s.Name.String())

		// Write arguments if present
		if len(s.Arguments) > 0 {
			sb.WriteString("(")
			for i, arg := range s.Arguments {
				if i > 0 {
					sb.WriteString(", ")
				}
				sb.WriteString(arg.Name.String())
				sb.WriteString(": ")
				qb.writeValue(sb, arg.Value)
			}
			sb.WriteString(")")
		}

		// Write sub-selections if present
		if len(s.SelectionSet) > 0 {
			// Get the field type for sub-selections
			fieldType := qb.getFieldType(step, parentType, fieldName)
			sb.WriteString(" {\n")
			for _, subSel := range s.SelectionSet {
				if err := qb.writeSelection(sb, subSel, indent+"\t", step, fieldType); err != nil {
					return err
				}
			}
			sb.WriteString(indent)
			sb.WriteString("}")
		}
		sb.WriteString("\n")

	case *ast.InlineFragment:
		sb.WriteString(indent)
		sb.WriteString("... on ")
		typeCondition := s.TypeCondition.Name.String()
		sb.WriteString(typeCondition)
		sb.WriteString(" {\n")
		for _, subSel := range s.SelectionSet {
			if err := qb.writeSelection(sb, subSel, indent+"\t", step, typeCondition); err != nil {
				return err
			}
		}
		sb.WriteString(indent)
		sb.WriteString("}\n")

	case *ast.FragmentSpread:
		sb.WriteString(indent)
		sb.WriteString("...")
		sb.WriteString(s.Name.String())
		sb.WriteString("\n")
	}

	return nil
}

// writeValue writes a value to the string builder.
func (qb *QueryBuilderV2) writeValue(sb *strings.Builder, val ast.Value) {
	switch v := val.(type) {
	case *ast.StringValue:
		sb.WriteString("\"")
		sb.WriteString(v.Value)
		sb.WriteString("\"")
	case *ast.IntValue:
		sb.WriteString(fmt.Sprintf("%d", v.Value))
	case *ast.FloatValue:
		sb.WriteString(fmt.Sprintf("%f", v.Value))
	case *ast.BooleanValue:
		sb.WriteString(fmt.Sprintf("%t", v.Value))
	case *ast.Variable:
		sb.WriteString("$")
		sb.WriteString(v.Name)
	case *ast.ListValue:
		sb.WriteString("[")
		for i, item := range v.Values {
			if i > 0 {
				sb.WriteString(", ")
			}
			qb.writeValue(sb, item)
		}
		sb.WriteString("]")
	case *ast.ObjectValue:
		sb.WriteString("{")
		for i, field := range v.Fields {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(field.Name.String())
			sb.WriteString(": ")
			qb.writeValue(sb, field.Value)
		}
		sb.WriteString("}")
	case *ast.EnumValue:
		sb.WriteString(v.Value)
	default:
		sb.WriteString("null")
	}
}
