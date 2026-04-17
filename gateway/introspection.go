package gateway

import (
	"fmt"
	"sort"
	"strings"

	"github.com/n9te9/graphql-parser/ast"
	"github.com/n9te9/graphql-parser/token"
)

// ---------------------------------------------------------------------------
// GraphQL introspection implementation.
//
// This file resolves the standard `__schema` / `__type` introspection queries
// against the composed supergraph schema (SuperGraphV2.Schema).  It is used
// only when `enable_introspection: true` is set in gateway.yaml.
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Introspection data model (mirrors the standard __Schema meta types).
// ---------------------------------------------------------------------------

type iType struct {
	kind           string // SCALAR / OBJECT / INTERFACE / UNION / ENUM / INPUT_OBJECT / LIST / NON_NULL
	name           string // empty for LIST / NON_NULL wrappers
	description    string
	fields         []*iField
	interfaces     []*iType
	possibleTypes  []*iType
	enumValues     []*iEnumValue
	inputFields   []*iInputValue
	ofType         *iType
	specifiedByURL string
}

type iField struct {
	name              string
	description       string
	args              []*iInputValue
	ttype             *iType
	isDeprecated      bool
	deprecationReason string
}

type iInputValue struct {
	name              string
	description       string
	ttype             *iType
	defaultValue      string // canonical literal, "" if none
	isDeprecated      bool
	deprecationReason string
}

type iEnumValue struct {
	name              string
	description       string
	isDeprecated      bool
	deprecationReason string
}

type iDirective struct {
	name         string
	description  string
	locations    []string
	args         []*iInputValue
	isRepeatable bool
}

type iSchema struct {
	description      string
	queryType        *iType
	mutationType     *iType
	subscriptionType *iType
	types            []*iType
	directives       []*iDirective
}

// ---------------------------------------------------------------------------
// Detection helpers.
// ---------------------------------------------------------------------------

// isIntrospectionField reports whether the top-level field name is an
// introspection meta-field.
func isIntrospectionField(name string) bool {
	return name == "__schema" || name == "__type" || name == "__typename"
}

// operationIsPureIntrospection reports whether every top-level field of the
// given operation is an introspection meta-field.
// (If even one non-introspection field is present we fall back to the normal
// execution path.)
func operationIsPureIntrospection(op *ast.OperationDefinition) bool {
	if op == nil || len(op.SelectionSet) == 0 {
		return false
	}
	hasIntro := false
	for _, sel := range op.SelectionSet {
		f, ok := sel.(*ast.Field)
		if !ok {
			return false
		}
		if !isIntrospectionField(f.Name.String()) {
			return false
		}
		hasIntro = true
	}
	return hasIntro
}

