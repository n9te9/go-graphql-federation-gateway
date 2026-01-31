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
	if len(step.DependsOn) == 0 {
		return qb.buildBaseQuery(step)
	}

	return qb.buildFetchEntitiesQuery(step, entities)
}

func (qb *queryBuilder) buildFetchEntitiesQuery(step *planner.Step, entities Entities) (string, map[string]any, error) {
	var builder strings.Builder

	builder.WriteString("query ($representations: [_Any!]!) {\n")
	builder.WriteString("\t_entities(representations: $representations) {\n")

	for _, sel := range step.Selections {
		builder.WriteString("\t\t... on " + sel.ParentType + " {\n")
		if err := qb.writeSelections(&builder, step.Selections, "\t\t\t"); err != nil {
			return "", nil, err
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

func (qb *queryBuilder) writeSelections(sb *strings.Builder, selections []*planner.Selection, indent string) error {
	for _, sel := range selections {
		sb.WriteString(indent + sel.Field)

		if len(sel.SubSelections) > 0 {
			sb.WriteString(" {\n")
			if err := qb.writeSelections(sb, sel.SubSelections, indent+"\t"); err != nil {
				return err
			}
			sb.WriteString(indent + "}")
		}

		sb.WriteString("\n")
	}
	return nil
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
