package graph_test

import (
	"testing"

	"github.com/n9te9/go-graphql-federation-gateway/federation/graph"
)

func TestNewSubGraphV2(t *testing.T) {
	schema := `
		type Product @key(fields: "id") {
			id: ID!
			name: String!
			price: Float!
		}

		type Query {
			product(id: ID!): Product
		}
	`

	sg, err := graph.NewSubGraphV2("product", []byte(schema), "http://product.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed: %v", err)
	}

	if sg.Name != "product" {
		t.Errorf("expected name 'product', got '%s'", sg.Name)
	}

	if sg.Host != "http://product.example.com" {
		t.Errorf("expected host 'http://product.example.com', got '%s'", sg.Host)
	}

	entities := sg.GetEntities()
	if len(entities) != 1 {
		t.Fatalf("expected 1 entity, got %d", len(entities))
	}

	productEntity, ok := entities["Product"]
	if !ok {
		t.Fatal("Product entity not found")
	}

	if len(productEntity.Keys) != 1 {
		t.Errorf("expected 1 key, got %d", len(productEntity.Keys))
	}

	if productEntity.Keys[0].FieldSet != "id" {
		t.Errorf("expected key field 'id', got '%s'", productEntity.Keys[0].FieldSet)
	}

	if !productEntity.Keys[0].Resolvable {
		t.Error("expected key to be resolvable")
	}

	if productEntity.IsExtension() {
		t.Error("expected Product entity to not be an extension")
	}

	if len(productEntity.Fields) != 3 {
		t.Errorf("expected 3 fields, got %d", len(productEntity.Fields))
	}
}

func TestNewSubGraphV2_WithExtension(t *testing.T) {
	schema := `
		extend type Product @key(fields: "id") {
			id: ID! @external
			reviews: [Review!]!
		}

		type Review {
			id: ID!
			rating: Int!
		}
	`

	sg, err := graph.NewSubGraphV2("review", []byte(schema), "http://review.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed: %v", err)
	}

	entities := sg.GetEntities()
	productEntity, ok := entities["Product"]
	if !ok {
		t.Fatal("Product entity not found")
	}

	if !productEntity.IsExtension() {
		t.Error("expected Product entity to be an extension")
	}
}

func TestNewSubGraphV2_WithDirectives(t *testing.T) {
	schema := `
		extend type Product @key(fields: "id") {
			id: ID! @external
			name: String! @external
			reviews: [Review!]! @requires(fields: "name")
		}

		type Review {
			id: ID!
			rating: Int!
			product: Product @provides(fields: "name")
		}
	`

	sg, err := graph.NewSubGraphV2("review", []byte(schema), "http://review.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed: %v", err)
	}

	entities := sg.GetEntities()
	productEntity, ok := entities["Product"]
	if !ok {
		t.Fatal("Product entity not found")
	}

	reviewsField, ok := productEntity.Fields["reviews"]
	if !ok {
		t.Fatal("reviews field not found")
	}

	if len(reviewsField.Requires) != 1 || reviewsField.Requires[0] != "name" {
		t.Errorf("expected requires 'name', got %v", reviewsField.Requires)
	}
}

func TestNewSubGraphV2_WithShareable(t *testing.T) {
	schema := `
		type Product @key(fields: "id") {
			id: ID!
			name: String! @shareable
		}
	`

	sg, err := graph.NewSubGraphV2("product", []byte(schema), "http://product.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed: %v", err)
	}

	entities := sg.GetEntities()
	productEntity, ok := entities["Product"]
	if !ok {
		t.Fatal("Product entity not found")
	}

	nameField, ok := productEntity.Fields["name"]
	if !ok {
		t.Fatal("name field not found")
	}

	if !nameField.IsShareable() {
		t.Error("expected name field to be shareable")
	}
}

