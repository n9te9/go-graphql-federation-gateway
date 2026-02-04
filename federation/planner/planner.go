package planner

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/n9te9/go-graphql-federation-gateway/federation/graph"
	"github.com/n9te9/goliteql/query"
	"github.com/n9te9/goliteql/schema"
)

type Planner interface {
	Plan(doc *query.Document, variables map[string]any) (*Plan, error)
}

type planner struct {
	superGraph *graph.SuperGraph
}

type Step struct {
	ID       int
	SubGraph *graph.SubGraph

	RootFields    []string
	Selections    []*Selection
	RootArguments map[string]map[string]any
	OperationType string
	DependsOn     []int
	Done          chan struct{}

	ownershipMap map[string]struct{}

	Err error
}

func (s *Step) IsBase() bool {
	return len(s.RootFields) > 0
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

func (p *planner) Plan(doc *query.Document, variables map[string]any) (*Plan, error) {
	op := p.superGraph.GetOperation(doc)
	if len(op.Selections) == 0 {
		return nil, errors.New("empty selection")
	}

	schemaTypeDefinitions, queryFields, err := p.findOperationField(op)
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
		if op.OperationType == query.MutationOperation && len(p.superGraph.Schema.Definition.Mutation) > 0 {
			rootTypeName = string(p.superGraph.Schema.Definition.Mutation)
		}
	}

	var rootSelections []*Selection
	for i, f := range queryFields {
		selections, err := p.extractSelections(f.Selections, string(schemaTypeDefinitions[i].Name), variables)
		if err != nil {
			return nil, err
		}

		rootArgs := make(map[string]any)
		for _, arg := range f.Arguments {
			rootArgs[string(arg.Name)] = p.resolveValue(arg.Value, variables)
		}

		rootSelections = append(rootSelections, &Selection{
			ParentType:    rootTypeName,
			Field:         string(f.Name),
			Arguments:     rootArgs,
			SubSelections: selections,
		})
	}

	plan := p.plan(rootTypeName, rootSelections)
	if err := p.checkDAG(plan); err != nil {
		return nil, err
	}

	return plan, nil
}

func (p *planner) resolveValue(v any, variables map[string]any) any {
	s := fmt.Sprintf("%v", v)
	if strings.HasPrefix(s, "$") {
		return variables[strings.TrimPrefix(s, "$")]
	}
	return v
}

