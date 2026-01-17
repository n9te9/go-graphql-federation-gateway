package executor

import (
	"strings"

	"github.com/n9te9/federation-gateway/federation/planner"
)

type QueryBuilder interface {
	Build(step *planner.Step, entities Entities) (string, map[string]any, error)
}

type Entities []map[string]any

type queryBuilder struct{}

var _ QueryBuilder = (*queryBuilder)(nil)

func NewQueryBuilder() *queryBuilder {
	return &queryBuilder{}
}

func (qb *queryBuilder) Build(step *planner.Step, entities Entities) (string, map[string]any, error) {
	if step.IsBase {
		return qb.buildBaseQuery(step)
	}

	return qb.buildFetchEntitiesQuery(step, entities)
}

func (qb *queryBuilder) buildFetchEntitiesQuery(step *planner.Step, entities Entities) (string, map[string]any, error) {
	var builder strings.Builder

	builder.WriteString("query ($representations: [_Any!]!) {\n")
	builder.WriteString("\t_entities(representations: $representations) {\n")

	selectionMap := make(map[string][]string)
	for _, sel := range step.Selections {
		selectionMap[sel.ParentType] = append(selectionMap[sel.ParentType], sel.Field)
	}

	for parentType, fields := range selectionMap {
		builder.WriteString("\t\t... on " + parentType + " {\n")
		for _, field := range fields {
			builder.WriteString("\t\t\t" + field + "\n")
		}
		builder.WriteString("\t\t}\n")
	}
	builder.WriteString("\t}\n")
	builder.WriteString("}")

	var resp []any
	for _, e := range entities {
		resp = append(resp, e)
	}

	return builder.String(), map[string]any{"representations": resp}, nil
}

func (qb *queryBuilder) buildBaseQuery(step *planner.Step) (string, map[string]any, error) {
	var builder strings.Builder

	builder.WriteString("query {\n")
	builder.WriteString("\t" + step.SubGraph.BaseName + " {\n")
	for _, sel := range step.Selections {
		builder.WriteString("\t\t" + sel.Field + "\n")
	}
	builder.WriteString("\t}\n")
	builder.WriteString("}")

	return builder.String(), nil, nil
}
