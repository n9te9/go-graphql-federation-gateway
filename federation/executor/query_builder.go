package executor

import (
	"strings"

	"github.com/n9te9/federation-gateway/federation/planner"
)

type QueryBuilder interface {
	Build(step *planner.Step, entities []Entity) (string, map[string]any, error)
}

type Entity map[string]any

type queryBuilder struct{}

func NewQueryBuilder() *queryBuilder {
	return &queryBuilder{}
}

func (qb *queryBuilder) Build(step *planner.Step, entities []Entity) (string, map[string]any, error) {
	if step.SubGraph.IsBase {
		return qb.buildBaseQuery(step)
	}

	return qb.buildFetchEntitiesQuery(step, entities)
}

func (qb *queryBuilder) buildFetchEntitiesQuery(step *planner.Step, entities []Entity) (string, map[string]any, error) {
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

	var reps []any
	for _, e := range entities {
		reps = append(reps, e)
	}

	return builder.String(), map[string]any{"representations": reps}, nil
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
