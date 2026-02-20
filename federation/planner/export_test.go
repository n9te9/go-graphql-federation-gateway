package planner

import (
	"github.com/n9te9/go-graphql-federation-gateway/federation/graph"
	"github.com/n9te9/graphql-parser/ast"
)

// InjectProvidedFieldsForTest exports injectProvidedFields for white-box testing.
func (p *PlannerV2) InjectProvidedFieldsForTest(
	selections []ast.Selection,
	parentType, fieldName string,
	childSelections []ast.Selection,
	sg *graph.SubGraphV2,
	fieldType string,
	fragmentDefs map[string]*ast.FragmentDefinition,
) []ast.Selection {
	return p.injectProvidedFields(selections, parentType, fieldName, childSelections, sg, fieldType, fragmentDefs)
}

// MergeSelectionsByNameForTest exports mergeSelectionsByName for white-box testing.
func (p *PlannerV2) MergeSelectionsByNameForTest(existing, additions []ast.Selection) []ast.Selection {
	return p.mergeSelectionsByName(existing, additions)
}

// CanResolveViaProvidesForTest exports canResolveViaProvides for white-box testing.
func (p *PlannerV2) CanResolveViaProvidesForTest(
	childSelections []ast.Selection,
	parentSG *graph.SubGraphV2,
	parentType, fieldName, fieldType string,
	dijkstraResult *graph.DijkstraResult,
) bool {
	return p.canResolveViaProvides(childSelections, parentSG, parentType, fieldName, fieldType, dijkstraResult)
}
