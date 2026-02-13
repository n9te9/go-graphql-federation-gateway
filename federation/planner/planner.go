package planner

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/n9te9/go-graphql-federation-gateway/federation/graph"
	"github.com/n9te9/graphql-parser/ast"
	"github.com/n9te9/graphql-parser/token"
)

type Planner interface {
	Plan(doc *ast.Document, variables map[string]any) (*Plan, error)
}

type planner struct {
	superGraph                 *graph.SuperGraph
	enableOpentelemetryTracing bool
}

type Step struct {
	ID       int
	SubGraph *graph.SubGraph

	RootFields    []*Selection
	Selections    []*Selection
	RootArguments map[string]map[string]any
	OperationType string
	DependsOn     []int
	Metadata      any
}

type planningContext struct {
	stepProvidedKeys map[int]map[string]struct{}
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

type PlannerOption struct {
	EnableOpentelemetryTracing bool
}

func NewPlanner(superGraph *graph.SuperGraph, setting PlannerOption) *planner {
	return &planner{
		superGraph:                 superGraph,
		enableOpentelemetryTracing: setting.EnableOpentelemetryTracing,
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
	OperationType  string
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

func (p *planner) Plan(doc *ast.Document, variables map[string]any) (*Plan, error) {
	// TODO: cache plans for repeated queries
	op := p.superGraph.GetOperation(doc)
	if op == nil {
		return nil, errors.New("no operation found")
	}
	if len(op.SelectionSet) == 0 {
		return nil, errors.New("empty selection")
	}

	schemaTypeDefinitions, queryFields, err := p.findOperationField(op)
	if err != nil {
		return nil, err
	}

	var rootTypeName string
	switch op.Operation {
	case ast.Query:
		rootTypeName = "Query"
	case ast.Mutation:
		rootTypeName = "Mutation"
	case ast.Subscription:
		rootTypeName = "Subscription"
	}

	// In graphql-parser, we need to find SchemaDefinition from Document
	for _, def := range p.superGraph.Schema.Definitions {
		if sd, ok := def.(*ast.SchemaDefinition); ok {
			for _, ot := range sd.OperationTypes {
				if (ot.Operation == token.QUERY && op.Operation == ast.Query) ||
					(ot.Operation == token.MUTATION && op.Operation == ast.Mutation) ||
					(ot.Operation == token.SUBSCRIPTION && op.Operation == ast.Subscription) {
					rootTypeName = ot.Type.Name.String()
				}
			}
		}
	}

	var rootSelections []*Selection
	for i, f := range queryFields {
		selections, err := p.extractSelections(f.SelectionSet, schemaTypeDefinitions[i].Name.String(), variables)
		if err != nil {
			return nil, err
		}

		rootArgs := make(map[string]any)
		for _, arg := range f.Arguments {
			rootArgs[arg.Name.String()] = p.resolveValue(arg.Value, variables)
		}

		var alias string
		if f.Alias != nil {
			alias = f.Alias.String()
		}

		rootSelections = append(rootSelections, &Selection{
			ParentType:    rootTypeName,
			Field:         f.Name.String(),
			Alias:         alias,
			Arguments:     rootArgs,
			SubSelections: selections,
		})
	}

	pctx := &planningContext{
		stepProvidedKeys: make(map[int]map[string]struct{}),
	}

	plan := p.plan(pctx, rootTypeName, rootSelections)
	if err := p.checkDAG(plan); err != nil {
		return nil, err
	}

	plan.RootSelections = rootSelections
	plan.OperationType = string(op.Operation)

	return plan, nil
}

func (p *planner) resolveValue(v ast.Value, variables map[string]any) any {
	switch val := v.(type) {
	case *ast.Variable:
		return variables[val.Name]
	case *ast.IntValue:
		return val.Value
	case *ast.FloatValue:
		return val.Value
	case *ast.StringValue:
		return val.Value
	case *ast.BooleanValue:
		return val.Value
	case *ast.EnumValue:
		return val.Value
	case *ast.ListValue:
		ret := make([]any, 0, len(val.Values))
		for _, item := range val.Values {
			ret = append(ret, p.resolveValue(item, variables))
		}
		return ret
	case *ast.ObjectValue:
		ret := make(map[string]any)
		for _, field := range val.Fields {
			ret[field.Name.String()] = p.resolveValue(field.Value, variables)
		}
		return ret
	default:
		return nil
	}
}

func (p *planner) extractSelections(selectionSet []ast.Selection, parentType string, variables map[string]any) ([]*Selection, error) {
	ret := make([]*Selection, 0)
	for _, sel := range selectionSet {
		switch f := sel.(type) {
		case *ast.Field:
			fieldTypeName, err := p.getFieldTypeName(parentType, f.Name.String())
			if err != nil {
				return nil, err
			}

			args := make(map[string]any)
			for _, arg := range f.Arguments {
				args[arg.Name.String()] = p.resolveValue(arg.Value, variables)
			}

			var alias string
			if f.Alias != nil {
				alias = f.Alias.String()
			}

			selection := &Selection{
				ParentType: parentType,
				Field:      f.Name.String(),
				Alias:      alias,
				Arguments:  args,
			}

			if len(f.SelectionSet) > 0 {
				subs, err := p.extractSelections(f.SelectionSet, fieldTypeName, variables)
				if err != nil {
					return nil, err
				}
				selection.SubSelections = subs
			}
			ret = append(ret, selection)
		case *ast.InlineFragment:
			typeCondition := f.TypeCondition.Name.String()
			subs, err := p.extractSelections(f.SelectionSet, typeCondition, variables)
			if err != nil {
				return nil, err
			}

			ret = append(ret, subs...)
		}
	}

	return ret, nil
}

func (p *planner) getFieldTypeName(parentTypename, fieldName string) (string, error) {
	if fieldName == "__typename" {
		return "String", nil
	}

	for _, def := range p.superGraph.Schema.Definitions {
		if td, ok := def.(*ast.ObjectTypeDefinition); ok {
			if td.Name.String() == parentTypename {
				for _, field := range td.Fields {
					if field.Name.String() == fieldName {
						return p.getNamedType(field.Type), nil
					}
				}
			}
		}
		if ext, ok := def.(*ast.ObjectTypeExtension); ok {
			if ext.Name.String() == parentTypename {
				for _, field := range ext.Fields {
					if field.Name.String() == fieldName {
						return p.getNamedType(field.Type), nil
					}
				}
			}
		}
	}

	return "", fmt.Errorf("field %s not found in type %s", fieldName, parentTypename)
}

func (p *planner) getNamedType(t ast.Type) string {
	switch typ := t.(type) {
	case *ast.NamedType:
		return typ.Name.String()
	case *ast.ListType:
		return p.getNamedType(typ.Type)
	case *ast.NonNullType:
		return p.getNamedType(typ.Type)
	default:
		return ""
	}
}

func (p *planner) findOperationField(op *ast.OperationDefinition) ([]*ast.ObjectTypeDefinition, []*ast.Field, error) {
	ret := make([]*ast.ObjectTypeDefinition, 0)
	fields := make([]*ast.Field, 0)

	var rootTypeName string
	switch op.Operation {
	case ast.Query:
		rootTypeName = "Query"
	case ast.Mutation:
		rootTypeName = "Mutation"
	case ast.Subscription:
		rootTypeName = "Subscription"
	}

	for _, def := range p.superGraph.Schema.Definitions {
		if sd, ok := def.(*ast.SchemaDefinition); ok {
			for _, ot := range sd.OperationTypes {
				if (ot.Operation == token.QUERY && op.Operation == ast.Query) ||
					(ot.Operation == token.MUTATION && op.Operation == ast.Mutation) ||
					(ot.Operation == token.SUBSCRIPTION && op.Operation == ast.Subscription) {
					rootTypeName = ot.Type.Name.String()
				}
			}
		}
	}

	var rootTD *ast.ObjectTypeDefinition
	for _, def := range p.superGraph.Schema.Definitions {
		if td, ok := def.(*ast.ObjectTypeDefinition); ok {
			if td.Name.String() == rootTypeName {
				rootTD = td
				break
			}
		}
	}

	if rootTD != nil {
		for _, sel := range op.SelectionSet {
			f, ok := sel.(*ast.Field)
			if !ok {
				continue
			}
			for _, fieldDef := range rootTD.Fields {
				if fieldDef.Name.String() == f.Name.String() {
					typeName := p.getNamedType(fieldDef.Type)
					for _, def := range p.superGraph.Schema.Definitions {
						if td, ok := def.(*ast.ObjectTypeDefinition); ok {
							if td.Name.String() == typeName {
								ret = append(ret, td)
								fields = append(fields, f)
								break
							}
						}
					}
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
	Alias      string

	SubSelections []*Selection
}

func (p *planner) plan(pctx *planningContext, rootTypeName string, rootSelections []*Selection) *Plan {
	plan := &Plan{
		Steps:          make([]*Step, 0),
		RootSelections: rootSelections,
	}

	for _, rootSel := range rootSelections {
		for _, subGraph := range p.superGraph.SubGraphs {
			if p.ownsRootField(subGraph, rootTypeName, rootSel.Field) {
				sels := p.walkRoot(rootSel.SubSelections, subGraph)

				plan.Steps = append(plan.Steps, &Step{
					SubGraph:   subGraph,
					Selections: sels,
					RootFields: []*Selection{rootSel},
					RootArguments: map[string]map[string]any{
						rootSel.Field: rootSel.Arguments,
					},
					OperationType: strings.ToLower(rootTypeName),
				})
			}
		}
	}

	for _, subGraph := range p.superGraph.SubGraphs {
		var resolverSels []*Selection
		for _, rootSel := range rootSelections {
			if p.ownsRootField(subGraph, rootTypeName, rootSel.Field) {
				var boundarySelections []*Selection
				for _, child := range rootSel.SubSelections {
					key := fmt.Sprintf("%s.%s", child.ParentType, child.Field)
					if _, ok := subGraph.OwnershipFieldMap()[key]; !ok {
						boundarySelections = append(boundarySelections, child)
					}
				}
				resolverSels = append(resolverSels, p.walkResolver(boundarySelections, subGraph)...)
			} else {
				resolverSels = append(resolverSels, p.walkResolver([]*Selection{rootSel}, subGraph)...)
			}
		}

		if len(resolverSels) > 0 {
			plan.Steps = append(plan.Steps, &Step{
				SubGraph:      subGraph,
				Selections:    resolverSels,
				RootFields:    nil,
				OperationType: rootTypeName,
			})
		}
	}

	for i, step := range plan.Steps {
		step.ID = i
		pctx.stepProvidedKeys[step.ID] = make(map[string]struct{})
	}

	p.solveRequiresField(plan)
	p.solveDependency(pctx, plan)

	return plan
}

func (p *planner) walkRoot(sels []*Selection, subGraph *graph.SubGraph) []*Selection {
	ret := make([]*Selection, 0)
	for _, sel := range sels {
		if sel.Field == "__typename" {
			ret = append(ret, &Selection{ParentType: sel.ParentType, Field: "__typename"})
			continue
		}

		key := fmt.Sprintf("%s.%s", sel.ParentType, sel.Field)
		if _, ok := subGraph.OwnershipFieldMap()[key]; ok {
			subSelections := p.walkRoot(sel.SubSelections, subGraph)
			if len(subSelections) == 0 && len(sel.SubSelections) > 0 {
				fieldTypeName, _ := p.getFieldTypeName(sel.ParentType, sel.Field)
				if fieldTypeName != "" {
					subSelections = append(subSelections, &Selection{
						ParentType: fieldTypeName,
						Field:      "__typename",
					})
				}
			}

			ret = append(ret, &Selection{
				ParentType:    sel.ParentType,
				Field:         sel.Field,
				Alias:         sel.Alias,
				Arguments:     sel.Arguments,
				SubSelections: subSelections,
			})
		}
	}
	return ret
}

func (p *planner) walkResolver(sels []*Selection, subGraph *graph.SubGraph) []*Selection {
	ret := make([]*Selection, 0)
	for _, sel := range sels {
		key := fmt.Sprintf("%s.%s", sel.ParentType, sel.Field)

		if _, ok := subGraph.OwnershipFieldMap()[key]; ok {
			subSelections := p.walkRoot(sel.SubSelections, subGraph)
			if len(subSelections) == 0 && len(sel.SubSelections) > 0 {
				fieldTypeName, _ := p.getFieldTypeName(sel.ParentType, sel.Field)
				if fieldTypeName != "" {
					subSelections = append(subSelections, &Selection{
						ParentType: fieldTypeName,
						Field:      "__typename",
					})
				}
			}

			ret = append(ret, &Selection{
				ParentType:    sel.ParentType,
				Field:         sel.Field,
				Alias:         sel.Alias,
				Arguments:     sel.Arguments,
				SubSelections: subSelections,
			})
		} else if len(sel.SubSelections) > 0 {
			ret = append(ret, p.walkResolver(sel.SubSelections, subGraph)...)
		}
	}
	return ret
}

func (p *planner) ownsRootField(subGraph *graph.SubGraph, rootTypeName string, fieldName string) bool {
	// Find rootTypeName definition in subGraph schema
	var rootTD *ast.ObjectTypeDefinition
	for _, def := range subGraph.Schema.Definitions {
		if td, ok := def.(*ast.ObjectTypeDefinition); ok {
			if td.Name.String() == rootTypeName {
				rootTD = td
				break
			}
		}
	}

	if rootTD != nil {
		for _, f := range rootTD.Fields {
			if f.Name.String() == fieldName {
				return true
			}
		}
	}
	return false
}

func (p *planner) solveDependency(pctx *planningContext, plan *Plan) {
	for _, step := range plan.Steps {
		if len(step.RootFields) > 0 {
			continue
		}

		p.solveOwnershipDependencies(pctx, plan.Steps, step)
		p.solveProvidingDependencies(pctx, plan.Steps, step)
	}

	p.enrichSelection(pctx, plan)
}

func (p *planner) solveOwnershipDependencies(pctx *planningContext, steps Steps, targetStep *Step) {
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

func (p *planner) solveProvidingDependencies(pctx *planningContext, steps Steps, targetStep *Step) {
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
				pctx.stepProvidedKeys[providerStep.ID][typeName+"."+key] = struct{}{}
			}
		}
	}
}

func (p *planner) enrichSelection(pctx *planningContext, plan *Plan) {
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
					if _, ok := pctx.stepProvidedKeys[providerStep.ID][fieldKey]; ok {
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
		}
	}
	traverse(step.Selections)

	return required
}

func (p *planner) getEntityKeys(subGraph *graph.SubGraph, typeName string) []string {
	extract := func(dirs []*ast.Directive) []string {
		for _, d := range dirs {
			if d.Name == "key" {
				return p.findKeyDirectiveFieldArguments(d.Arguments)[0]
			}
		}
		return nil
	}

	for _, def := range subGraph.Schema.Definitions {
		if t, ok := def.(*ast.ObjectTypeDefinition); ok {
			if t.Name.String() == typeName {
				if keys := extract(t.Directives); keys != nil {
					return keys
				}
			}
		}
		if ext, ok := def.(*ast.ObjectTypeExtension); ok {
			if ext.Name.String() == typeName {
				if keys := extract(ext.Directives); keys != nil {
					return keys
				}
			}
		}
	}

	return nil
}

func (p *planner) findKeyDirectiveFieldArguments(keyDirectiveArgs []*ast.Argument) [][]string {
	ret := make([][]string, 0)
	for _, arg := range keyDirectiveArgs {
		if arg.Name.String() == "fields" {
			v := strings.Trim(arg.Value.String(), `"`)
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