func TestNewSubGraphV2_WithNonResolvableKey(t *testing.T) {
	schema := `
		type Product @key(fields: "id", resolvable: false) {
			id: ID!
			name: String!
		}
	`

	sg, err := graph.NewSubGraphV2("product", []byte(schema), "http://product.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed: %v", err)
	}

	entities := sg.GetEntities()
	productEntity, ok := entities["Product"]
	if !ok {
		t.Fatal("Product entity not found")
	}

	if len(productEntity.Keys) != 1 {
		t.Fatalf("expected 1 key, got %d", len(productEntity.Keys))
	}

	if productEntity.Keys[0].Resolvable {
		t.Error("expected key to be non-resolvable")
	}
}

func TestNewSubGraphV2_WithOverride(t *testing.T) {
	schema := `
		extend type Product @key(fields: "id") {
			id: ID! @external
			name: String! @override(from: "products")
		}
	`

	sg, err := graph.NewSubGraphV2("product-v2", []byte(schema), "http://product-v2.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed: %v", err)
	}

	entities := sg.GetEntities()
	productEntity, ok := entities["Product"]
	if !ok {
		t.Fatal("Product entity not found")
	}

	nameField, ok := productEntity.Fields["name"]
	if !ok {
		t.Fatal("name field not found")
	}

	override := nameField.GetOverride()
	if override == nil {
		t.Fatal("expected override metadata, got nil")
	}

	if override.From != "products" {
		t.Errorf("expected override from 'products', got '%s'", override.From)
	}
}

func TestNewSubGraphV2_WithInaccessible(t *testing.T) {
	schema := `
		type Product @key(fields: "id") {
			id: ID!
			name: String!
			internalCode: String! @inaccessible
		}
	`

	sg, err := graph.NewSubGraphV2("product", []byte(schema), "http://product.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed: %v", err)
	}

	entities := sg.GetEntities()
	productEntity, ok := entities["Product"]
	if !ok {
		t.Fatal("Product entity not found")
	}

	internalCodeField, ok := productEntity.Fields["internalCode"]
	if !ok {
		t.Fatal("internalCode field not found")
	}

	if !internalCodeField.IsInaccessible() {
		t.Error("expected internalCode field to be inaccessible")
	}

	// Check that other fields are not inaccessible
	nameField, ok := productEntity.Fields["name"]
	if !ok {
		t.Fatal("name field not found")
	}

	if nameField.IsInaccessible() {
		t.Error("expected name field to be accessible")
	}
}

func TestNewSubGraphV2_TypeLevelInaccessible(t *testing.T) {
	schema := `
		type InternalData @key(fields: "id") @inaccessible {
			id: ID!
			secret: String!
		}
	`

	sg, err := graph.NewSubGraphV2("internal", []byte(schema), "http://internal.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed: %v", err)
	}

	entities := sg.GetEntities()
	entity, ok := entities["InternalData"]
	if !ok {
		t.Fatal("InternalData entity not found")
	}

	if !entity.IsInaccessible() {
		t.Error("expected InternalData entity to be marked @inaccessible at type level")
	}
}

func TestNewSubGraphV2_TypeLevelInaccessible_RegularType(t *testing.T) {
	// A non-entity type can also be @inaccessible (no @key needed)
	schema := `
		type PublicProduct @key(fields: "id") {
			id: ID!
			name: String!
		}

		type InternalMetadata @key(fields: "id") @inaccessible {
			id: ID!
			internalField: String!
		}
	`

	sg, err := graph.NewSubGraphV2("service", []byte(schema), "http://service.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed: %v", err)
	}

	// PublicProduct should NOT be inaccessible
	publicEntity, ok := sg.GetEntities()["PublicProduct"]
	if !ok {
		t.Fatal("PublicProduct entity not found")
	}
	if publicEntity.IsInaccessible() {
		t.Error("expected PublicProduct to be accessible")
	}

	// InternalMetadata SHOULD be inaccessible
	internalEntity, ok := sg.GetEntities()["InternalMetadata"]
	if !ok {
		t.Fatal("InternalMetadata entity not found")
	}
	if !internalEntity.IsInaccessible() {
		t.Error("expected InternalMetadata to be inaccessible")
	}
}

