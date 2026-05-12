// Package introspection answers GraphQL introspection queries against a
// composed federation supergraph schema.
//
// The resolver walks an introspection-only operation against a *ast.Document
// (the composed supergraph) and produces a JSON-encodable response of the
// shape required by the GraphQL spec ([October 2021 §4.5]).
//
// Only introspection meta-fields are handled: __schema, __type and __typename.
// Mixed queries (introspection + data fields) must be detected by the caller
// and routed elsewhere; this package always assumes the operation is
// introspection-only.
//
// [October 2021 §4.5]: https://spec.graphql.org/October2021/#sec-Introspection
package introspection

import (
	"fmt"

	"github.com/n9te9/graphql-parser/ast"
)

// Resolver answers introspection queries from a composed supergraph schema.
//
// A Resolver is read-only after construction and safe for concurrent use.
type Resolver struct {
	schema *ast.Document

	// Indexes built once at construction time.
	objects    map[string]*ast.ObjectTypeDefinition
	interfaces map[string]*ast.InterfaceTypeDefinition
	unions     map[string]*ast.UnionTypeDefinition
	enums      map[string]*ast.EnumTypeDefinition
	inputs     map[string]*ast.InputObjectTypeDefinition
	scalars    map[string]*ast.ScalarTypeDefinition
	directives map[string]*ast.DirectiveDefinition
}

// NewResolver indexes the schema and returns a ready-to-use Resolver.
func NewResolver(schema *ast.Document) *Resolver {
	r := &Resolver{
		schema:     schema,
		objects:    make(map[string]*ast.ObjectTypeDefinition),
		interfaces: make(map[string]*ast.InterfaceTypeDefinition),
		unions:     make(map[string]*ast.UnionTypeDefinition),
		enums:      make(map[string]*ast.EnumTypeDefinition),
		inputs:     make(map[string]*ast.InputObjectTypeDefinition),
		scalars:    make(map[string]*ast.ScalarTypeDefinition),
		directives: make(map[string]*ast.DirectiveDefinition),
	}
	if schema == nil {
		return r
	}
	for _, def := range schema.Definitions {
		switch d := def.(type) {
		case *ast.ObjectTypeDefinition:
			r.objects[d.Name.String()] = d
		case *ast.InterfaceTypeDefinition:
			r.interfaces[d.Name.String()] = d
		case *ast.UnionTypeDefinition:
			r.unions[d.Name.String()] = d
		case *ast.EnumTypeDefinition:
			r.enums[d.Name.String()] = d
		case *ast.InputObjectTypeDefinition:
			r.inputs[d.Name.String()] = d
		case *ast.ScalarTypeDefinition:
			r.scalars[d.Name.String()] = d
		case *ast.DirectiveDefinition:
			r.directives[d.Name.String()] = d
		}
	}
	// Built-in scalars are always available, even when not declared in SDL.
	for _, name := range []string{"String", "Int", "Float", "Boolean", "ID"} {
		if _, ok := r.scalars[name]; !ok {
			r.scalars[name] = &ast.ScalarTypeDefinition{Name: &ast.Name{Value: name}}
		}
	}
	return r
}

// Resolve produces the data payload for op (assumed introspection-only).
// The returned data map is JSON-encodable. Any errors encountered are returned
// as a slice; data may be partial (non-nil) even when errs is non-empty.
func (r *Resolver) Resolve(
	doc *ast.Document,
	op *ast.OperationDefinition,
	vars map[string]any,
) (map[string]any, []error) {
	if r.schema == nil || op == nil {
		return nil, []error{fmt.Errorf("introspection: missing schema or operation")}
	}
	frags := collectFragments(doc)
	out := map[string]any{}
	var errs []error
	for _, sel := range op.SelectionSet {
		f, ok := sel.(*ast.Field)
		if !ok {
			continue
		}
		key := fieldResponseKey(f)
		switch f.Name.String() {
		case "__schema":
			out[key] = r.resolveSchema(f, frags, vars)
		case "__type":
			name := stringArg(f.Arguments, "name", vars)
			if name == "" {
				errs = append(errs, fmt.Errorf("__type requires a non-empty 'name' argument"))
				out[key] = nil
				continue
			}
			if t := r.resolveType(name, f, frags, vars); t == nil {
				out[key] = nil
			} else {
				out[key] = t
			}
		case "__typename":
			// Top-level __typename returns the operation root type.
			out[key] = r.rootTypeName(op.Operation)
		}
	}
	return out, errs
}

