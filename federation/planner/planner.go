package planner

import (
	"errors"
	"fmt"
	"strings"

	"github.com/n9te9/federation-gateway/federation/graph"
	"github.com/n9te9/goliteql/query"
	"github.com/n9te9/goliteql/schema"
)

type Planner interface {
	Plan(doc *query.Document) (*Plan, error)
}

type planner struct {
	superGraph *graph.SuperGraph
}

type Step struct {
	ID       int
	SubGraph *graph.SubGraph

	IsBase     bool
	Selections []*Selection
	DependsOn  []int
	Done       chan struct{}

	Err error
}

func (s *Step) findExtendKeys() [][]string {
	for _, ext := range s.SubGraph.Schema.Extends {
		switch t := ext.(type) {
		case *schema.TypeDefinition:
			directives := schema.Directives(t.Directives)
			keyDirective := directives.Get([]byte("key"))
			if keyDirective == nil {
				return nil
			}

			return s.findKeyDirectiveFieldArguments(keyDirective.Arguments)
		}
	}

	return nil
}

func (s *Step) findKeyDirectiveFieldArguments(keyDirectiveArgs []*schema.DirectiveArgument) [][]string {
	ret := make([][]string, 0)
	for _, arg := range keyDirectiveArgs {
		if string(arg.Name) == "fields" {
			v := strings.Trim(string(arg.Value), `"`)
			keys := strings.Split(v, " ")
			ret = append(ret, keys)
		}
	}

	return ret
}

func (s *Step) hasField(fieldName string) bool {
	for _, t := range s.SubGraph.Schema.Types {
		for _, field := range t.Fields {
			if fieldName == string(field.Name) {
				return true
			}
		}
	}

	return false
}

type StepStatus int

const (
	Pending StepStatus = iota
	Running
	Completed
	Failed
)

func NewPlanner(superGraph *graph.SuperGraph) *planner {
	return &planner{
		superGraph: superGraph,
	}
}

type Steps []*Step

func (s Steps) findDependedStep(step *Step) []int {
	dependKeys := step.findExtendKeys()

	ret := make([]int, 0)
	for _, st := range s {
		if st == step {
			continue
		}

		for _, keys := range dependKeys {
			for _, key := range keys {
				if st.hasField(key) {
					ret = append(ret, st.ID)
				}
			}
		}
	}

	return ret
}

type Plan struct {
	Steps          Steps
	RootSelections []*Selection
}

func (p *Plan) GetStepByID(id int) *Step {
	for _, step := range p.Steps {
		if step.ID == id {
			return step
		}
	}

	return nil
}

func (p *Plan) Selections() []*Selection {
	ret := make([]*Selection, 0)
	for _, step := range p.Steps {
		ret = append(ret, step.Selections...)
	}

	return ret
}

func (p *planner) Plan(doc *query.Document) (*Plan, error) {
	op := p.superGraph.GetOperation(doc)
	if len(op.Selections) == 0 {
		return nil, errors.New("empty selection")
	}

	schemaTypeDefinition, queryField, err := p.findOperationField(op)
	selections, err := p.extractSelections(op.Selections[0].GetSelections(), string(schemaTypeDefinition.Name))
	if err != nil {
		return nil, err
	}

	var rootSelections []*Selection
	for _, sel := range op.Selections {
		f, ok := sel.(*query.Field)
		if !ok {
			continue
		}

		if string(f.Name) != string(queryField.Name) {
			continue
		}

		rootSelections = append(rootSelections, &Selection{
			ParentType:    string(schemaTypeDefinition.Name),
			Field:         string(f.Name),
			SubSelections: selections,
		})
	}

	keys := p.generateFieldKeys(schemaTypeDefinition, queryField)

	return p.plan(string(queryField.Name), keys, schemaTypeDefinition, rootSelections), nil
}