func (p *planner) extractSelections(selection []query.Selection, parentType string, variables map[string]any) ([]*Selection, error) {
	ret := make([]*Selection, 0)
	for _, sel := range selection {
		switch f := sel.(type) {
		case *query.Field:
			fieldTypeName, err := p.getFieldTypeName(parentType, string(f.Name))
			if err != nil {
				return nil, err
			}

			args := make(map[string]any)
			for _, arg := range f.Arguments {
				args[string(arg.Name)] = p.resolveValue(arg.Value, variables)
			}

			selection := &Selection{
				ParentType: parentType,
				Field:      string(f.Name),
				Arguments:  args,
			}

			if len(f.Selections) > 0 {
				subs, err := p.extractSelections(f.Selections, fieldTypeName, variables)
				if err != nil {
					return nil, err
				}
				selection.SubSelections = subs
			}
			ret = append(ret, selection)
		case *query.InlineFragment:
			typeCondition := string(f.TypeCondition)
			subs, err := p.extractSelections(f.Selections, typeCondition, variables)
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

func (p *planner) findOperationField(op *query.Operation) ([]*schema.TypeDefinition, []*query.Field, error) {
	ret := make([]*schema.TypeDefinition, 0)
	fields := make([]*query.Field, 0)
	for _, schemaOperation := range p.superGraph.Schema.Operations {
		for _, field := range schemaOperation.Fields {
			for _, sel := range op.Selections {
				f, ok := sel.(*query.Field)
				if !ok {
					continue
				}

				if string(field.Name) == string(f.Name) {
					ret = append(ret, p.superGraph.Schema.Indexes.TypeIndex[string(field.Type.GetRootType().Name)])
					fields = append(fields, f)
				}
			}
		}
	}
	return ret, fields, nil
}

type Selection struct {
	ParentType string
	Field      string
	Arguments  map[string]any

	SubSelections []*Selection
}

func (p *planner) plan(rootTypeName string, rootSelections []*Selection) *Plan {
	plan := &Plan{
		Steps:          make([]*Step, 0),
		RootSelections: rootSelections,
	}

	for _, rootSel := range rootSelections {
		for _, subGraph := range p.superGraph.SubGraphs {
			if p.ownsRootField(subGraph, rootTypeName, rootSel.Field) {
				sels := p.walk(rootSel.SubSelections, subGraph)

				plan.Steps = append(plan.Steps, &Step{
					SubGraph:      subGraph,
					Selections:    sels,
					RootFields:    []string{rootSel.Field},
					RootArguments: map[string]map[string]any{rootSel.Field: rootSel.Arguments},
					OperationType: strings.ToLower(rootTypeName),
					ownershipMap:  make(map[string]struct{}),
					Done:          make(chan struct{}),
				})
				break
			}
		}
	}

	for _, subGraph := range p.superGraph.SubGraphs {
		var resolverSels []*Selection
		for _, rootSel := range rootSelections {
			if !p.ownsRootField(subGraph, rootTypeName, rootSel.Field) {
				resolverSels = append(resolverSels, p.walk([]*Selection{rootSel}, subGraph)...)
			}
		}

		if len(resolverSels) > 0 {
			plan.Steps = append(plan.Steps, &Step{
				SubGraph:     subGraph,
				Selections:   resolverSels,
				RootFields:   nil,
				ownershipMap: make(map[string]struct{}),
				Done:         make(chan struct{}),
			})
		}
	}

	p.solveRequiresField(plan)
	p.solveDependency(plan)

	return plan
}

func (p *planner) walk(sels []*Selection, subGraph *graph.SubGraph) []*Selection {
	ret := make([]*Selection, 0)
	for _, sel := range sels {
		subSelections := p.walk(sel.SubSelections, subGraph)
		key := fmt.Sprintf("%s.%s", sel.ParentType, sel.Field)

		if _, ok := subGraph.OwnershipFieldMap()[key]; ok {
			ret = append(ret, &Selection{
				ParentType:    sel.ParentType,
				Field:         sel.Field,
				Arguments:     sel.Arguments,
				SubSelections: subSelections,
			})
		} else if len(subSelections) > 0 {
			ret = append(ret, subSelections...)
		}
	}
	return ret
}

func (p *planner) ownsRootField(subGraph *graph.SubGraph, rootTypeName string, fieldName string) bool {
	for _, op := range subGraph.Schema.Operations {
		if strings.EqualFold(string(op.OperationType), rootTypeName) {
			for _, f := range op.Fields {
				if string(f.Name) == fieldName {
					return true
				}
			}
		}
	}
	return false
}

func (p *planner) solveDependency(plan *Plan) {
	for i, step := range plan.Steps {
		step.ID = i
	}

	for _, step := range plan.Steps {
		if len(step.RootFields) > 0 {
			continue
		}

		p.solveOwnershipDependencies(plan.Steps, step)
		p.solveProvidingDependencies(plan.Steps, step)
	}

	p.enrichSelection(plan)
}

func (p *planner) solveOwnershipDependencies(steps Steps, targetStep *Step) {
	neededKeys := make(map[string]struct{})
	for typeName, requiredField := range p.findRequiredKeys(targetStep) {
		for _, key := range requiredField {
			neededKeys[typeName+"."+key] = struct{}{}
		}
	}

	for typeName, v := range targetStep.SubGraph.RequiredFields() {
		for fieldSet := range v {
			neededKeys[typeName+"."+fieldSet] = struct{}{}
		}
	}

	dependsOn := make([]int, 0, len(steps))
	for _, step := range steps {
		if step == targetStep || step.SubGraph == targetStep.SubGraph {
			continue
		}

		var hasDependency bool
		for key := range neededKeys {
			if _, ok := step.SubGraph.OwnershipFieldMap()[key]; ok {
				hasDependency = true
				break
			}
		}

		if !hasDependency {
			continue
		}

		if !slices.Contains(dependsOn, step.ID) && !slices.Contains(step.DependsOn, targetStep.ID) {
			dependsOn = append(dependsOn, step.ID)
		}
	}

	targetStep.DependsOn = dependsOn
}

func (p *planner) solveProvidingDependencies(steps Steps, targetStep *Step) {
	requiredKeysMap := p.findRequiredKeys(targetStep)
	for typeName := range requiredKeysMap {
		for _, providerStep := range steps {
			if targetStep == providerStep || targetStep.SubGraph == providerStep.SubGraph {
				continue
			}

			keys := p.getEntityKeys(providerStep.SubGraph, typeName)
			if len(keys) == 0 {
				continue
			}

			if !slices.Contains(targetStep.DependsOn, providerStep.ID) && !slices.Contains(providerStep.DependsOn, targetStep.ID) {
				targetStep.DependsOn = append(targetStep.DependsOn, providerStep.ID)
			}

			for _, key := range keys {
				providerStep.ownershipMap[typeName+"."+key] = struct{}{}
			}
		}
	}
}

func (p *planner) enrichSelection(plan *Plan) {
	for _, targetStep := range plan.Steps {
		if !targetStep.IsBase() {
			targetStep.RootFields = nil
		}

		requiredKeysMap := p.findRequiredKeys(targetStep)
		if len(requiredKeysMap) == 0 {
			continue
		}

		for typeName, keys := range requiredKeysMap {
			for _, key := range keys {
				for _, providerID := range targetStep.DependsOn {
					providerStep := plan.GetStepByID(providerID)

					fieldKey := typeName + "." + key
					if _, ok := providerStep.ownershipMap[fieldKey]; ok {
						updated, _ := p.injectKey(providerStep.Selections, typeName, key)
						providerStep.Selections = updated
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
					} else {
						ownerStep.Selections = append(ownerStep.Selections, &Selection{
							ParentType: parentType,
							Field:      reqField,
						})
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