// rootTypeName returns the root type name for the operation.
func (r *Resolver) rootTypeName(operation ast.OperationType) string {
	switch operation {
	case ast.Mutation:
		return "Mutation"
	case ast.Subscription:
		return "Subscription"
	default:
		return "Query"
	}
}

// ---------------------------------------------------------------------------
// __Schema resolver
// ---------------------------------------------------------------------------

func (r *Resolver) resolveSchema(
	field *ast.Field,
	frags map[string]*ast.FragmentDefinition,
	vars map[string]any,
) map[string]any {
	out := map[string]any{}
	for _, sel := range expandSelections(field.SelectionSet, frags, "__Schema") {
		f, ok := sel.(*ast.Field)
		if !ok {
			continue
		}
		key := fieldResponseKey(f)
		switch f.Name.String() {
		case "__typename":
			out[key] = "__Schema"
		case "description":
			out[key] = nil
		case "types":
			types := make([]any, 0)
			for _, def := range r.schema.Definitions {
				if name := definitionTypeName(def); name != "" {
					types = append(types, r.resolveTypeDef(def, f, frags, vars))
				}
			}
			// Built-in scalars not present in schema are still introspectable.
			for _, name := range []string{"String", "Int", "Float", "Boolean", "ID"} {
				if !r.hasTypeDef(name) {
					types = append(types, r.resolveTypeDef(r.scalars[name], f, frags, vars))
				}
			}
			out[key] = types
		case "queryType":
			out[key] = r.resolveTypeRefByName("Query", f, frags, vars)
		case "mutationType":
			if _, ok := r.objects["Mutation"]; ok {
				out[key] = r.resolveTypeRefByName("Mutation", f, frags, vars)
			} else {
				out[key] = nil
			}
		case "subscriptionType":
			if _, ok := r.objects["Subscription"]; ok {
				out[key] = r.resolveTypeRefByName("Subscription", f, frags, vars)
			} else {
				out[key] = nil
			}
		case "directives":
			dirs := make([]any, 0, len(r.directives))
			for _, d := range r.directives {
				dirs = append(dirs, r.resolveDirective(d, f, frags, vars))
			}
			out[key] = dirs
		}
	}
	return out
}

// hasTypeDef reports whether the schema declares a named type with this name.
func (r *Resolver) hasTypeDef(name string) bool {
	if _, ok := r.objects[name]; ok {
		return true
	}
	if _, ok := r.interfaces[name]; ok {
		return true
	}
	if _, ok := r.unions[name]; ok {
		return true
	}
	if _, ok := r.enums[name]; ok {
		return true
	}
	if _, ok := r.inputs[name]; ok {
		return true
	}
	if _, ok := r.scalars[name]; ok {
		return true
	}
	return false
}

// definitionTypeName returns the name for a type definition, or "" if def is
// not a named type definition.
func definitionTypeName(def ast.Definition) string {
	switch d := def.(type) {
	case *ast.ObjectTypeDefinition:
		return d.Name.String()
	case *ast.InterfaceTypeDefinition:
		return d.Name.String()
	case *ast.UnionTypeDefinition:
		return d.Name.String()
	case *ast.EnumTypeDefinition:
		return d.Name.String()
	case *ast.InputObjectTypeDefinition:
		return d.Name.String()
	case *ast.ScalarTypeDefinition:
		return d.Name.String()
	}
	return ""
}

// ---------------------------------------------------------------------------
// __Type resolver
// ---------------------------------------------------------------------------

func (r *Resolver) resolveType(
	name string,
	field *ast.Field,
	frags map[string]*ast.FragmentDefinition,
	vars map[string]any,
) map[string]any {
	def := r.lookupType(name)
	if def == nil {
		return nil
	}
	return r.resolveTypeDef(def, field, frags, vars)
}