func TestNewSubGraphV2_WithTag(t *testing.T) {
	schema := `
		type Product @key(fields: "id") {
			id: ID!
			name: String! @tag(name: "public")
			price: Float! @tag(name: "public") @tag(name: "partner")
		}
	`

	sg, err := graph.NewSubGraphV2("product", []byte(schema), "http://product.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed: %v", err)
	}

	entities := sg.GetEntities()
	productEntity, ok := entities["Product"]
	if !ok {
		t.Fatal("Product entity not found")
	}

	nameField, ok := productEntity.Fields["name"]
	if !ok {
		t.Fatal("name field not found")
	}

	tags := nameField.GetTags()
	if len(tags) != 1 {
		t.Fatalf("expected 1 tag, got %d", len(tags))
	}

	if tags[0] != "public" {
		t.Errorf("expected tag 'public', got '%s'", tags[0])
	}

	priceField, ok := productEntity.Fields["price"]
	if !ok {
		t.Fatal("price field not found")
	}

	tags = priceField.GetTags()
	if len(tags) != 2 {
		t.Fatalf("expected 2 tags, got %d", len(tags))
	}

	if tags[0] != "public" || tags[1] != "partner" {
		t.Errorf("expected tags 'public' and 'partner', got %v", tags)
	}
}

func TestNewSubGraphV2_WithInterfaceObject(t *testing.T) {
	schema := `
		type Node @key(fields: "id") @interfaceObject {
			id: ID!
		}
	`

	sg, err := graph.NewSubGraphV2("nodes", []byte(schema), "http://nodes.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed: %v", err)
	}

	entities := sg.GetEntities()
	nodeEntity, ok := entities["Node"]
	if !ok {
		t.Fatal("Node entity not found")
	}

	if !nodeEntity.IsInterfaceObject() {
		t.Error("expected Node entity to be an interface object")
	}
}

func TestNewSubGraphV2_WithComposeDirective(t *testing.T) {
	schema := `
		schema @composeDirective(name: "@custom") {
			query: Query
		}

		directive @custom on FIELD_DEFINITION

		type Product @key(fields: "id") {
			id: ID!
			name: String! @custom
		}

		type Query {
			product(id: ID!): Product
		}
	`

	sg, err := graph.NewSubGraphV2("product", []byte(schema), "http://product.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed: %v", err)
	}

	composeDirectives := sg.GetComposeDirectives()
	if len(composeDirectives) != 1 {
		t.Fatalf("expected 1 compose directive, got %d", len(composeDirectives))
	}

	if composeDirectives[0] != "@custom" {
		t.Errorf("expected compose directive '@custom', got '%s'", composeDirectives[0])
	}
}

func TestNewSubGraphV2_ExtractsDirectiveDefinitions(t *testing.T) {
	schema := `
		schema @composeDirective(name: "@rateLimit") {
			query: Query
		}

		directive @rateLimit(limit: Int!, duration: Int!) on FIELD_DEFINITION

		type Query {
			expensiveQuery: String! @rateLimit(limit: 10, duration: 60)
		}
	`

	sg, err := graph.NewSubGraphV2("product", []byte(schema), "http://product.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed: %v", err)
	}

	defs := sg.GetDirectiveDefinitions()
	if len(defs) != 1 {
		t.Fatalf("expected 1 directive definition, got %d", len(defs))
	}

	def, ok := defs["rateLimit"]
	if !ok {
		t.Fatal("expected 'rateLimit' directive definition, not found")
	}

	if def.Name.String() != "rateLimit" {
		t.Errorf("expected directive name 'rateLimit', got '%s'", def.Name.String())
	}
}