func (p *planner) extractSelections(selection []query.Selection, parentType string) ([]*Selection, error) {
	ret := make([]*Selection, 0)
	for _, sel := range selection {
		f, ok := sel.(*query.Field)
		if !ok {
			continue
		}

		selection := &Selection{
			ParentType: parentType,
			Field:      string(f.Name),
		}

		fieldTypeName, err := p.getFieldTypeName(parentType, string(f.Name))
		if err != nil {
			return nil, err
		}

		if len(f.Selections) > 0 {
			subs, err := p.extractSelections(f.Selections, fieldTypeName)
			if err != nil {
				return nil, err
			}
			selection.SubSelections = subs
		}

		ret = append(ret, selection)
	}

	return ret, nil
}

func (p *planner) getFieldTypeName(parentTypename, fieldName string) (string, error) {
	td, ok := p.superGraph.Schema.Indexes.TypeIndex[parentTypename]

	if ok {
		for _, field := range td.Fields {
			if string(field.Name) == fieldName {
				return string(field.Type.GetRootType().Name), nil
			}
		}

		for _, exttd := range p.superGraph.Schema.Extends {
			if extTypeDef, ok := exttd.(*schema.TypeDefinition); ok {
				if string(extTypeDef.Name) != parentTypename {
					continue
				}

				for _, field := range extTypeDef.Fields {
					if string(field.Name) == fieldName {
						return string(field.Type.GetRootType().Name), nil
					}
				}
			}
		}
	}

	return "", fmt.Errorf("field %s not found in type %s", fieldName, parentTypename)
}

func (p *planner) findOperationField(op *query.Operation) (*schema.TypeDefinition, *query.Field, error) {
	for _, schemaOperation := range p.superGraph.Schema.Operations {
		for _, field := range schemaOperation.Fields {
			for _, sel := range op.Selections {
				f, ok := sel.(*query.Field)
				if !ok {
					continue
				}

				if string(field.Name) == string(f.Name) {
					return p.superGraph.Schema.Indexes.TypeIndex[string(field.Type.GetRootType().Name)], f, nil
				}
			}
		}
	}
	return nil, nil, errors.New("not found query operation")
}

func (p *planner) generateFieldKeys(typeDefinition *schema.TypeDefinition, field *query.Field) []string {
	ret := make([]string, 0)
	for _, sel := range field.Selections {
		f, ok := sel.(*query.Field)
		if !ok {
			continue
		}

		ret = append(ret, fmt.Sprintf("%s.%s", typeDefinition.Name, f.Name))
	}

	return ret
}

type Selection struct {
	ParentType string
	Field      string

	SubSelections []*Selection
}

func (p *planner) plan(queryName string, keys []string, typeDefinition *schema.TypeDefinition, rootSelections []*Selection) *Plan {
	plan := &Plan{
		Steps:          make([]*Step, 0),
		RootSelections: rootSelections,
	}

	for _, subGraph := range p.superGraph.SubGraphs {
		isBase := false
		if _, ok := subGraph.OwnershipTypes[string(typeDefinition.Name)]; ok {
			isBase = true
			subGraph.BaseName = queryName
		}

		sels := make([]*Selection, 0)
		for _, key := range keys {
			if _, exist := subGraph.OwnershipFieldMap()[key]; exist {
				parts := strings.SplitN(key, ".", 2)
				parentType := parts[0]
				field := parts[1]

				sels = append(sels, &Selection{
					ParentType: parentType,
					Field:      field,
				})
			}
		}

		if len(sels) == 0 && !isBase {
			continue
		}

		plan.Steps = append(plan.Steps, &Step{
			SubGraph:   subGraph,
			Selections: sels,
			DependsOn:  nil,
			IsBase:     isBase,
			Err:        nil,
			Done:       make(chan struct{}),
		})
	}

	p.solveDependency(plan)

	return plan
}

func (p *planner) solveDependency(plan *Plan) {
	for i, step := range plan.Steps {
		step.ID = i
	}

	for _, step := range plan.Steps {
		dependsOn := plan.Steps.findDependedStep(step)
		step.DependsOn = dependsOn
	}
}