func (r *Resolver) lookupType(name string) ast.Definition {
	if d, ok := r.objects[name]; ok {
		return d
	}
	if d, ok := r.interfaces[name]; ok {
		return d
	}
	if d, ok := r.unions[name]; ok {
		return d
	}
	if d, ok := r.enums[name]; ok {
		return d
	}
	if d, ok := r.inputs[name]; ok {
		return d
	}
	if d, ok := r.scalars[name]; ok {
		return d
	}
	return nil
}

// resolveTypeDef builds a `__Type` value for a named type definition.
func (r *Resolver) resolveTypeDef(
	def ast.Definition,
	field *ast.Field,
	frags map[string]*ast.FragmentDefinition,
	vars map[string]any,
) map[string]any {
	out := map[string]any{}
	for _, sel := range expandSelections(field.SelectionSet, frags, "__Type") {
		f, ok := sel.(*ast.Field)
		if !ok {
			continue
		}
		key := fieldResponseKey(f)
		switch f.Name.String() {
		case "__typename":
			out[key] = "__Type"
		case "kind":
			out[key] = typeKind(def)
		case "name":
			out[key] = definitionTypeName(def)
		case "description":
			out[key] = nil
		case "fields":
			out[key] = r.fieldsOf(def, f, frags, vars)
		case "interfaces":
			out[key] = r.interfacesOf(def, f, frags, vars)
		case "possibleTypes":
			out[key] = r.possibleTypesOf(def, f, frags, vars)
		case "enumValues":
			out[key] = r.enumValuesOf(def, f, frags, vars)
		case "inputFields":
			out[key] = r.inputFieldsOf(def, f, frags, vars)
		case "ofType":
			out[key] = nil // Named types have no ofType; only LIST/NON_NULL wrappers do.
		case "specifiedByURL":
			out[key] = nil
		}
	}
	return out
}

// resolveTypeRef resolves a __Type for an ast.Type (which may be List or NonNull).
func (r *Resolver) resolveTypeRef(
	t ast.Type,
	field *ast.Field,
	frags map[string]*ast.FragmentDefinition,
	vars map[string]any,
) map[string]any {
	if t == nil {
		return nil
	}
	out := map[string]any{}
	for _, sel := range expandSelections(field.SelectionSet, frags, "__Type") {
		f, ok := sel.(*ast.Field)
		if !ok {
			continue
		}
		key := fieldResponseKey(f)
		switch f.Name.String() {
		case "__typename":
			out[key] = "__Type"
		case "kind":
			out[key] = typeRefKind(t)
		case "name":
			if named, ok := t.(*ast.NamedType); ok {
				out[key] = named.Name.String()
			} else {
				out[key] = nil
			}
		case "description":
			out[key] = nil
		case "ofType":
			switch wrapped := t.(type) {
			case *ast.NonNullType:
				out[key] = r.resolveTypeRef(wrapped.Type, f, frags, vars)
			case *ast.ListType:
				out[key] = r.resolveTypeRef(wrapped.Type, f, frags, vars)
			default:
				out[key] = nil
			}
		case "fields", "interfaces", "possibleTypes", "enumValues", "inputFields":
			// For NonNull / List wrappers these are always null. For Named
			// types we need to delegate to resolveTypeDef-style behaviour.
			if named, ok := t.(*ast.NamedType); ok {
				if def := r.lookupType(named.Name.String()); def != nil {
					switch f.Name.String() {
					case "fields":
						out[key] = r.fieldsOf(def, f, frags, vars)
					case "interfaces":
						out[key] = r.interfacesOf(def, f, frags, vars)
					case "possibleTypes":
						out[key] = r.possibleTypesOf(def, f, frags, vars)
					case "enumValues":
						out[key] = r.enumValuesOf(def, f, frags, vars)
					case "inputFields":
						out[key] = r.inputFieldsOf(def, f, frags, vars)
					}
					continue
				}
			}
			out[key] = nil
		case "specifiedByURL":
			out[key] = nil
		}
	}
	return out
}