func TestNewSubGraphV2_ExtractsMultipleDirectiveDefinitions(t *testing.T) {
	schema := `
		schema
			@composeDirective(name: "@rateLimit")
			@composeDirective(name: "@cacheControl") {
			query: Query
		}

		directive @rateLimit(limit: Int!) on FIELD_DEFINITION
		directive @cacheControl(maxAge: Int!) on FIELD_DEFINITION | OBJECT

		type Query {
			cachedData: String! @cacheControl(maxAge: 3600) @rateLimit(limit: 100)
		}
	`

	sg, err := graph.NewSubGraphV2("product", []byte(schema), "http://product.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed: %v", err)
	}

	defs := sg.GetDirectiveDefinitions()
	if len(defs) != 2 {
		t.Fatalf("expected 2 directive definitions, got %d", len(defs))
	}

	if _, ok := defs["rateLimit"]; !ok {
		t.Error("expected 'rateLimit' directive definition, not found")
	}
	if _, ok := defs["cacheControl"]; !ok {
		t.Error("expected 'cacheControl' directive definition, not found")
	}
}

func TestNewSubGraphV2_DirectiveDefinitions_OnlyComposed(t *testing.T) {
	// @internal is defined but NOT listed in @composeDirective, so should not be extracted
	schema := `
		schema @composeDirective(name: "@rateLimit") {
			query: Query
		}

		directive @rateLimit(limit: Int!) on FIELD_DEFINITION
		directive @internal on FIELD_DEFINITION

		type Query {
			data: String! @rateLimit(limit: 10) @internal
		}
	`

	sg, err := graph.NewSubGraphV2("product", []byte(schema), "http://product.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed: %v", err)
	}

	defs := sg.GetDirectiveDefinitions()
	if len(defs) != 1 {
		t.Fatalf("expected 1 directive definition (only composed ones), got %d", len(defs))
	}

	if _, ok := defs["rateLimit"]; !ok {
		t.Error("expected 'rateLimit' directive definition, not found")
	}
	if _, ok := defs["internal"]; ok {
		t.Error("'internal' directive should not be extracted (not in @composeDirective)")
	}
}

func TestNewSubGraphV2_NoComposeDirective_EmptyDefinitions(t *testing.T) {
	schema := `
		type Product @key(fields: "id") {
			id: ID!
			name: String!
		}

		type Query {
			product(id: ID!): Product
		}
	`

	sg, err := graph.NewSubGraphV2("product", []byte(schema), "http://product.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed: %v", err)
	}

	defs := sg.GetDirectiveDefinitions()
	if len(defs) != 0 {
		t.Errorf("expected 0 directive definitions, got %d", len(defs))
	}
}

// TestNewSubGraphV2_InterfaceTypeDefinition_WithInterfaceObject tests that
// InterfaceTypeDefinition with @interfaceObject and @key is parsed as an entity.
func TestNewSubGraphV2_InterfaceTypeDefinition_WithInterfaceObject(t *testing.T) {
	schema := `
		interface Node @interfaceObject @key(fields: "id") {
			id: ID!
		}

		type Query {
			node(id: ID!): Node
		}
	`

	sg, err := graph.NewSubGraphV2("core", []byte(schema), "http://core.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed: %v", err)
	}

	entities := sg.GetEntities()
	nodeEntity, ok := entities["Node"]
	if !ok {
		t.Fatal("Node entity not found")
	}

	if !nodeEntity.IsInterfaceObject() {
		t.Error("expected Node entity to be an interface object")
	}

	if len(nodeEntity.Keys) != 1 {
		t.Fatalf("expected 1 key, got %d", len(nodeEntity.Keys))
	}

	if nodeEntity.Keys[0].FieldSet != "id" {
		t.Errorf("expected key field 'id', got '%s'", nodeEntity.Keys[0].FieldSet)
	}

	if !nodeEntity.Keys[0].Resolvable {
		t.Error("expected key to be resolvable")
	}

	if nodeEntity.IsExtension() {
		t.Error("expected Node entity to not be an extension")
	}

	idField, ok := nodeEntity.Fields["id"]
	if !ok {
		t.Fatal("id field not found in Node entity")
	}

	if idField.Name != "id" {
		t.Errorf("expected field name 'id', got '%s'", idField.Name)
	}
}

