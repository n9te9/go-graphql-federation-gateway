package planner

import (
	"errors"
	"fmt"
	"strings"

	"github.com/n9te9/go-graphql-federation-gateway/federation/graph"
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

func (s *Step) hasField(fieldName string) bool {
	for _, t := range s.SubGraph.Schema.Types {
		for _, field := range t.Fields {
			if fieldName == string(field.Name) {
				if dir := field.Directives.Get([]byte("external")); dir != nil {
					return false
				}
				return true
			}
		}
	}

	for _, ext := range s.SubGraph.Schema.Extends {
		if t, ok := ext.(*schema.TypeDefinition); ok {
			for _, field := range t.Fields {
				if fieldName == string(field.Name) {
					if dir := field.Directives.Get([]byte("external")); dir != nil {
						return false
					}
					return true
				}
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

func (s Steps) IDs() []int {
	ret := make([]int, 0, len(s))
	for _, step := range s {
		ret = append(ret, step.ID)
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

	var rootTypeName string
	switch op.OperationType {
	case query.QueryOperation:
		rootTypeName = "Query"
	case query.MutationOperation:
		rootTypeName = "Mutation"
	case query.SubscriptionOperation:
		rootTypeName = "Subscription"
	}

	if p.superGraph.Schema.Definition != nil {
		if op.OperationType == query.QueryOperation && len(p.superGraph.Schema.Definition.Query) > 0 {
			rootTypeName = string(p.superGraph.Schema.Definition.Query)
		}
	}

	var rootSelections []*Selection
	for _, sel := range op.Selections {
		switch f := sel.(type) {
		case *query.Field:
			if string(f.Name) != string(queryField.Name) {
				continue
			}

			rootSelections = append(rootSelections, &Selection{
				ParentType:    rootTypeName,
				Field:         string(f.Name),
				SubSelections: selections,
			})
		}
	}

	plan := p.plan(string(queryField.Name), schemaTypeDefinition, rootSelections)
	if err := p.checkDAG(plan); err != nil {
		return nil, err
	}

	return plan, nil
}

func (p *planner) extractSelections(selection []query.Selection, parentType string) ([]*Selection, error) {
	ret := make([]*Selection, 0)
	for _, sel := range selection {
		switch f := sel.(type) {
		case *query.Field:
			fieldTypeName, err := p.getFieldTypeName(parentType, string(f.Name))
			if err != nil {
				return nil, err
			}

			selection := &Selection{
				ParentType: parentType,
				Field:      string(f.Name),
			}

			if len(f.Selections) > 0 {
				subs, err := p.extractSelections(f.Selections, fieldTypeName)
				if err != nil {
					return nil, err
				}
				selection.SubSelections = subs
			}
			ret = append(ret, selection)
		case *query.InlineFragment:
			typeCondition := string(f.TypeCondition)
			subs, err := p.extractSelections(f.Selections, typeCondition)
			if err != nil {
				return nil, err
			}

			ret = append(ret, subs...)
		}
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

type Selection struct {
	ParentType string
	Field      string

	SubSelections []*Selection
}

func (p *planner) plan(queryName string, typeDefinition *schema.TypeDefinition, rootSelections []*Selection) *Plan {
	plan := &Plan{
		Steps:          make([]*Step, 0),
		RootSelections: rootSelections,
	}

	var walk func(sels []*Selection, subGraph *graph.SubGraph) []*Selection
	walk = func(sels []*Selection, subGraph *graph.SubGraph) []*Selection {
		ret := make([]*Selection, 0)
		for _, sel := range sels {
			switch sel.ParentType {
			case "mutation":
				// TODO: implement for mutation
			case "subscription":
				// TODO: implement for subscription
			}

			subSelections := walk(sel.SubSelections, subGraph)

			key := fmt.Sprintf("%s.%s", sel.ParentType, sel.Field)

			isOwned := false
			if _, ok := subGraph.OwnershipFieldMap()[key]; ok {
				isOwned = true
			}

			if isOwned {
				selection := &Selection{
					ParentType:    sel.ParentType,
					Field:         sel.Field,
					SubSelections: subSelections,
				}
				ret = append(ret, selection)
			} else if len(subSelections) > 0 {
				ret = append(ret, subSelections...)
			}
		}

		return ret
	}

	for _, subGraph := range p.superGraph.SubGraphs {
		isBase := false
		if _, ok := subGraph.OwnershipTypes[string(typeDefinition.Name)]; ok {
			isBase = true
			subGraph.BaseName = queryName
		}

		sels := walk(rootSelections, subGraph)
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

	p.solveRequiresField(plan)
	p.solveDependency(plan)

	return plan
}

func (p *planner) solveDependency(plan *Plan) {
	for i, step := range plan.Steps {
		step.ID = i
	}

	for _, step := range plan.Steps {
		p.solveStepDependency(plan.Steps, step)
	}
}

func (p *planner) solveStepDependency(steps Steps, targetStep *Step) {
	requiredKeysMap := p.findRequiredKeys(targetStep)
	if len(requiredKeysMap) == 0 {
		return
	}

	for typeName, keys := range requiredKeysMap {
		for _, key := range keys {
			for _, providerStep := range steps {
				if targetStep == providerStep {
					continue
				}

				if providerStep.hasField(key) {
					updatedSelections, injected := p.injectKey(providerStep.Selections, typeName, key)

					if injected {
						providerStep.Selections = updatedSelections

						exists := false
						for _, id := range targetStep.DependsOn {
							if id == providerStep.ID {
								exists = true
								break
							}
						}

						if !exists && targetStep.ID != 0 {
							targetStep.DependsOn = append(targetStep.DependsOn, providerStep.ID)
						}
					}
				}
			}
		}
	}
}

func (p *planner) findOwnerStep(steps Steps, parentType, fieldName string) *Step {
	key := fmt.Sprintf("%s.%s", parentType, fieldName)
	for _, step := range steps {
		if _, ok := step.SubGraph.OwnershipFieldMap()[key]; ok {
			return step
		}
	}
	return nil
}

func (p *planner) solveRequiresField(plan *Plan) {
	for _, step := range plan.Steps {
		requiredFields := step.SubGraph.RequiredFields()
		if len(requiredFields) == 0 {
			continue
		}

		for parentType, fieldsMap := range requiredFields {
			for requiredSet := range fieldsMap {
				reqFields := strings.Fields(requiredSet)

				for _, reqField := range reqFields {
					ownerStep := p.findOwnerStep(plan.Steps, parentType, reqField)
					if ownerStep == nil {
						continue
					}

					newSelections, injected := p.injectField(ownerStep.Selections, parentType, reqField)

					if injected {
						ownerStep.Selections = newSelections
					}
				}
			}
		}
	}
}

func (p *planner) injectField(selections []*Selection, parentType, fieldName string) ([]*Selection, bool) {
	injectedAny := false

	isTargetContext := false
	for _, sel := range selections {
		if sel.ParentType == parentType {
			isTargetContext = true
			break
		}
	}

	if isTargetContext {
		exists := false
		for _, sel := range selections {
			if sel.Field == fieldName {
				exists = true
				break
			}
		}

		if !exists {
			selections = append(selections, &Selection{
				ParentType:    parentType,
				Field:         fieldName,
				SubSelections: []*Selection{},
			})
			injectedAny = true
		}
	}

	for _, sel := range selections {
		if len(sel.SubSelections) > 0 {
			updatedSubs, childInjected := p.injectField(sel.SubSelections, parentType, fieldName)
			if childInjected {
				sel.SubSelections = updatedSubs
				injectedAny = true
			}
		}
	}

	return selections, injectedAny
}

func (p *planner) findRequiredKeys(step *Step) map[string][]string {
	required := make(map[string][]string)

	var traverse func(sels []*Selection)
	traverse = func(sels []*Selection) {
		for _, sel := range sels {
			keys := p.getEntityKeys(step.SubGraph, sel.ParentType)
			if len(keys) > 0 {
				if _, ok := required[sel.ParentType]; !ok {
					required[sel.ParentType] = make([]string, 0)
				}
				required[sel.ParentType] = append(required[sel.ParentType], keys...)
			}

			if len(sel.SubSelections) > 0 {
				traverse(sel.SubSelections)
			}
		}
	}
	traverse(step.Selections)

	return required
}

func (p *planner) getEntityKeys(subGraph *graph.SubGraph, typeName string) []string {
	extract := func(dirs []*schema.Directive) []string {
		directives := schema.Directives(dirs)
		if keyDir := directives.Get([]byte("key")); keyDir != nil {
			return p.findKeyDirectiveFieldArguments(keyDir.Arguments)[0]
		}
		return nil
	}

	if t, ok := subGraph.Schema.Indexes.TypeIndex[typeName]; ok {
		if keys := extract(t.Directives); keys != nil {
			return keys
		}
	}

	for _, ext := range subGraph.Schema.Extends {
		if t, ok := ext.(*schema.TypeDefinition); ok {
			if string(t.Name) == typeName {
				if keys := extract(t.Directives); keys != nil {
					return keys
				}
			}
		}
	}

	return nil
}

func (p *planner) findKeyDirectiveFieldArguments(keyDirectiveArgs []*schema.DirectiveArgument) [][]string {
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

func (p *planner) injectKey(selections []*Selection, targetTypeName string, keyField string) ([]*Selection, bool) {
	injected := false

	isTargetContext := false
	for _, sel := range selections {
		if sel.ParentType == targetTypeName {
			isTargetContext = true
			break
		}
	}

	if isTargetContext {
		exists := false
		for _, sel := range selections {
			if sel.Field == keyField {
				exists = true
				break
			}
		}
		if !exists {
			selections = append(selections, &Selection{
				ParentType: targetTypeName,
				Field:      keyField,
			})
		}
		injected = true
	}

	for _, sel := range selections {
		fieldTypeName, err := p.getFieldTypeName(sel.ParentType, sel.Field)
		if err != nil {
			continue
		}

		if fieldTypeName == targetTypeName {
			exists := false
			for _, sub := range sel.SubSelections {
				if sub.Field == keyField {
					exists = true
					break
				}
			}

			if !exists {
				sel.SubSelections = append(sel.SubSelections, &Selection{
					ParentType: targetTypeName,
					Field:      keyField,
				})
			}
			injected = true
		}

		if len(sel.SubSelections) > 0 {
			updatedSubs, subInjected := p.injectKey(sel.SubSelections, targetTypeName, keyField)
			if subInjected {
				sel.SubSelections = updatedSubs
				injected = true
			}
		}
	}

	return selections, injected
}

func (p *planner) checkDAG(plan *Plan) error {
	visited := make(map[int]bool)
	var visit func(step *Step) error
	visit = func(step *Step) error {
		if visited[step.ID] {
			return fmt.Errorf("cycle detected at step %d", step.ID)
		}

		visited[step.ID] = true
		for _, depID := range step.DependsOn {
			depStep := plan.GetStepByID(depID)
			if depStep == nil {
				return fmt.Errorf("step %d depends on unknown step %d", step.ID, depID)
			}
			if err := visit(depStep); err != nil {
				return err
			}
		}
		visited[step.ID] = false
		return nil
	}

	for _, step := range plan.Steps {
		if err := visit(step); err != nil {
			return err
		}
	}

	return nil
}