// resolveTypeRefByName returns a __Type reference for a named type.
func (r *Resolver) resolveTypeRefByName(
	name string,
	field *ast.Field,
	frags map[string]*ast.FragmentDefinition,
	vars map[string]any,
) map[string]any {
	return r.resolveTypeRef(&ast.NamedType{Name: &ast.Name{Value: name}}, field, frags, vars)
}

// ---------------------------------------------------------------------------
// __Type sub-resolvers
// ---------------------------------------------------------------------------

func (r *Resolver) fieldsOf(
	def ast.Definition,
	field *ast.Field,
	frags map[string]*ast.FragmentDefinition,
	vars map[string]any,
) any {
	var fieldDefs []*ast.FieldDefinition
	switch d := def.(type) {
	case *ast.ObjectTypeDefinition:
		fieldDefs = d.Fields
	case *ast.InterfaceTypeDefinition:
		fieldDefs = d.Fields
	default:
		return nil
	}
	out := make([]any, 0, len(fieldDefs))
	for _, fd := range fieldDefs {
		// Skip @inaccessible and gateway-internal meta fields.
		if hasDirective(fd.Directives, "inaccessible") {
			continue
		}
		name := fd.Name.String()
		if name == "_service" || name == "_entities" {
			continue
		}
		out = append(out, r.resolveField(fd, field, frags, vars))
	}
	return out
}

func (r *Resolver) interfacesOf(
	def ast.Definition,
	field *ast.Field,
	frags map[string]*ast.FragmentDefinition,
	vars map[string]any,
) any {
	obj, ok := def.(*ast.ObjectTypeDefinition)
	if !ok {
		return nil
	}
	out := make([]any, 0, len(obj.Interfaces))
	for _, iface := range obj.Interfaces {
		out = append(out, r.resolveTypeRef(iface, field, frags, vars))
	}
	return out
}

func (r *Resolver) possibleTypesOf(
	def ast.Definition,
	field *ast.Field,
	frags map[string]*ast.FragmentDefinition,
	vars map[string]any,
) any {
	switch d := def.(type) {
	case *ast.UnionTypeDefinition:
		out := make([]any, 0, len(d.Types))
		for _, t := range d.Types {
			out = append(out, r.resolveTypeRef(t, field, frags, vars))
		}
		return out
	case *ast.InterfaceTypeDefinition:
		// Find every object type that implements this interface.
		ifaceName := d.Name.String()
		out := make([]any, 0)
		for _, obj := range r.objects {
			for _, iface := range obj.Interfaces {
				if iface != nil && iface.Name.String() == ifaceName {
					out = append(out, r.resolveTypeRefByName(obj.Name.String(), field, frags, vars))
					break
				}
			}
		}
		return out
	}
	return nil
}

func (r *Resolver) enumValuesOf(
	def ast.Definition,
	field *ast.Field,
	frags map[string]*ast.FragmentDefinition,
	vars map[string]any,
) any {
	enum, ok := def.(*ast.EnumTypeDefinition)
	if !ok {
		return nil
	}
	out := make([]any, 0, len(enum.Values))
	for _, v := range enum.Values {
		if hasDirective(v.Directives, "inaccessible") {
			continue
		}
		out = append(out, r.resolveEnumValue(v, field, frags))
	}
	return out
}

func (r *Resolver) inputFieldsOf(
	def ast.Definition,
	field *ast.Field,
	frags map[string]*ast.FragmentDefinition,
	vars map[string]any,
) any {
	in, ok := def.(*ast.InputObjectTypeDefinition)
	if !ok {
		return nil
	}
	out := make([]any, 0, len(in.Fields))
	for _, f := range in.Fields {
		if hasDirective(f.Directives, "inaccessible") {
			continue
		}
		out = append(out, r.resolveInputValue(f, field, frags, vars))
	}
	return out
}

// ---------------------------------------------------------------------------
// __Field / __InputValue / __EnumValue / __Directive
// ---------------------------------------------------------------------------