// TestNewSubGraphV2_InterfaceTypeDefinition_WithKeyOnly tests that
// InterfaceTypeDefinition with only @key (no @interfaceObject) is parsed as an entity.
func TestNewSubGraphV2_InterfaceTypeDefinition_WithKeyOnly(t *testing.T) {
	schema := `
		interface Media @key(fields: "id") {
			id: ID!
			title: String!
		}
	`

	sg, err := graph.NewSubGraphV2("media", []byte(schema), "http://media.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed: %v", err)
	}

	entities := sg.GetEntities()
	mediaEntity, ok := entities["Media"]
	if !ok {
		t.Fatal("Media entity not found")
	}

	// @key only, no @interfaceObject → isInterfaceObject should be false
	if mediaEntity.IsInterfaceObject() {
		t.Error("expected Media entity to NOT be an interface object (no @interfaceObject directive)")
	}

	if len(mediaEntity.Keys) != 1 || mediaEntity.Keys[0].FieldSet != "id" {
		t.Errorf("expected key 'id', got %v", mediaEntity.Keys)
	}
}

// TestNewSubGraphV2_InterfaceTypeExtension_WithInterfaceObject tests that
// InterfaceTypeExtension with @interfaceObject and @key is parsed as an entity extension.
func TestNewSubGraphV2_InterfaceTypeExtension_WithInterfaceObject(t *testing.T) {
	schema := `
		extend interface Node @interfaceObject @key(fields: "id") {
			id: ID!
			metadata: String!
		}
	`

	sg, err := graph.NewSubGraphV2("metadata", []byte(schema), "http://metadata.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed: %v", err)
	}

	entities := sg.GetEntities()
	nodeEntity, ok := entities["Node"]
	if !ok {
		t.Fatal("Node entity not found")
	}

	if !nodeEntity.IsInterfaceObject() {
		t.Error("expected Node entity to be an interface object")
	}

	if !nodeEntity.IsExtension() {
		t.Error("expected Node entity to be an extension")
	}

	if len(nodeEntity.Keys) != 1 || nodeEntity.Keys[0].FieldSet != "id" {
		t.Errorf("expected key 'id', got %v", nodeEntity.Keys)
	}

	metaField, ok := nodeEntity.Fields["metadata"]
	if !ok {
		t.Fatal("metadata field not found in Node entity")
	}

	if metaField.Name != "metadata" {
		t.Errorf("expected field name 'metadata', got '%s'", metaField.Name)
	}
}

// TestNewSubGraphV2_Tag_FieldLevel tests that @tag directives on fields are parsed correctly.
func TestNewSubGraphV2_Tag_FieldLevel(t *testing.T) {
	schema := `
		type Product @key(fields: "id") {
			id: ID!
			name: String! @tag(name: "public")
			internalCost: Float! @tag(name: "internal")
		}
	`

	sg, err := graph.NewSubGraphV2("product", []byte(schema), "http://product.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed: %v", err)
	}

	entities := sg.GetEntities()
	productEntity, ok := entities["Product"]
	if !ok {
		t.Fatal("Product entity not found")
	}

	nameField, ok := productEntity.Fields["name"]
	if !ok {
		t.Fatal("name field not found")
	}
	if len(nameField.GetTags()) != 1 || nameField.GetTags()[0] != "public" {
		t.Errorf("expected name field tags ['public'], got %v", nameField.GetTags())
	}

	costField, ok := productEntity.Fields["internalCost"]
	if !ok {
		t.Fatal("internalCost field not found")
	}
	if len(costField.GetTags()) != 1 || costField.GetTags()[0] != "internal" {
		t.Errorf("expected internalCost field tags ['internal'], got %v", costField.GetTags())
	}

	idField, ok := productEntity.Fields["id"]
	if !ok {
		t.Fatal("id field not found")
	}
	if len(idField.GetTags()) != 0 {
		t.Errorf("expected id field to have no tags, got %v", idField.GetTags())
	}
}

// TestNewSubGraphV2_Tag_TypeLevel tests that @tag directives on types are parsed correctly.
func TestNewSubGraphV2_Tag_TypeLevel(t *testing.T) {
	schema := `
		type Product @key(fields: "id") @tag(name: "public") {
			id: ID!
			name: String!
		}
	`

	sg, err := graph.NewSubGraphV2("product", []byte(schema), "http://product.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed: %v", err)
	}

	entities := sg.GetEntities()
	productEntity, ok := entities["Product"]
	if !ok {
		t.Fatal("Product entity not found")
	}

	if len(productEntity.GetTags()) != 1 || productEntity.GetTags()[0] != "public" {
		t.Errorf("expected Product entity tags ['public'], got %v", productEntity.GetTags())
	}
}

