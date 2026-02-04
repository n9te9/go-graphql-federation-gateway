package executor

import (
	"fmt"
	"strings"

	"github.com/n9te9/go-graphql-federation-gateway/federation/planner"
	"github.com/n9te9/goliteql/schema"
)

type QueryBuilder interface {
	Build(step *planner.Step, entities Entities, variables map[string]any) (string, map[string]any, error)
}

type Entities []map[string]any

type queryBuilder struct{}

var _ QueryBuilder = (*queryBuilder)(nil)

func NewQueryBuilder() *queryBuilder {
	return &queryBuilder{}
}

func (qb *queryBuilder) Build(step *planner.Step, entities Entities, variables map[string]any) (string, map[string]any, error) {
	if step.IsBase() {
		return qb.buildBaseQuery(step, variables)
	}
	return qb.buildFetchEntitiesQuery(step, entities, variables)
}

func (qb *queryBuilder) buildBaseQuery(step *planner.Step, variables map[string]any) (string, map[string]any, error) {
	var bodyBuilder strings.Builder
	subgraphVars := make(map[string]any)
	var varDefs []string
	varIdx := 0

	opKeyword := step.OperationType
	if opKeyword == "" {
		opKeyword = "query"
	}

	parentType := "Query"
	if opKeyword == "mutation" {
		parentType = "Mutation"
	}

	rootFieldName := step.RootFields[0]
	bodyBuilder.WriteString("\t" + rootFieldName)

	if args, ok := step.RootArguments[rootFieldName]; ok && len(args) > 0 {
		qb.writeArguments(&bodyBuilder, args, variables, &varIdx, &varDefs, subgraphVars, step, parentType, rootFieldName)
	}

	bodyBuilder.WriteString(" {\n")
	if err := qb.writeSelections(&bodyBuilder, step.Selections, variables, "\t\t", &varIdx, &varDefs, subgraphVars, step); err != nil {
		return "", nil, err
	}
	bodyBuilder.WriteString("\t}\n")

	var header strings.Builder
	header.WriteString(opKeyword)
	if len(varDefs) > 0 {
		header.WriteString("(")
		header.WriteString(strings.Join(varDefs, ", "))
		header.WriteString(")")
	}
	header.WriteString(" {\n")

	return header.String() + bodyBuilder.String() + "}", subgraphVars, nil
}

func (qb *queryBuilder) buildFetchEntitiesQuery(step *planner.Step, entities Entities, variables map[string]any) (string, map[string]any, error) {
	var builder strings.Builder
	subgraphVars := make(map[string]any)
	var varDefs []string
	varIdx := 0

	builder.WriteString("query ($representations: [_Any!]!) {\n")
	builder.WriteString("\t_entities(representations: $representations) {\n")

	selectionsByParent := make(map[string][]*planner.Selection)
	var parentTypes []string
	for _, sel := range step.Selections {
		if _, ok := selectionsByParent[sel.ParentType]; !ok {
			parentTypes = append(parentTypes, sel.ParentType)
		}
		selectionsByParent[sel.ParentType] = append(selectionsByParent[sel.ParentType], sel)
	}

	for _, parentType := range parentTypes {
		builder.WriteString("\t\t... on " + parentType + " {\n")
		if err := qb.writeSelections(&builder, selectionsByParent[parentType], variables, "\t\t\t", &varIdx, &varDefs, subgraphVars, step); err != nil {
			return "", nil, err
		}
		builder.WriteString("\t\t}\n")
	}

	builder.WriteString("\t}\n")
	builder.WriteString("}")

	var representations []any
	for _, e := range entities {
		representations = append(representations, e)
	}
	subgraphVars["representations"] = representations

	return builder.String(), subgraphVars, nil
}

func (qb *queryBuilder) writeSelections(sb *strings.Builder, selections []*planner.Selection, variables map[string]any, indent string, varIdx *int, varDefs *[]string, subgraphVars map[string]any, step *planner.Step) error {
	for _, sel := range selections {
		lowParent := strings.ToLower(sel.ParentType)
		if lowParent == "query" || lowParent == "mutation" {
			if err := qb.writeSelections(sb, sel.SubSelections, variables, indent, varIdx, varDefs, subgraphVars, step); err != nil {
				return err
			}
			continue
		}

		sb.WriteString(indent + sel.Field)

		if len(sel.Arguments) > 0 {
			qb.writeArguments(sb, sel.Arguments, variables, varIdx, varDefs, subgraphVars, step, sel.ParentType, sel.Field)
		}

		if len(sel.SubSelections) > 0 {
			sb.WriteString(" {\n")
			if err := qb.writeSelections(sb, sel.SubSelections, variables, indent+"\t", varIdx, varDefs, subgraphVars, step); err != nil {
				return err
			}
			sb.WriteString(indent + "}")
		}
		sb.WriteString("\n")
	}
	return nil
}

func (qb *queryBuilder) writeArguments(sb *strings.Builder, args map[string]any, clientVars map[string]any, varIdx *int, varDefs *[]string, subgraphVars map[string]any, step *planner.Step, parentType, fieldName string) {
	if len(args) == 0 {
		return
	}
	sb.WriteString("(")
	var parts []string
	for name, val := range args {
		varName := fmt.Sprintf("v%d", *varIdx)
		resolvedVal := qb.resolveValue(val, clientVars)
		subgraphVars[varName] = resolvedVal

		argType := qb.getArgumentType(step, parentType, fieldName, name)

		*varDefs = append(*varDefs, fmt.Sprintf("$%s: %s", varName, argType))
		parts = append(parts, fmt.Sprintf("%s: $%s", name, varName))
		*varIdx++
	}
	sb.WriteString(strings.Join(parts, ", "))
	sb.WriteString(")")
}

func (qb *queryBuilder) getArgumentType(step *planner.Step, parentType, fieldName, argName string) string {
	for _, op := range step.SubGraph.Schema.Operations {
		if string(op.OperationType) == step.OperationType {
			for _, field := range op.Fields {
				if string(field.Name) == fieldName {
					for _, arg := range field.Arguments {
						if string(arg.Name) == argName {
							if arg.Type.Nullable {
								return string(arg.Type.Name)
							} else {
								return qb.resolveArgumentType(arg.Type)
							}
						}
					}
				}
			}
		}
	}

	return "Any!"
}

func (qb *queryBuilder) resolveArgumentType(argType *schema.FieldType) string {
	if argType.IsList {
		innerType := qb.resolveArgumentType(argType.ListType)
		if argType.Nullable {
			return fmt.Sprintf("[%s]", innerType)
		} else {
			return fmt.Sprintf("[%s]!", innerType)
		}
	}

	if argType.Nullable {
		return string(argType.Name)
	}

	return fmt.Sprintf("%s!", string(argType.Name))
}

func (qb *queryBuilder) resolveValue(val any, clientVars map[string]any) any {
	var s string
	switch v := val.(type) {
	case []byte:
		s = string(v)
	case string:
		s = v
	default:
		s = fmt.Sprintf("%v", v)
	}

	s = strings.Trim(s, `"' `)

	if strings.HasPrefix(s, "$") {
		varName := strings.TrimPrefix(s, "$")
		if actualValue, ok := clientVars[varName]; ok {
			return actualValue
		}
	}

	return s
}