func (r *Resolver) resolveField(
	fd *ast.FieldDefinition,
	parent *ast.Field,
	frags map[string]*ast.FragmentDefinition,
	vars map[string]any,
) map[string]any {
	out := map[string]any{}
	for _, sel := range expandSelections(parent.SelectionSet, frags, "__Field") {
		f, ok := sel.(*ast.Field)
		if !ok {
			continue
		}
		key := fieldResponseKey(f)
		switch f.Name.String() {
		case "__typename":
			out[key] = "__Field"
		case "name":
			out[key] = fd.Name.String()
		case "description":
			out[key] = nil
		case "args":
			args := make([]any, 0, len(fd.Arguments))
			for _, a := range fd.Arguments {
				args = append(args, r.resolveInputValue(a, f, frags, vars))
			}
			out[key] = args
		case "type":
			out[key] = r.resolveTypeRef(fd.Type, f, frags, vars)
		case "isDeprecated":
			out[key] = hasDirective(fd.Directives, "deprecated")
		case "deprecationReason":
			out[key] = deprecationReason(fd.Directives)
		}
	}
	return out
}

func (r *Resolver) resolveInputValue(
	in *ast.InputValueDefinition,
	parent *ast.Field,
	frags map[string]*ast.FragmentDefinition,
	vars map[string]any,
) map[string]any {
	out := map[string]any{}
	for _, sel := range expandSelections(parent.SelectionSet, frags, "__InputValue") {
		f, ok := sel.(*ast.Field)
		if !ok {
			continue
		}
		key := fieldResponseKey(f)
		switch f.Name.String() {
		case "__typename":
			out[key] = "__InputValue"
		case "name":
			out[key] = in.Name.String()
		case "description":
			out[key] = nil
		case "type":
			out[key] = r.resolveTypeRef(in.Type, f, frags, vars)
		case "defaultValue":
			if in.DefaultValue != nil {
				s := in.DefaultValue.String()
				out[key] = s
			} else {
				out[key] = nil
			}
		case "isDeprecated":
			out[key] = hasDirective(in.Directives, "deprecated")
		case "deprecationReason":
			out[key] = deprecationReason(in.Directives)
		}
	}
	return out
}

func (r *Resolver) resolveEnumValue(
	v *ast.EnumValueDefinition,
	parent *ast.Field,
	frags map[string]*ast.FragmentDefinition,
) map[string]any {
	out := map[string]any{}
	for _, sel := range expandSelections(parent.SelectionSet, frags, "__EnumValue") {
		f, ok := sel.(*ast.Field)
		if !ok {
			continue
		}
		key := fieldResponseKey(f)
		switch f.Name.String() {
		case "__typename":
			out[key] = "__EnumValue"
		case "name":
			out[key] = v.Name.String()
		case "description":
			out[key] = nil
		case "isDeprecated":
			out[key] = hasDirective(v.Directives, "deprecated")
		case "deprecationReason":
			out[key] = deprecationReason(v.Directives)
		}
	}
	return out
}