// documentHasIntrospection scans every operation and reports whether any of
// them is a pure-introspection operation.
func documentHasIntrospection(doc *ast.Document) bool {
	if doc == nil {
		return false
	}
	for _, def := range doc.Definitions {
		op, ok := def.(*ast.OperationDefinition)
		if !ok {
			continue
		}
		if operationIsPureIntrospection(op) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Introspector: builds an iSchema and resolves selection sets against it.
// ---------------------------------------------------------------------------

type introspector struct {
	schema    *iSchema
	byName    map[string]*iType
	fragments map[string]*ast.FragmentDefinition
	variables map[string]any
}

func newIntrospector(engine *executionEngine) *introspector {
	b := &schemaBuilder{
		byName:    make(map[string]*iType),
		directive: make(map[string]*iDirective),
	}
	b.build(engine)
	return &introspector{
		schema: b.schema(),
		byName: b.byName,
	}
}

// resolveIntrospection executes every pure-introspection operation in doc
// against the composed schema and returns the "data" object.
func resolveIntrospection(doc *ast.Document, variables map[string]any, engine *executionEngine) map[string]any {
	in := newIntrospector(engine)
	in.variables = variables
	in.fragments = make(map[string]*ast.FragmentDefinition)
	for _, def := range doc.Definitions {
		if fd, ok := def.(*ast.FragmentDefinition); ok {
			in.fragments[fd.Name.String()] = fd
		}
	}

	// If there is a named operation and multiple operations exist, pick the
	// first pure-introspection operation (the spec requires clients to send a
	// single operation for introspection; we accept any that is pure).
	var op *ast.OperationDefinition
	for _, def := range doc.Definitions {
		if o, ok := def.(*ast.OperationDefinition); ok && operationIsPureIntrospection(o) {
			op = o
			break
		}
	}
	if op == nil {
		return map[string]any{}
	}

	return in.resolveRoot(op.SelectionSet)
}

func (in *introspector) resolveRoot(selections []ast.Selection) map[string]any {
	out := make(map[string]any, len(selections))
	for _, sel := range selections {
		f, ok := sel.(*ast.Field)
		if !ok {
			continue
		}
		key := f.Name.String()
		if f.Alias != nil {
			key = f.Alias.String()
		}
		switch f.Name.String() {
		case "__typename":
			out[key] = "Query"
		case "__schema":
			out[key] = in.resolveType("__Schema", in.schema, f.SelectionSet)
		case "__type":
			name := ""
			for _, a := range f.Arguments {
				if a.Name.String() == "name" {
					name = stringArgValue(a.Value, in.variables)
				}
			}
			t, ok := in.byName[name]
			if !ok {
				out[key] = nil
			} else {
				out[key] = in.resolveType("__Type", t, f.SelectionSet)
			}
		}
	}
	return out
}

// resolveType executes the selection set against source, which is a value
// whose introspection type is parentTypeName (one of __Schema / __Type /
// __Field / __InputValue / __EnumValue / __Directive).
func (in *introspector) resolveType(parentTypeName string, source any, selections []ast.Selection) any {
	if source == nil {
		return nil
	}
	out := make(map[string]any)
	in.collectSelections(parentTypeName, source, selections, out)
	return out
}

func (in *introspector) collectSelections(parentTypeName string, source any, selections []ast.Selection, out map[string]any) {
	for _, sel := range selections {
		switch s := sel.(type) {
		case *ast.Field:
			// Respect @skip/@include.
			if skipField(s.Directives, in.variables) {
				continue
			}
			key := s.Name.String()
			if s.Alias != nil {
				key = s.Alias.String()
			}
			if s.Name.String() == "__typename" {
				out[key] = parentTypeName
				continue
			}
			out[key] = in.resolveField(parentTypeName, source, s)
		case *ast.InlineFragment:
			tc := ""
			if s.TypeCondition != nil {
				tc = s.TypeCondition.String()
			}
			if tc == "" || tc == parentTypeName {
				in.collectSelections(parentTypeName, source, s.SelectionSet, out)
			}
		case *ast.FragmentSpread:
			if fd, ok := in.fragments[s.Name.String()]; ok {
				tc := ""
				if fd.TypeCondition != nil {
					tc = fd.TypeCondition.String()
				}
				if tc == "" || tc == parentTypeName {
					in.collectSelections(parentTypeName, source, fd.SelectionSet, out)
				}
			}
		}
	}
}

// resolveField dispatches field resolution per introspection type.
func (in *introspector) resolveField(parentTypeName string, source any, f *ast.Field) any {
	switch parentTypeName {
	case "__Schema":
		return in.resolveSchemaField(source.(*iSchema), f)
	case "__Type":
		return in.resolveTypeField(source.(*iType), f)
	case "__Field":
		return in.resolveFieldField(source.(*iField), f)
	case "__InputValue":
		return in.resolveInputValueField(source.(*iInputValue), f)
	case "__EnumValue":
		return in.resolveEnumValueField(source.(*iEnumValue), f)
	case "__Directive":
		return in.resolveDirectiveField(source.(*iDirective), f)
	}
	return nil
}

func (in *introspector) resolveSchemaField(s *iSchema, f *ast.Field) any {
	switch f.Name.String() {
	case "description":
		return stringOrNull(s.description)
	case "queryType":
		return in.resolveType("__Type", s.queryType, f.SelectionSet)
	case "mutationType":
		if s.mutationType == nil {
			return nil
		}
		return in.resolveType("__Type", s.mutationType, f.SelectionSet)
	case "subscriptionType":
		if s.subscriptionType == nil {
			return nil
		}
		return in.resolveType("__Type", s.subscriptionType, f.SelectionSet)
	case "types":
		return in.resolveTypeList(s.types, f.SelectionSet)
	case "directives":
		out := make([]any, 0, len(s.directives))
		for _, d := range s.directives {
			out = append(out, in.resolveType("__Directive", d, f.SelectionSet))
		}
		return out
	}
	return nil
}

func (in *introspector) resolveTypeField(t *iType, f *ast.Field) any {
	switch f.Name.String() {
	case "kind":
		return t.kind
	case "name":
		return stringOrNull(t.name)
	case "description":
		return stringOrNull(t.description)
	case "fields":
		if t.kind != "OBJECT" && t.kind != "INTERFACE" {
			return nil
		}
		includeDep := boolArg(f, "includeDeprecated", false, in.variables)
		out := make([]any, 0, len(t.fields))
		for _, fld := range t.fields {
			if !includeDep && fld.isDeprecated {
				continue
			}
			out = append(out, in.resolveType("__Field", fld, f.SelectionSet))
		}
		return out
	case "interfaces":
		if t.kind != "OBJECT" && t.kind != "INTERFACE" {
			return nil
		}
		return in.resolveTypeList(t.interfaces, f.SelectionSet)
	case "possibleTypes":
		if t.kind != "INTERFACE" && t.kind != "UNION" {
			return nil
		}
		return in.resolveTypeList(t.possibleTypes, f.SelectionSet)
	case "enumValues":
		if t.kind != "ENUM" {
			return nil
		}
		includeDep := boolArg(f, "includeDeprecated", false, in.variables)
		out := make([]any, 0, len(t.enumValues))
		for _, ev := range t.enumValues {
			if !includeDep && ev.isDeprecated {
				continue
			}
			out = append(out, in.resolveType("__EnumValue", ev, f.SelectionSet))
		}
		return out
	case "inputFields":
		if t.kind != "INPUT_OBJECT" {
			return nil
		}
		out := make([]any, 0, len(t.inputFields))
		for _, iv := range t.inputFields {
			out = append(out, in.resolveType("__InputValue", iv, f.SelectionSet))
		}
		return out
	case "ofType":
		if t.ofType == nil {
			return nil
		}
		return in.resolveType("__Type", t.ofType, f.SelectionSet)
	case "specifiedByURL":
		return stringOrNull(t.specifiedByURL)
	}
	return nil
}

func (in *introspector) resolveFieldField(fd *iField, f *ast.Field) any {
	switch f.Name.String() {
	case "name":
		return fd.name
	case "description":
		return stringOrNull(fd.description)
	case "args":
		out := make([]any, 0, len(fd.args))
		for _, a := range fd.args {
			out = append(out, in.resolveType("__InputValue", a, f.SelectionSet))
		}
		return out
	case "type":
		return in.resolveType("__Type", fd.ttype, f.SelectionSet)
	case "isDeprecated":
		return fd.isDeprecated
	case "deprecationReason":
		return stringOrNull(fd.deprecationReason)
	}
	return nil
}

func (in *introspector) resolveInputValueField(iv *iInputValue, f *ast.Field) any {
	switch f.Name.String() {
	case "name":
		return iv.name
	case "description":
		return stringOrNull(iv.description)
	case "type":
		return in.resolveType("__Type", iv.ttype, f.SelectionSet)
	case "defaultValue":
		return stringOrNull(iv.defaultValue)
	case "isDeprecated":
		return iv.isDeprecated
	case "deprecationReason":
		return stringOrNull(iv.deprecationReason)
	}
	return nil
}

func (in *introspector) resolveEnumValueField(ev *iEnumValue, f *ast.Field) any {
	switch f.Name.String() {
	case "name":
		return ev.name
	case "description":
		return stringOrNull(ev.description)
	case "isDeprecated":
		return ev.isDeprecated
	case "deprecationReason":
		return stringOrNull(ev.deprecationReason)
	}
	return nil
}

func (in *introspector) resolveDirectiveField(d *iDirective, f *ast.Field) any {
	switch f.Name.String() {
	case "name":
		return d.name
	case "description":
		return stringOrNull(d.description)
	case "locations":
		out := make([]any, 0, len(d.locations))
		for _, l := range d.locations {
			out = append(out, l)
		}
		return out
	case "args":
		out := make([]any, 0, len(d.args))
		for _, a := range d.args {
			out = append(out, in.resolveType("__InputValue", a, f.SelectionSet))
		}
		return out
	case "isRepeatable":
		return d.isRepeatable
	}
	return nil
}

func (in *introspector) resolveTypeList(types []*iType, selections []ast.Selection) []any {
	out := make([]any, 0, len(types))
	for _, t := range types {
		out = append(out, in.resolveType("__Type", t, selections))
	}
	return out
}

// ---------------------------------------------------------------------------
// Schema builder: walks the composed AST and produces an iSchema snapshot.
// ---------------------------------------------------------------------------

type schemaBuilder struct {
	byName    map[string]*iType
	directive map[string]*iDirective
	order     []string // preserves insertion order for stable "types" output
	queryName string
	mutName   string
	subName   string
	schemaDoc string // @schema description, if any
}

func (b *schemaBuilder) build(engine *executionEngine) {
	doc := engine.superGraph.Schema

	// Default operation root type names.
	b.queryName = "Query"
	b.mutName = "Mutation"
	b.subName = "Subscription"

	// Override from `schema { ... }` block if present.
	for _, def := range doc.Definitions {
		if sd, ok := def.(*ast.SchemaDefinition); ok {
			b.schemaDoc = sd.Description
			for _, ot := range sd.OperationTypes {
				if ot.Type == nil {
					continue
				}
				switch ot.Operation {
				case token.QUERY:
					b.queryName = ot.Type.Name.String()
				case token.MUTATION:
					b.mutName = ot.Type.Name.String()
				case token.SUBSCRIPTION:
					b.subName = ot.Type.Name.String()
				}
			}
		}
	}

	// Built-in scalars — registered up front so Type.ofType chains always resolve.
	for _, s := range []struct{ name, desc string }{
		{"Int", "The `Int` scalar type represents non-fractional signed whole numeric values."},
		{"Float", "The `Float` scalar type represents signed double-precision fractional values."},
		{"String", "The `String` scalar type represents textual data."},
		{"Boolean", "The `Boolean` scalar type represents `true` or `false`."},
		{"ID", "The `ID` scalar type represents a unique identifier."},
	} {
		b.registerType(&iType{kind: "SCALAR", name: s.name, description: s.desc})
	}

	// First pass: register every named type so wrappers/references always
	// resolve.  We'll fill fields / interfaces / possibleTypes in pass two.
	for _, def := range doc.Definitions {
		switch d := def.(type) {
		case *ast.ObjectTypeDefinition:
			b.registerType(&iType{kind: "OBJECT", name: d.Name.String(), description: d.Description})
		case *ast.InterfaceTypeDefinition:
			b.registerType(&iType{kind: "INTERFACE", name: d.Name.String(), description: d.Description})
		case *ast.UnionTypeDefinition:
			b.registerType(&iType{kind: "UNION", name: d.Name.String(), description: d.Description})
		case *ast.EnumTypeDefinition:
			b.registerType(&iType{kind: "ENUM", name: d.Name.String(), description: d.Description})
		case *ast.InputObjectTypeDefinition:
			b.registerType(&iType{kind: "INPUT_OBJECT", name: d.Name.String(), description: d.Description})
		case *ast.ScalarTypeDefinition:
			b.registerType(&iType{kind: "SCALAR", name: d.Name.String(), description: d.Description})
		}
	}

	// Second pass: populate structural details and handle ObjectTypeExtensions.
	for _, def := range doc.Definitions {
		switch d := def.(type) {
		case *ast.ObjectTypeDefinition:
			t := b.byName[d.Name.String()]
			t.fields = append(t.fields, b.buildFields(d.Fields)...)
			for _, iface := range d.Interfaces {
				if it, ok := b.byName[iface.Name.String()]; ok {
					t.interfaces = append(t.interfaces, it)
					// Register implementation on the interface side.
					it.possibleTypes = appendUnique(it.possibleTypes, t)
				}
			}
		case *ast.ObjectTypeExtension:
			t, ok := b.byName[d.Name.String()]
			if !ok {
				// Extension with no base type (should not happen after composition).
				t = &iType{kind: "OBJECT", name: d.Name.String()}
				b.registerType(t)
			}
			t.fields = append(t.fields, b.buildFields(d.Fields)...)
			for _, iface := range d.Interfaces {
				if it, ok := b.byName[iface.Name.String()]; ok {
					t.interfaces = appendUnique(t.interfaces, it)
					it.possibleTypes = appendUnique(it.possibleTypes, t)
				}
			}
		case *ast.InterfaceTypeDefinition:
			t := b.byName[d.Name.String()]
			t.fields = append(t.fields, b.buildFields(d.Fields)...)
		case *ast.UnionTypeDefinition:
			t := b.byName[d.Name.String()]
			for _, mt := range d.Types {
				if m, ok := b.byName[mt.Name.String()]; ok {
					t.possibleTypes = appendUnique(t.possibleTypes, m)
				}
			}
		case *ast.EnumTypeDefinition:
			t := b.byName[d.Name.String()]
			for _, v := range d.Values {
				dep, reason := deprecationInfo(v.Directives)
				t.enumValues = append(t.enumValues, &iEnumValue{
					name:              v.Name.String(),
					description:       v.Description,
					isDeprecated:      dep,
					deprecationReason: reason,
				})
			}
		case *ast.InputObjectTypeDefinition:
			t := b.byName[d.Name.String()]
			for _, iv := range d.Fields {
				t.inputFields = append(t.inputFields, b.buildInputValue(iv))
			}
		case *ast.ScalarTypeDefinition:
			t := b.byName[d.Name.String()]
			t.specifiedByURL = specifiedByURL(d.Directives)
		}
	}

	// Directive definitions: merge standard ones with composed user-defined.
	b.registerStandardDirectives()
	for _, def := range doc.Definitions {
		if dd, ok := def.(*ast.DirectiveDefinition); ok {
			b.registerDirective(dd)
		}
	}

	// Finally, register introspection meta-types so queries like
	// `__type(name: "__Type")` work.
	b.registerIntrospectionMetaTypes()
}

func (b *schemaBuilder) schema() *iSchema {
	// Deterministic type ordering: insertion order.
	types := make([]*iType, 0, len(b.order))
	for _, name := range b.order {
		types = append(types, b.byName[name])
	}

	s := &iSchema{
		description:      b.schemaDoc,
		queryType:        b.byName[b.queryName],
		mutationType:     b.byName[b.mutName],
		subscriptionType: b.byName[b.subName],
		types:            types,
	}

	// Sort directives by name for stable output.
	names := make([]string, 0, len(b.directive))
	for n := range b.directive {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		s.directives = append(s.directives, b.directive[n])
	}
	return s
}

func (b *schemaBuilder) registerType(t *iType) {
	if _, exists := b.byName[t.name]; exists {
		return
	}
	b.byName[t.name] = t
	b.order = append(b.order, t.name)
}

func (b *schemaBuilder) buildFields(defs []*ast.FieldDefinition) []*iField {
	out := make([]*iField, 0, len(defs))
	for _, fd := range defs {
		dep, reason := deprecationInfo(fd.Directives)
		args := make([]*iInputValue, 0, len(fd.Arguments))
		for _, a := range fd.Arguments {
			args = append(args, b.buildInputValue(a))
		}
		out = append(out, &iField{
			name:              fd.Name.String(),
			description:       fd.Description,
			args:              args,
			ttype:             b.buildTypeRef(fd.Type),
			isDeprecated:      dep,
			deprecationReason: reason,
		})
	}
	return out
}

func (b *schemaBuilder) buildInputValue(iv *ast.InputValueDefinition) *iInputValue {
	dep, reason := deprecationInfo(iv.Directives)
	def := ""
	if iv.DefaultValue != nil {
		def = iv.DefaultValue.String()
	}
	return &iInputValue{
		name:              iv.Name.String(),
		description:       iv.Description,
		ttype:             b.buildTypeRef(iv.Type),
		defaultValue:      def,
		isDeprecated:      dep,
		deprecationReason: reason,
	}
}

// buildTypeRef builds an iType wrapper chain (NON_NULL / LIST / named type).
// For an unknown named type a placeholder SCALAR is registered so the
// introspection result remains internally consistent.
func (b *schemaBuilder) buildTypeRef(t ast.Type) *iType {
	switch x := t.(type) {
	case *ast.NonNullType:
		return &iType{kind: "NON_NULL", ofType: b.buildTypeRef(x.Type)}
	case *ast.ListType:
		return &iType{kind: "LIST", ofType: b.buildTypeRef(x.Type)}
	case *ast.NamedType:
		name := x.Name.String()
		if ref, ok := b.byName[name]; ok {
			return ref
		}
		// Unknown — fall back to SCALAR so the chain still resolves.
		placeholder := &iType{kind: "SCALAR", name: name}
		b.registerType(placeholder)
		return placeholder
	}
	return &iType{kind: "SCALAR", name: "String"}
}

// ---------------------------------------------------------------------------
// Standard + user-defined directive registration.
// ---------------------------------------------------------------------------

func (b *schemaBuilder) registerStandardDirectives() {
	// @skip / @include
	skipInc := []*iInputValue{{
		name:  "if",
		ttype: nonNull(b.byName["Boolean"]),
	}}
	b.directive["skip"] = &iDirective{
		name:        "skip",
		description: "Directs the executor to skip this field or fragment when the `if` argument is true.",
		locations:   []string{"FIELD", "FRAGMENT_SPREAD", "INLINE_FRAGMENT"},
		args:        skipInc,
	}
	b.directive["include"] = &iDirective{
		name:        "include",
		description: "Directs the executor to include this field or fragment only when the `if` argument is true.",
		locations:   []string{"FIELD", "FRAGMENT_SPREAD", "INLINE_FRAGMENT"},
		args:        skipInc,
	}

	// @deprecated
	b.directive["deprecated"] = &iDirective{
		name:        "deprecated",
		description: "Marks an element of a GraphQL schema as no longer supported.",
		locations:   []string{"FIELD_DEFINITION", "ARGUMENT_DEFINITION", "INPUT_FIELD_DEFINITION", "ENUM_VALUE"},
		args: []*iInputValue{{
			name:         "reason",
			ttype:        b.byName["String"],
			defaultValue: `"No longer supported"`,
		}},
	}

	// @specifiedBy
	b.directive["specifiedBy"] = &iDirective{
		name:        "specifiedBy",
		description: "Exposes a URL that specifies the behaviour of this scalar.",
		locations:   []string{"SCALAR"},
		args: []*iInputValue{{
			name:  "url",
			ttype: nonNull(b.byName["String"]),
		}},
	}
}

func (b *schemaBuilder) registerDirective(dd *ast.DirectiveDefinition) {
	if _, exists := b.directive[dd.Name.String()]; exists {
		return
	}
	locations := make([]string, 0, len(dd.Locations))
	for _, l := range dd.Locations {
		locations = append(locations, l.String())
	}
	args := make([]*iInputValue, 0, len(dd.Arguments))
	for _, a := range dd.Arguments {
		args = append(args, b.buildInputValue(a))
	}
	b.directive[dd.Name.String()] = &iDirective{
		name:         dd.Name.String(),
		description:  dd.Description,
		locations:    locations,
		args:         args,
		isRepeatable: dd.Repeatable,
	}
}

// ---------------------------------------------------------------------------
// Introspection meta-type registration (so __type(name: "__Type") etc. works).
// ---------------------------------------------------------------------------

func (b *schemaBuilder) registerIntrospectionMetaTypes() {
	str := b.byName["String"]
	boolean := b.byName["Boolean"]

	// __TypeKind enum.
	typeKind := &iType{kind: "ENUM", name: "__TypeKind",
		description: "An enum describing what kind of type a given `__Type` is.",
		enumValues: []*iEnumValue{
			{name: "SCALAR"}, {name: "OBJECT"}, {name: "INTERFACE"}, {name: "UNION"},
			{name: "ENUM"}, {name: "INPUT_OBJECT"}, {name: "LIST"}, {name: "NON_NULL"},
		},
	}
	b.registerType(typeKind)

	// __DirectiveLocation enum.
	dirLoc := &iType{kind: "ENUM", name: "__DirectiveLocation",
		description: "A Directive can be adjacent to many parts of the GraphQL language.",
		enumValues: []*iEnumValue{
			{name: "QUERY"}, {name: "MUTATION"}, {name: "SUBSCRIPTION"},
			{name: "FIELD"}, {name: "FRAGMENT_DEFINITION"}, {name: "FRAGMENT_SPREAD"},
			{name: "INLINE_FRAGMENT"}, {name: "VARIABLE_DEFINITION"},
			{name: "SCHEMA"}, {name: "SCALAR"}, {name: "OBJECT"},
			{name: "FIELD_DEFINITION"}, {name: "ARGUMENT_DEFINITION"},
			{name: "INTERFACE"}, {name: "UNION"}, {name: "ENUM"}, {name: "ENUM_VALUE"},
			{name: "INPUT_OBJECT"}, {name: "INPUT_FIELD_DEFINITION"},
		},
	}
	b.registerType(dirLoc)

	// Declare meta-type objects up front so self-references work.
	metaSchema := &iType{kind: "OBJECT", name: "__Schema"}
	metaType := &iType{kind: "OBJECT", name: "__Type"}
	metaField := &iType{kind: "OBJECT", name: "__Field"}
	metaInput := &iType{kind: "OBJECT", name: "__InputValue"}
	metaEnum := &iType{kind: "OBJECT", name: "__EnumValue"}
	metaDirective := &iType{kind: "OBJECT", name: "__Directive"}
	b.registerType(metaSchema)
	b.registerType(metaType)
	b.registerType(metaField)
	b.registerType(metaInput)
	b.registerType(metaEnum)
	b.registerType(metaDirective)

	// __Schema fields.
	metaSchema.fields = []*iField{
		{name: "description", ttype: str},
		{name: "types", ttype: nonNull(list(nonNull(metaType)))},
		{name: "queryType", ttype: nonNull(metaType)},
		{name: "mutationType", ttype: metaType},
		{name: "subscriptionType", ttype: metaType},
		{name: "directives", ttype: nonNull(list(nonNull(metaDirective)))},
	}
	// __Type fields.
	metaType.fields = []*iField{
		{name: "kind", ttype: nonNull(typeKind)},
		{name: "name", ttype: str},
		{name: "description", ttype: str},
		{name: "fields", ttype: list(nonNull(metaField)), args: []*iInputValue{{name: "includeDeprecated", ttype: boolean, defaultValue: "false"}}},
		{name: "interfaces", ttype: list(nonNull(metaType))},
		{name: "possibleTypes", ttype: list(nonNull(metaType))},
		{name: "enumValues", ttype: list(nonNull(metaEnum)), args: []*iInputValue{{name: "includeDeprecated", ttype: boolean, defaultValue: "false"}}},
		{name: "inputFields", ttype: list(nonNull(metaInput))},
		{name: "ofType", ttype: metaType},
		{name: "specifiedByURL", ttype: str},
	}
	// __Field fields.
	metaField.fields = []*iField{
		{name: "name", ttype: nonNull(str)},
		{name: "description", ttype: str},
		{name: "args", ttype: nonNull(list(nonNull(metaInput)))},
		{name: "type", ttype: nonNull(metaType)},
		{name: "isDeprecated", ttype: nonNull(boolean)},
		{name: "deprecationReason", ttype: str},
	}
	// __InputValue fields.
	metaInput.fields = []*iField{
		{name: "name", ttype: nonNull(str)},
		{name: "description", ttype: str},
		{name: "type", ttype: nonNull(metaType)},
		{name: "defaultValue", ttype: str},
		{name: "isDeprecated", ttype: nonNull(boolean)},
		{name: "deprecationReason", ttype: str},
	}
	// __EnumValue fields.
	metaEnum.fields = []*iField{
		{name: "name", ttype: nonNull(str)},
		{name: "description", ttype: str},
		{name: "isDeprecated", ttype: nonNull(boolean)},
		{name: "deprecationReason", ttype: str},
	}
	// __Directive fields.
	metaDirective.fields = []*iField{
		{name: "name", ttype: nonNull(str)},
		{name: "description", ttype: str},
		{name: "locations", ttype: nonNull(list(nonNull(dirLoc)))},
		{name: "args", ttype: nonNull(list(nonNull(metaInput)))},
		{name: "isRepeatable", ttype: nonNull(boolean)},
	}
}

// ---------------------------------------------------------------------------
// Small helpers.
// ---------------------------------------------------------------------------

func nonNull(t *iType) *iType { return &iType{kind: "NON_NULL", ofType: t} }
func list(t *iType) *iType    { return &iType{kind: "LIST", ofType: t} }

func stringOrNull(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func appendUnique(list []*iType, t *iType) []*iType {
	for _, x := range list {
		if x == t {
			return list
		}
	}
	return append(list, t)
}

// deprecationInfo returns (true, reason) when a @deprecated directive is
// present and (false, "") otherwise. Reason defaults to "No longer supported".
func deprecationInfo(directives []*ast.Directive) (bool, string) {
	for _, d := range directives {
		if d.Name != "deprecated" {
			continue
		}
		reason := "No longer supported"
		for _, a := range d.Arguments {
			if a.Name.String() == "reason" {
				if sv, ok := a.Value.(*ast.StringValue); ok {
					reason = sv.Value
				}
			}
		}
		return true, reason
	}
	return false, ""
}

func specifiedByURL(directives []*ast.Directive) string {
	for _, d := range directives {
		if d.Name != "specifiedBy" {
			continue
		}
		for _, a := range d.Arguments {
			if a.Name.String() == "url" {
				if sv, ok := a.Value.(*ast.StringValue); ok {
					return sv.Value
				}
			}
		}
	}
	return ""
}

// skipField honours @skip(if: ...) and @include(if: ...).
func skipField(directives []*ast.Directive, vars map[string]any) bool {
	for _, d := range directives {
		switch d.Name {
		case "skip":
			if boolDirectiveArg(d, "if", false, vars) {
				return true
			}
		case "include":
			if !boolDirectiveArg(d, "if", true, vars) {
				return true
			}
		}
	}
	return false
}

func boolDirectiveArg(d *ast.Directive, name string, def bool, vars map[string]any) bool {
	for _, a := range d.Arguments {
		if a.Name.String() != name {
			continue
		}
		return evalBool(a.Value, vars, def)
	}
	return def
}

func boolArg(f *ast.Field, name string, def bool, vars map[string]any) bool {
	for _, a := range f.Arguments {
		if a.Name.String() != name {
			continue
		}
		return evalBool(a.Value, vars, def)
	}
	return def
}

func evalBool(v ast.Value, vars map[string]any, def bool) bool {
	switch x := v.(type) {
	case *ast.BooleanValue:
		return x.Value
	case *ast.Variable:
		if raw, ok := vars[x.Name]; ok {
			if bv, ok := raw.(bool); ok {
				return bv
			}
		}
	}
	return def
}

func stringArgValue(v ast.Value, vars map[string]any) string {
	switch x := v.(type) {
	case *ast.StringValue:
		return x.Value
	case *ast.Variable:
		if raw, ok := vars[x.Name]; ok {
			if sv, ok := raw.(string); ok {
				return sv
			}
			return fmt.Sprint(raw)
		}
	}
	return strings.Trim(v.String(), `"`)
}