// TestNewSubGraphV2_Tag_MultipleTags tests parsing of multiple @tag directives on a type and its fields.
func TestNewSubGraphV2_Tag_MultipleTags(t *testing.T) {
	schema := `
		type Product @key(fields: "id") @tag(name: "public") @tag(name: "ecommerce") {
			id: ID!
			name: String! @tag(name: "public") @tag(name: "search")
		}
	`

	sg, err := graph.NewSubGraphV2("product", []byte(schema), "http://product.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed: %v", err)
	}

	entities := sg.GetEntities()
	productEntity, ok := entities["Product"]
	if !ok {
		t.Fatal("Product entity not found")
	}

	typeTags := productEntity.GetTags()
	if len(typeTags) != 2 {
		t.Errorf("expected 2 type tags, got %d: %v", len(typeTags), typeTags)
	}
	typeTagSet := make(map[string]bool)
	for _, tag := range typeTags {
		typeTagSet[tag] = true
	}
	if !typeTagSet["public"] || !typeTagSet["ecommerce"] {
		t.Errorf("expected type tags ['public', 'ecommerce'], got %v", typeTags)
	}

	nameField, ok := productEntity.Fields["name"]
	if !ok {
		t.Fatal("name field not found")
	}
	fieldTags := nameField.GetTags()
	if len(fieldTags) != 2 {
		t.Errorf("expected 2 field tags, got %d: %v", len(fieldTags), fieldTags)
	}
	fieldTagSet := make(map[string]bool)
	for _, tag := range fieldTags {
		fieldTagSet[tag] = true
	}
	if !fieldTagSet["public"] || !fieldTagSet["search"] {
		t.Errorf("expected field tags ['public', 'search'], got %v", fieldTags)
	}
}

// TestNewSubGraphV2_Tag_NoTags tests that entities and fields without @tag have empty tag slices.
func TestNewSubGraphV2_Tag_NoTags(t *testing.T) {
	schema := `
		type Product @key(fields: "id") {
			id: ID!
			name: String!
		}
	`

	sg, err := graph.NewSubGraphV2("product", []byte(schema), "http://product.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed: %v", err)
	}

	entities := sg.GetEntities()
	productEntity, ok := entities["Product"]
	if !ok {
		t.Fatal("Product entity not found")
	}

	if len(productEntity.GetTags()) != 0 {
		t.Errorf("expected no type tags, got %v", productEntity.GetTags())
	}

	for _, field := range productEntity.Fields {
		if len(field.GetTags()) != 0 {
			t.Errorf("expected no field tags for '%s', got %v", field.Name, field.GetTags())
		}
	}
}

// TestNewSubGraphV2_InterfaceTypeDefinition_MultipleFields tests that all fields
// of an interface entity are properly parsed.
func TestNewSubGraphV2_InterfaceTypeDefinition_MultipleFields(t *testing.T) {
	schema := `
		interface Node @interfaceObject @key(fields: "id") {
			id: ID!
			createdAt: String!
			updatedAt: String!
		}
	`

	sg, err := graph.NewSubGraphV2("timestamps", []byte(schema), "http://timestamps.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed: %v", err)
	}

	entities := sg.GetEntities()
	nodeEntity, ok := entities["Node"]
	if !ok {
		t.Fatal("Node entity not found")
	}

	if len(nodeEntity.Fields) != 3 {
		t.Errorf("expected 3 fields (id, createdAt, updatedAt), got %d", len(nodeEntity.Fields))
	}

	for _, fieldName := range []string{"id", "createdAt", "updatedAt"} {
		if _, ok := nodeEntity.Fields[fieldName]; !ok {
			t.Errorf("field '%s' not found in Node entity", fieldName)
		}
	}
}