func (r *Resolver) resolveDirective(
	d *ast.DirectiveDefinition,
	parent *ast.Field,
	frags map[string]*ast.FragmentDefinition,
	vars map[string]any,
) map[string]any {
	out := map[string]any{}
	for _, sel := range expandSelections(parent.SelectionSet, frags, "__Directive") {
		f, ok := sel.(*ast.Field)
		if !ok {
			continue
		}
		key := fieldResponseKey(f)
		switch f.Name.String() {
		case "__typename":
			out[key] = "__Directive"
		case "name":
			out[key] = d.Name.String()
		case "description":
			out[key] = nil
		case "locations":
			locs := make([]string, 0, len(d.Locations))
			for _, l := range d.Locations {
				locs = append(locs, l.String())
			}
			out[key] = locs
		case "args":
			args := make([]any, 0, len(d.Arguments))
			for _, a := range d.Arguments {
				args = append(args, r.resolveInputValue(a, f, frags, vars))
			}
			out[key] = args
		case "isRepeatable":
			out[key] = false
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// typeKind returns the introspection __TypeKind value for a named type def.
func typeKind(def ast.Definition) string {
	switch def.(type) {
	case *ast.ObjectTypeDefinition:
		return "OBJECT"
	case *ast.InterfaceTypeDefinition:
		return "INTERFACE"
	case *ast.UnionTypeDefinition:
		return "UNION"
	case *ast.EnumTypeDefinition:
		return "ENUM"
	case *ast.InputObjectTypeDefinition:
		return "INPUT_OBJECT"
	case *ast.ScalarTypeDefinition:
		return "SCALAR"
	}
	return "SCALAR"
}

// typeRefKind returns the kind of an ast.Type reference, including LIST and
// NON_NULL wrappers.
func typeRefKind(t ast.Type) string {
	switch v := t.(type) {
	case *ast.NonNullType:
		return "NON_NULL"
	case *ast.ListType:
		return "LIST"
	case *ast.NamedType:
		// The kind of a NamedType depends on what the schema declares it as.
		// Without access to the schema here we fall back to SCALAR; callers
		// that need the precise kind use Resolver.resolveTypeDef instead.
		_ = v
		return "SCALAR"
	}
	return "SCALAR"
}

// hasDirective reports whether the directive set contains a directive with
// the given name.
func hasDirective(dirs []*ast.Directive, name string) bool {
	for _, d := range dirs {
		if d.Name == name {
			return true
		}
	}
	return false
}

// deprecationReason extracts the `reason` argument from a @deprecated directive,
// returning nil when no @deprecated directive is present.
func deprecationReason(dirs []*ast.Directive) any {
	for _, d := range dirs {
		if d.Name != "deprecated" {
			continue
		}
		for _, a := range d.Arguments {
			if a.Name != nil && a.Name.Value == "reason" {
				if sv, ok := a.Value.(*ast.StringValue); ok {
					return sv.Value
				}
				return a.Value.String()
			}
		}
		return "No longer supported"
	}
	return nil
}

// fieldResponseKey returns the response key (alias or field name) for f.
func fieldResponseKey(f *ast.Field) string {
	if f.Alias != nil && f.Alias.String() != "" {
		return f.Alias.String()
	}
	return f.Name.String()
}

// stringArg extracts a StringValue argument, resolving variable references when
// present in vars. Returns "" if the argument is missing or not a string.
func stringArg(args []*ast.Argument, name string, vars map[string]any) string {
	for _, a := range args {
		if a.Name == nil || a.Name.Value != name {
			continue
		}
		switch v := a.Value.(type) {
		case *ast.StringValue:
			return v.Value
		case *ast.Variable:
			if vars == nil {
				return ""
			}
			if raw, ok := vars[v.Name]; ok {
				if s, ok := raw.(string); ok {
					return s
				}
			}
		}
	}
	return ""
}

// collectFragments extracts all named fragment definitions from doc.
func collectFragments(doc *ast.Document) map[string]*ast.FragmentDefinition {
	out := map[string]*ast.FragmentDefinition{}
	if doc == nil {
		return out
	}
	for _, def := range doc.Definitions {
		if frag, ok := def.(*ast.FragmentDefinition); ok {
			out[frag.Name.String()] = frag
		}
	}
	return out
}

// expandSelections inlines fragment spreads and inline fragments into a flat
// selection list, scoped to introspection meta-types so we don't need
// schema-wide type compatibility checks.
//
// parentTypeName is the introspection type we are resolving (e.g. "__Schema",
// "__Type"). Inline fragments and named fragments are inlined when their
// type condition matches the parent or is empty.
func expandSelections(
	sels []ast.Selection,
	frags map[string]*ast.FragmentDefinition,
	parentTypeName string,
) []ast.Selection {
	out := make([]ast.Selection, 0, len(sels))
	for _, sel := range sels {
		switch s := sel.(type) {
		case *ast.Field:
			out = append(out, s)
		case *ast.InlineFragment:
			cond := ""
			if s.TypeCondition != nil {
				cond = s.TypeCondition.String()
			}
			if cond == "" || cond == parentTypeName {
				out = append(out, expandSelections(s.SelectionSet, frags, parentTypeName)...)
			}
		case *ast.FragmentSpread:
			frag, ok := frags[s.Name.String()]
			if !ok {
				continue
			}
			cond := ""
			if frag.TypeCondition != nil {
				cond = frag.TypeCondition.String()
			}
			if cond == "" || cond == parentTypeName {
				out = append(out, expandSelections(frag.SelectionSet, frags, parentTypeName)...)
			}
		}
	}
	return out
}
