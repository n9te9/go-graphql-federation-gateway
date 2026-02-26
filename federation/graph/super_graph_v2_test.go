package graph_test

import (
	"testing"

	"github.com/n9te9/go-graphql-federation-gateway/federation/graph"
	"github.com/n9te9/graphql-parser/ast"
)

func TestNewSuperGraphV2(t *testing.T) {
	// Product サービスのスキーマ
	productSchema := `
		type Product @key(fields: "id") {
			id: ID!
			name: String!
			price: Float!
		}

		type Query {
			product(id: ID!): Product
		}
	`

	// Review サービスのスキーマ
	reviewSchema := `
		extend type Product @key(fields: "id") {
			id: ID! @external
			reviews: [Review!]!
		}

		type Review {
			id: ID!
			rating: Int!
			comment: String!
		}

		extend type Query {
			review(id: ID!): Review
		}
	`

	productSG, err := graph.NewSubGraphV2("product", []byte(productSchema), "http://product.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed for product: %v", err)
	}

	reviewSG, err := graph.NewSubGraphV2("review", []byte(reviewSchema), "http://review.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed for review: %v", err)
	}

	superGraph, err := graph.NewSuperGraphV2([]*graph.SubGraphV2{productSG, reviewSG})
	if err != nil {
		t.Fatalf("NewSuperGraphV2 failed: %v", err)
	}

	if len(superGraph.SubGraphs) != 2 {
		t.Errorf("expected 2 subgraphs, got %d", len(superGraph.SubGraphs))
	}

	if superGraph.Schema == nil {
		t.Fatal("expected schema to be composed")
	}

	// Ownership のチェック
	// Product.id は product サービスが所有
	productIDOwners := superGraph.GetSubGraphsForField("Product", "id")
	if len(productIDOwners) != 1 {
		t.Errorf("expected 1 owner for Product.id, got %d", len(productIDOwners))
	}
	if len(productIDOwners) > 0 && productIDOwners[0].Name != "product" {
		t.Errorf("expected Product.id to be owned by 'product', got '%s'", productIDOwners[0].Name)
	}

	// Product.reviews は review サービスが所有
	productReviewsOwners := superGraph.GetSubGraphsForField("Product", "reviews")
	if len(productReviewsOwners) != 1 {
		t.Errorf("expected 1 owner for Product.reviews, got %d", len(productReviewsOwners))
	}
	if len(productReviewsOwners) > 0 && productReviewsOwners[0].Name != "review" {
		t.Errorf("expected Product.reviews to be owned by 'review', got '%s'", productReviewsOwners[0].Name)
	}

	// Query.product は product サービスが所有
	queryProductOwners := superGraph.GetSubGraphsForField("Query", "product")
	if len(queryProductOwners) != 1 {
		t.Errorf("expected 1 owner for Query.product, got %d", len(queryProductOwners))
	}
	if len(queryProductOwners) > 0 && queryProductOwners[0].Name != "product" {
		t.Errorf("expected Query.product to be owned by 'product', got '%s'", queryProductOwners[0].Name)
	}
}

func TestNewSuperGraphV2_SchemaComposition(t *testing.T) {
	// User サービスのスキーマ
	userSchema := `
		type User @key(fields: "id") {
			id: ID!
			username: String!
		}

		type Query {
			user(id: ID!): User
		}
	`

	// Post サービスのスキーマ
	postSchema := `
		extend type User @key(fields: "id") {
			id: ID! @external
			posts: [Post!]!
		}

		type Post {
			id: ID!
			title: String!
			content: String!
		}
	`

	userSG, err := graph.NewSubGraphV2("user", []byte(userSchema), "http://user.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed for user: %v", err)
	}

	postSG, err := graph.NewSubGraphV2("post", []byte(postSchema), "http://post.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed for post: %v", err)
	}

	superGraph, err := graph.NewSuperGraphV2([]*graph.SubGraphV2{userSG, postSG})
	if err != nil {
		t.Fatalf("NewSuperGraphV2 failed: %v", err)
	}

	// スキーマが正しく合成されているか確認
	if superGraph.Schema == nil {
		t.Fatal("expected schema to be composed")
	}

	// User 型が存在するか確認
	var userTypeFound bool
	var postTypeFound bool
	for _, def := range superGraph.Schema.Definitions {
		if objDef, ok := def.(*ast.ObjectTypeDefinition); ok {
			switch objDef.Name.String() {
			case "User":
				userTypeFound = true
				// User 型のフィールド数を確認 (id, username, posts)
				if len(objDef.Fields) != 3 {
					t.Errorf("expected 3 fields for User, got %d", len(objDef.Fields))
				}
			case "Post":
				postTypeFound = true
			}
		}
	}

	if !userTypeFound {
		t.Error("User type not found in composed schema")
	}

	if !postTypeFound {
		t.Error("Post type not found in composed schema")
	}
}

func TestNewSuperGraphV2_EmptySubGraphs(t *testing.T) {
	_, err := graph.NewSuperGraphV2([]*graph.SubGraphV2{})
	if err == nil {
		t.Error("expected error for empty subgraphs, got nil")
	}
}

func TestNewSuperGraphV2_MultipleOwners(t *testing.T) {
	// Product サービス1のスキーマ
	productSchema1 := `
		type Product @key(fields: "id") {
			id: ID!
			name: String! @shareable
		}
	`

	// Product サービス2のスキーマ
	productSchema2 := `
		extend type Product @key(fields: "id") {
			id: ID! @external
			name: String! @shareable
			description: String!
		}
	`

	productSG1, err := graph.NewSubGraphV2("product1", []byte(productSchema1), "http://product1.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed for product1: %v", err)
	}

	productSG2, err := graph.NewSubGraphV2("product2", []byte(productSchema2), "http://product2.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed for product2: %v", err)
	}

	superGraph, err := graph.NewSuperGraphV2([]*graph.SubGraphV2{productSG1, productSG2})
	if err != nil {
		t.Fatalf("NewSuperGraphV2 failed: %v", err)
	}

	// Product.name は両方のサービスが所有（@shareable のため）
	productNameOwners := superGraph.GetSubGraphsForField("Product", "name")
	if len(productNameOwners) != 2 {
		t.Errorf("expected 2 owners for Product.name (shareable), got %d", len(productNameOwners))
	}

	// Product.description は product2 サービスのみが所有
	productDescOwners := superGraph.GetSubGraphsForField("Product", "description")
	if len(productDescOwners) != 1 {
		t.Errorf("expected 1 owner for Product.description, got %d", len(productDescOwners))
	}
	if len(productDescOwners) > 0 && productDescOwners[0].Name != "product2" {
		t.Errorf("expected Product.description to be owned by 'product2', got '%s'", productDescOwners[0].Name)
	}
}

// TestNewSuperGraphV2_MutationTypeComposition tests that Mutation types are properly composed
func TestNewSuperGraphV2_MutationTypeComposition(t *testing.T) {
	// Products サービスのスキーマ (Mutation type included with extend schema directive)
	productsSchema := `
		extend schema
		  @link(url: "https://specs.apollo.dev/federation/v2.0",
		        import: ["@key", "@shareable"])

		type Product @key(fields: "id") {
			id: ID!
			name: String!
			price: Int!
		}

		type Query {
			product(id: ID!): Product
		}

		type Mutation {
			createProduct(name: String!, price: Int!): Product
		}
	`

	// Users サービスのスキーマ (extend Mutation)
	usersSchema := `
		type User @key(fields: "id") {
			id: ID!
			username: String!
		}

		extend type Query {
			user(id: ID!): User
		}

		extend type Mutation {
			createUser(username: String!): User
		}
	`

	productsSG, err := graph.NewSubGraphV2("products", []byte(productsSchema), "http://products.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed for products: %v", err)
	}

	usersSG, err := graph.NewSubGraphV2("users", []byte(usersSchema), "http://users.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed for users: %v", err)
	}

	superGraph, err := graph.NewSuperGraphV2([]*graph.SubGraphV2{productsSG, usersSG})
	if err != nil {
		t.Fatalf("NewSuperGraphV2 failed: %v", err)
	}

	// Verify Mutation type exists in composed schema
	var mutationType *ast.ObjectTypeDefinition
	for _, def := range superGraph.Schema.Definitions {
		if objDef, ok := def.(*ast.ObjectTypeDefinition); ok {
			if objDef.Name.String() == "Mutation" {
				mutationType = objDef
				break
			}
		}
	}

	if mutationType == nil {
		t.Fatal("expected Mutation type to be in composed schema")
	}

	// Verify both createProduct and createUser fields exist
	hasCreateProduct := false
	hasCreateUser := false
	for _, field := range mutationType.Fields {
		if field.Name.String() == "createProduct" {
			hasCreateProduct = true
		}
		if field.Name.String() == "createUser" {
			hasCreateUser = true
		}
	}

	if !hasCreateProduct {
		t.Error("expected Mutation.createProduct field in composed schema")
	}
	if !hasCreateUser {
		t.Error("expected Mutation.createUser field in composed schema")
	}

	// Verify ownership
	createProductOwners := superGraph.GetSubGraphsForField("Mutation", "createProduct")
	if len(createProductOwners) != 1 {
		t.Errorf("expected 1 owner for Mutation.createProduct, got %d", len(createProductOwners))
	}
	if len(createProductOwners) > 0 && createProductOwners[0].Name != "products" {
		t.Errorf("expected Mutation.createProduct to be owned by 'products', got '%s'", createProductOwners[0].Name)
	}

	createUserOwners := superGraph.GetSubGraphsForField("Mutation", "createUser")
	if len(createUserOwners) != 1 {
		t.Errorf("expected 1 owner for Mutation.createUser, got %d", len(createUserOwners))
	}
	if len(createUserOwners) > 0 && createUserOwners[0].Name != "users" {
		t.Errorf("expected Mutation.createUser to be owned by 'users', got '%s'", createUserOwners[0].Name)
	}
}

// TestNewSuperGraphV2_ResolvableFalse tests that @key(resolvable: false) entities are excluded from ownership
func TestNewSuperGraphV2_ResolvableFalse(t *testing.T) {
	// Inventory service - defines Product stub (resolvable: false)
	// This service can extend Product but cannot resolve Product entities
	inventorySchema := `
		type Product @key(fields: "id", resolvable: false) {
			id: ID!
			inStock: Boolean!
		}

		type Query {
			checkInventory(productId: ID!): Boolean
		}
	`

	// Products service - defines Product entity (resolvable: true, default)
	productsSchema := `
		type Product @key(fields: "id") {
			id: ID!
			name: String!
			price: Int!
		}

		type Query {
			product(id: ID!): Product
		}
	`

	// Create subgraphs: inventory first, products second
	// If resolvable: false is not handled, inventory would be chosen
	inventorySG, err := graph.NewSubGraphV2("inventory", []byte(inventorySchema), "http://inventory.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed for inventory: %v", err)
	}

	productsSG, err := graph.NewSubGraphV2("products", []byte(productsSchema), "http://products.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed for products: %v", err)
	}

	superGraph, err := graph.NewSuperGraphV2([]*graph.SubGraphV2{inventorySG, productsSG})
	if err != nil {
		t.Fatalf("NewSuperGraphV2 failed: %v", err)
	}

	// Verify that Product entity owner is products service, not inventory
	entityOwner := superGraph.GetEntityOwnerSubGraph("Product")
	if entityOwner == nil {
		t.Fatal("expected Product to have an entity owner")
	}
	if entityOwner.Name != "products" {
		t.Errorf("expected Product entity owner to be 'products', got '%s'", entityOwner.Name)
	}

	// Verify Product.inStock is owned by inventory service
	inStockOwners := superGraph.GetSubGraphsForField("Product", "inStock")
	if len(inStockOwners) != 1 {
		t.Errorf("expected 1 owner for Product.inStock, got %d", len(inStockOwners))
	}
	if len(inStockOwners) > 0 && inStockOwners[0].Name != "inventory" {
		t.Errorf("expected Product.inStock to be owned by 'inventory', got '%s'", inStockOwners[0].Name)
	}

	// Verify Product entity is recognized as entity type
	if !superGraph.IsEntityType("Product") {
		t.Error("expected Product to be recognized as entity type")
	}
}

func TestNewSuperGraphV2_WithOverride(t *testing.T) {
	// Product service v1 (original owner of name field)
	productV1Schema := `
		type Product @key(fields: "id") {
			id: ID!
			name: String!
			price: Float!
		}

		type Query {
			product(id: ID!): Product
		}
	`

	// Product service v2 (overrides name field)
	productV2Schema := `
		extend type Product @key(fields: "id") {
			id: ID! @external
			name: String! @override(from: "products")
			description: String!
		}
	`

	productV1SG, err := graph.NewSubGraphV2("products", []byte(productV1Schema), "http://products.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed for products: %v", err)
	}

	productV2SG, err := graph.NewSubGraphV2("products-v2", []byte(productV2Schema), "http://products-v2.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed for products-v2: %v", err)
	}

	superGraph, err := graph.NewSuperGraphV2([]*graph.SubGraphV2{productV1SG, productV2SG})
	if err != nil {
		t.Fatalf("NewSuperGraphV2 failed: %v", err)
	}

	// Verify Product.name is owned by products-v2 (not products)
	nameOwners := superGraph.GetSubGraphsForField("Product", "name")
	if len(nameOwners) != 1 {
		t.Fatalf("expected 1 owner for Product.name, got %d", len(nameOwners))
	}
	if nameOwners[0].Name != "products-v2" {
		t.Errorf("expected Product.name to be owned by 'products-v2', got '%s'", nameOwners[0].Name)
	}

	// Verify Product.price is still owned by products
	priceOwners := superGraph.GetSubGraphsForField("Product", "price")
	if len(priceOwners) != 1 {
		t.Fatalf("expected 1 owner for Product.price, got %d", len(priceOwners))
	}
	if priceOwners[0].Name != "products" {
		t.Errorf("expected Product.price to be owned by 'products', got '%s'", priceOwners[0].Name)
	}

	// Verify Product.description is owned by products-v2
	descriptionOwners := superGraph.GetSubGraphsForField("Product", "description")
	if len(descriptionOwners) != 1 {
		t.Fatalf("expected 1 owner for Product.description, got %d", len(descriptionOwners))
	}
	if descriptionOwners[0].Name != "products-v2" {
		t.Errorf("expected Product.description to be owned by 'products-v2', got '%s'", descriptionOwners[0].Name)
	}

	// Verify GetFieldOwnerSubGraph returns correct owner
	nameOwner := superGraph.GetFieldOwnerSubGraph("Product", "name")
	if nameOwner == nil {
		t.Fatal("expected Product.name to have an owner")
	}
	if nameOwner.Name != "products-v2" {
		t.Errorf("expected GetFieldOwnerSubGraph to return 'products-v2', got '%s'", nameOwner.Name)
	}
}

func TestNewSuperGraphV2_MergesComposeDirectiveDefinitions(t *testing.T) {
	// Both subgraphs define the same @rateLimit directive via @composeDirective
	subGraphASchema := `
		schema @composeDirective(name: "@rateLimit") {
			query: Query
		}

		directive @rateLimit(limit: Int!) on FIELD_DEFINITION

		type Query {
			dataA: String! @rateLimit(limit: 10)
		}
	`

	subGraphBSchema := `
		schema @composeDirective(name: "@rateLimit") {
			query: Query
		}

		directive @rateLimit(limit: Int!) on FIELD_DEFINITION

		extend type Query {
			dataB: String! @rateLimit(limit: 20)
		}
	`

	sgA, err := graph.NewSubGraphV2("serviceA", []byte(subGraphASchema), "http://a.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed for serviceA: %v", err)
	}

	sgB, err := graph.NewSubGraphV2("serviceB", []byte(subGraphBSchema), "http://b.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed for serviceB: %v", err)
	}

	superGraph, err := graph.NewSuperGraphV2([]*graph.SubGraphV2{sgA, sgB})
	if err != nil {
		t.Fatalf("NewSuperGraphV2 failed: %v", err)
	}

	defs := superGraph.DirectiveDefinitions
	if len(defs) != 1 {
		t.Fatalf("expected 1 directive definition in super graph, got %d", len(defs))
	}

	def, ok := defs["rateLimit"]
	if !ok {
		t.Fatal("expected 'rateLimit' directive definition in super graph")
	}

	if def.Name.String() != "rateLimit" {
		t.Errorf("expected directive name 'rateLimit', got '%s'", def.Name.String())
	}
}

func TestNewSuperGraphV2_MergesDifferentComposeDirectives(t *testing.T) {
	// subGraphA defines @rateLimit, subGraphB defines @cacheControl
	subGraphASchema := `
		schema @composeDirective(name: "@rateLimit") {
			query: Query
		}

		directive @rateLimit(limit: Int!) on FIELD_DEFINITION

		type Query {
			dataA: String! @rateLimit(limit: 10)
		}
	`

	subGraphBSchema := `
		schema @composeDirective(name: "@cacheControl") {
			query: Query
		}

		directive @cacheControl(maxAge: Int!) on FIELD_DEFINITION | OBJECT

		extend type Query {
			dataB: String! @cacheControl(maxAge: 3600)
		}
	`

	sgA, err := graph.NewSubGraphV2("serviceA", []byte(subGraphASchema), "http://a.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed for serviceA: %v", err)
	}

	sgB, err := graph.NewSubGraphV2("serviceB", []byte(subGraphBSchema), "http://b.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed for serviceB: %v", err)
	}

	superGraph, err := graph.NewSuperGraphV2([]*graph.SubGraphV2{sgA, sgB})
	if err != nil {
		t.Fatalf("NewSuperGraphV2 failed: %v", err)
	}

	defs := superGraph.DirectiveDefinitions
	if len(defs) != 2 {
		t.Fatalf("expected 2 directive definitions in super graph, got %d", len(defs))
	}

	if _, ok := defs["rateLimit"]; !ok {
		t.Error("expected 'rateLimit' directive definition in super graph")
	}
	if _, ok := defs["cacheControl"]; !ok {
		t.Error("expected 'cacheControl' directive definition in super graph")
	}
}

func TestNewSuperGraphV2_InconsistentDirectiveDefinitions_ReturnsError(t *testing.T) {
	// subGraphA and subGraphB define @rateLimit with different argument names
	subGraphASchema := `
		schema @composeDirective(name: "@rateLimit") {
			query: Query
		}

		directive @rateLimit(limit: Int!) on FIELD_DEFINITION

		type Query {
			dataA: String! @rateLimit(limit: 10)
		}
	`

	subGraphBSchema := `
		schema @composeDirective(name: "@rateLimit") {
			query: Query
		}

		directive @rateLimit(max: Int!) on FIELD_DEFINITION

		extend type Query {
			dataB: String! @rateLimit(max: 20)
		}
	`

	sgA, err := graph.NewSubGraphV2("serviceA", []byte(subGraphASchema), "http://a.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed for serviceA: %v", err)
	}

	sgB, err := graph.NewSubGraphV2("serviceB", []byte(subGraphBSchema), "http://b.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed for serviceB: %v", err)
	}

	_, err = graph.NewSuperGraphV2([]*graph.SubGraphV2{sgA, sgB})
	if err == nil {
		t.Fatal("expected error for inconsistent directive definitions, got nil")
	}

	expectedMsg := "inconsistent directive definition for '@rateLimit' between subgraphs"
	if err.Error() != expectedMsg {
		t.Errorf("expected error '%s', got '%s'", expectedMsg, err.Error())
	}
}

func TestNewSuperGraphV2_NoComposeDirectives_EmptyDefinitions(t *testing.T) {
	// Subgraphs without @composeDirective should yield empty DirectiveDefinitions
	productSchema := `
		type Product @key(fields: "id") {
			id: ID!
			name: String!
		}

		type Query {
			product(id: ID!): Product
		}
	`

	sg, err := graph.NewSubGraphV2("product", []byte(productSchema), "http://product.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed: %v", err)
	}

	superGraph, err := graph.NewSuperGraphV2([]*graph.SubGraphV2{sg})
	if err != nil {
		t.Fatalf("NewSuperGraphV2 failed: %v", err)
	}

	if len(superGraph.DirectiveDefinitions) != 0 {
		t.Errorf("expected 0 directive definitions, got %d", len(superGraph.DirectiveDefinitions))
	}
}

// TestNewSuperGraphV2_InterfaceObject_Ownership tests that @interfaceObject entity fields
// are registered in the ownership map, allowing the planner to route correctly.
func TestNewSuperGraphV2_InterfaceObject_Ownership(t *testing.T) {
	// CoreService defines Node as interface entity (base definition)
	coreSchema := `
		interface Node @interfaceObject @key(fields: "id") {
			id: ID!
		}

		type Query {
			node(id: ID!): Node
		}
	`

	// MetadataService extends Node with additional fields
	metadataSchema := `
		extend interface Node @interfaceObject @key(fields: "id") {
			id: ID! @external
			metadata: String!
		}
	`

	coreSG, err := graph.NewSubGraphV2("core", []byte(coreSchema), "http://core.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed for core: %v", err)
	}

	metadataSG, err := graph.NewSubGraphV2("metadata", []byte(metadataSchema), "http://metadata.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed for metadata: %v", err)
	}

	superGraph, err := graph.NewSuperGraphV2([]*graph.SubGraphV2{coreSG, metadataSG})
	if err != nil {
		t.Fatalf("NewSuperGraphV2 failed: %v", err)
	}

	// Node.id should be owned by core
	nodeIDOwners := superGraph.GetSubGraphsForField("Node", "id")
	if len(nodeIDOwners) == 0 {
		t.Fatal("expected Node.id to have an owner, got none")
	}
	if nodeIDOwners[0].Name != "core" {
		t.Errorf("expected Node.id to be owned by 'core', got '%s'", nodeIDOwners[0].Name)
	}

	// Node.metadata should be owned by metadata service
	nodeMetaOwners := superGraph.GetSubGraphsForField("Node", "metadata")
	if len(nodeMetaOwners) == 0 {
		t.Fatal("expected Node.metadata to have an owner, got none")
	}
	if nodeMetaOwners[0].Name != "metadata" {
		t.Errorf("expected Node.metadata to be owned by 'metadata', got '%s'", nodeMetaOwners[0].Name)
	}
}

// TestNewSuperGraphV2_InterfaceObject_EntityOwner tests that GetEntityOwnerSubGraph
// works correctly for interface entities.
func TestNewSuperGraphV2_InterfaceObject_EntityOwner(t *testing.T) {
	// CoreService defines Node as interface entity
	coreSchema := `
		interface Node @interfaceObject @key(fields: "id") {
			id: ID!
		}

		type Query {
			node(id: ID!): Node
		}
	`

	// MetadataService extends Node (as interface extension with @interfaceObject)
	metadataSchema := `
		extend interface Node @interfaceObject @key(fields: "id") {
			id: ID! @external
			metadata: String!
		}
	`

	coreSG, err := graph.NewSubGraphV2("core", []byte(coreSchema), "http://core.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed for core: %v", err)
	}

	metadataSG, err := graph.NewSubGraphV2("metadata", []byte(metadataSchema), "http://metadata.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed for metadata: %v", err)
	}

	superGraph, err := graph.NewSuperGraphV2([]*graph.SubGraphV2{coreSG, metadataSG})
	if err != nil {
		t.Fatalf("NewSuperGraphV2 failed: %v", err)
	}

	// GetEntityOwnerSubGraph should return core for Node
	owner := superGraph.GetEntityOwnerSubGraph("Node")
	if owner == nil {
		t.Fatal("expected Node to have an entity owner, got nil")
	}
	if owner.Name != "core" {
		t.Errorf("expected Node entity owner to be 'core', got '%s'", owner.Name)
	}

	// IsEntityType should return true for Node
	if !superGraph.IsEntityType("Node") {
		t.Error("expected Node to be an entity type")
	}
}

// TestNewSuperGraphV2_InterfaceObject_FieldMerge tests that fields from multiple subgraphs
// are correctly merged for an interface entity.
func TestNewSuperGraphV2_InterfaceObject_FieldMerge(t *testing.T) {
	// SubgraphA defines interface Node with base fields
	subgraphASchema := `
		interface Node @interfaceObject @key(fields: "id") {
			id: ID!
		}

		type Query {
			node(id: ID!): Node
		}
	`

	// SubgraphB extends Node with timestamps
	subgraphBSchema := `
		extend interface Node @interfaceObject @key(fields: "id") {
			id: ID! @external
			createdAt: String!
			updatedAt: String!
		}
	`

	sgA, err := graph.NewSubGraphV2("sgA", []byte(subgraphASchema), "http://sga.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed for sgA: %v", err)
	}

	sgB, err := graph.NewSubGraphV2("sgB", []byte(subgraphBSchema), "http://sgb.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed for sgB: %v", err)
	}

	superGraph, err := graph.NewSuperGraphV2([]*graph.SubGraphV2{sgA, sgB})
	if err != nil {
		t.Fatalf("NewSuperGraphV2 failed: %v", err)
	}

	// createdAt should be owned by sgB
	createdAtOwners := superGraph.GetSubGraphsForField("Node", "createdAt")
	if len(createdAtOwners) == 0 {
		t.Fatal("expected Node.createdAt to have an owner, got none")
	}
	if createdAtOwners[0].Name != "sgB" {
		t.Errorf("expected Node.createdAt to be owned by 'sgB', got '%s'", createdAtOwners[0].Name)
	}

	// updatedAt should be owned by sgB
	updatedAtOwners := superGraph.GetSubGraphsForField("Node", "updatedAt")
	if len(updatedAtOwners) == 0 {
		t.Fatal("expected Node.updatedAt to have an owner, got none")
	}
	if updatedAtOwners[0].Name != "sgB" {
		t.Errorf("expected Node.updatedAt to be owned by 'sgB', got '%s'", updatedAtOwners[0].Name)
	}
}

// TestNewSuperGraphV2_InterfaceObject_WithObjectTypeInterfaceObject tests the case
// where @interfaceObject is on an object type (existing pattern) to ensure backward compat.
func TestNewSuperGraphV2_InterfaceObject_WithObjectTypeInterfaceObject(t *testing.T) {
	// CoreService: Node is defined as an object type with @interfaceObject
	coreSchema := `
		type Node @key(fields: "id") @interfaceObject {
			id: ID!
		}

		type Query {
			node(id: ID!): Node
		}
	`

	// ReviewsService: Node is extended as object type with @interfaceObject
	reviewsSchema := `
		extend type Node @key(fields: "id") @interfaceObject {
			id: ID! @external
			reviewCount: Int!
		}
	`

	coreSG, err := graph.NewSubGraphV2("core", []byte(coreSchema), "http://core.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed for core: %v", err)
	}

	reviewsSG, err := graph.NewSubGraphV2("reviews", []byte(reviewsSchema), "http://reviews.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed for reviews: %v", err)
	}

	superGraph, err := graph.NewSuperGraphV2([]*graph.SubGraphV2{coreSG, reviewsSG})
	if err != nil {
		t.Fatalf("NewSuperGraphV2 failed: %v", err)
	}

	// Node.id should be owned by core
	idOwners := superGraph.GetSubGraphsForField("Node", "id")
	if len(idOwners) == 0 {
		t.Fatal("expected Node.id to have an owner")
	}
	if idOwners[0].Name != "core" {
		t.Errorf("expected Node.id owned by 'core', got '%s'", idOwners[0].Name)
	}

	// Node.reviewCount should be owned by reviews
	reviewCountOwners := superGraph.GetSubGraphsForField("Node", "reviewCount")
	if len(reviewCountOwners) == 0 {
		t.Fatal("expected Node.reviewCount to have an owner")
	}
	if reviewCountOwners[0].Name != "reviews" {
		t.Errorf("expected Node.reviewCount owned by 'reviews', got '%s'", reviewCountOwners[0].Name)
	}
}

// TestSuperGraphV2_TagMetadata_GetTypeTags tests GetTypeTags returns tags for a type.
func TestSuperGraphV2_TagMetadata_GetTypeTags(t *testing.T) {
	productSchema := `
		type Product @key(fields: "id") @tag(name: "public") {
			id: ID!
			name: String!
		}

		type Query {
			product(id: ID!): Product
		}
	`

	sg, err := graph.NewSubGraphV2("product", []byte(productSchema), "http://product.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed: %v", err)
	}

	superGraph, err := graph.NewSuperGraphV2([]*graph.SubGraphV2{sg})
	if err != nil {
		t.Fatalf("NewSuperGraphV2 failed: %v", err)
	}

	tags := superGraph.GetTypeTags("Product")
	if len(tags) != 1 || tags[0] != "public" {
		t.Errorf("expected ['public'], got %v", tags)
	}

	emptyTags := superGraph.GetTypeTags("Query")
	if len(emptyTags) != 0 {
		t.Errorf("expected no tags for Query, got %v", emptyTags)
	}
}

// TestSuperGraphV2_TagMetadata_GetFieldTags tests GetFieldTags returns tags for a field.
func TestSuperGraphV2_TagMetadata_GetFieldTags(t *testing.T) {
	productSchema := `
		type Product @key(fields: "id") {
			id: ID!
			name: String! @tag(name: "public")
			internalCost: Float! @tag(name: "internal")
		}

		type Query {
			product(id: ID!): Product
		}
	`

	sg, err := graph.NewSubGraphV2("product", []byte(productSchema), "http://product.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed: %v", err)
	}

	superGraph, err := graph.NewSuperGraphV2([]*graph.SubGraphV2{sg})
	if err != nil {
		t.Fatalf("NewSuperGraphV2 failed: %v", err)
	}

	nameTags := superGraph.GetFieldTags("Product", "name")
	if len(nameTags) != 1 || nameTags[0] != "public" {
		t.Errorf("expected ['public'] for Product.name, got %v", nameTags)
	}

	costTags := superGraph.GetFieldTags("Product", "internalCost")
	if len(costTags) != 1 || costTags[0] != "internal" {
		t.Errorf("expected ['internal'] for Product.internalCost, got %v", costTags)
	}

	idTags := superGraph.GetFieldTags("Product", "id")
	if len(idTags) != 0 {
		t.Errorf("expected no tags for Product.id, got %v", idTags)
	}

	nilTags := superGraph.GetFieldTags("Product", "nonexistent")
	if nilTags != nil {
		t.Errorf("expected nil for nonexistent field, got %v", nilTags)
	}
}

// TestSuperGraphV2_TagMetadata_MergeFromMultipleSubgraphs tests that tags from multiple
// subgraphs are merged correctly.
func TestSuperGraphV2_TagMetadata_MergeFromMultipleSubgraphs(t *testing.T) {
	productSchema := `
		type Product @key(fields: "id") @tag(name: "public") {
			id: ID!
			name: String! @tag(name: "public")
		}

		type Query {
			product(id: ID!): Product
		}
	`

	reviewSchema := `
		extend type Product @key(fields: "id") {
			id: ID! @external
			internalCost: Float! @tag(name: "internal")
		}
	`

	productSG, err := graph.NewSubGraphV2("product", []byte(productSchema), "http://product.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed for product: %v", err)
	}

	reviewSG, err := graph.NewSubGraphV2("review", []byte(reviewSchema), "http://review.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed for review: %v", err)
	}

	superGraph, err := graph.NewSuperGraphV2([]*graph.SubGraphV2{productSG, reviewSG})
	if err != nil {
		t.Fatalf("NewSuperGraphV2 failed: %v", err)
	}

	// Type tags from product subgraph
	productTypeTags := superGraph.GetTypeTags("Product")
	if len(productTypeTags) != 1 || productTypeTags[0] != "public" {
		t.Errorf("expected Product type tags ['public'], got %v", productTypeTags)
	}

	// Field tags from product subgraph
	nameTags := superGraph.GetFieldTags("Product", "name")
	if len(nameTags) != 1 || nameTags[0] != "public" {
		t.Errorf("expected Product.name tags ['public'], got %v", nameTags)
	}

	// Field tags from review subgraph
	costTags := superGraph.GetFieldTags("Product", "internalCost")
	if len(costTags) != 1 || costTags[0] != "internal" {
		t.Errorf("expected Product.internalCost tags ['internal'], got %v", costTags)
	}
}

// TestSuperGraphV2_TagMetadata_Deduplication tests that duplicate tags from multiple
// subgraphs are deduplicated.
func TestSuperGraphV2_TagMetadata_Deduplication(t *testing.T) {
	schemaA := `
		type Product @key(fields: "id") @tag(name: "public") {
			id: ID!
			name: String! @tag(name: "public") @tag(name: "search")
		}

		type Query {
			product(id: ID!): Product
		}
	`

	schemaB := `
		extend type Product @key(fields: "id") @tag(name: "public") {
			id: ID! @external
			description: String! @tag(name: "public") @tag(name: "catalog")
		}
	`

	sgA, err := graph.NewSubGraphV2("sgA", []byte(schemaA), "http://a.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed for sgA: %v", err)
	}

	sgB, err := graph.NewSubGraphV2("sgB", []byte(schemaB), "http://b.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed for sgB: %v", err)
	}

	superGraph, err := graph.NewSuperGraphV2([]*graph.SubGraphV2{sgA, sgB})
	if err != nil {
		t.Fatalf("NewSuperGraphV2 failed: %v", err)
	}

	// Type tags: "public" should appear only once
	typeTags := superGraph.GetTypeTags("Product")
	if len(typeTags) != 1 || typeTags[0] != "public" {
		t.Errorf("expected deduplicated type tags ['public'], got %v", typeTags)
	}

	// Field tags for name: "public" and "search"
	nameTags := superGraph.GetFieldTags("Product", "name")
	if len(nameTags) != 2 {
		t.Errorf("expected 2 tags for Product.name, got %d: %v", len(nameTags), nameTags)
	}
	nameTagSet := make(map[string]bool)
	for _, tag := range nameTags {
		nameTagSet[tag] = true
	}
	if !nameTagSet["public"] || !nameTagSet["search"] {
		t.Errorf("expected ['public', 'search'] for Product.name, got %v", nameTags)
	}

	// Field tags for description: "public" and "catalog"
	descTags := superGraph.GetFieldTags("Product", "description")
	if len(descTags) != 2 {
		t.Errorf("expected 2 tags for Product.description, got %d: %v", len(descTags), descTags)
	}
	descTagSet := make(map[string]bool)
	for _, tag := range descTags {
		descTagSet[tag] = true
	}
	if !descTagSet["public"] || !descTagSet["catalog"] {
		t.Errorf("expected ['public', 'catalog'] for Product.description, got %v", descTags)
	}
}

// TestSuperGraphV2_TagMetadata_HasTypeTag tests HasTypeTag returns correct boolean.
func TestSuperGraphV2_TagMetadata_HasTypeTag(t *testing.T) {
	productSchema := `
		type Product @key(fields: "id") @tag(name: "public") {
			id: ID!
		}

		type Query {
			product(id: ID!): Product
		}
	`

	sg, err := graph.NewSubGraphV2("product", []byte(productSchema), "http://product.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed: %v", err)
	}

	superGraph, err := graph.NewSuperGraphV2([]*graph.SubGraphV2{sg})
	if err != nil {
		t.Fatalf("NewSuperGraphV2 failed: %v", err)
	}

	if !superGraph.HasTypeTag("Product", "public") {
		t.Error("expected HasTypeTag('Product', 'public') to be true")
	}

	if superGraph.HasTypeTag("Product", "internal") {
		t.Error("expected HasTypeTag('Product', 'internal') to be false")
	}

	if superGraph.HasTypeTag("NonExistent", "public") {
		t.Error("expected HasTypeTag for non-existent type to be false")
	}
}

// TestSuperGraphV2_TagMetadata_HasFieldTag tests HasFieldTag returns correct boolean.
func TestSuperGraphV2_TagMetadata_HasFieldTag(t *testing.T) {
	productSchema := `
		type Product @key(fields: "id") {
			id: ID!
			name: String! @tag(name: "public")
		}

		type Query {
			product(id: ID!): Product
		}
	`

	sg, err := graph.NewSubGraphV2("product", []byte(productSchema), "http://product.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed: %v", err)
	}

	superGraph, err := graph.NewSuperGraphV2([]*graph.SubGraphV2{sg})
	if err != nil {
		t.Fatalf("NewSuperGraphV2 failed: %v", err)
	}

	if !superGraph.HasFieldTag("Product", "name", "public") {
		t.Error("expected HasFieldTag('Product', 'name', 'public') to be true")
	}

	if superGraph.HasFieldTag("Product", "name", "internal") {
		t.Error("expected HasFieldTag('Product', 'name', 'internal') to be false")
	}

	if superGraph.HasFieldTag("Product", "id", "public") {
		t.Error("expected HasFieldTag('Product', 'id', 'public') to be false")
	}

	if superGraph.HasFieldTag("Product", "nonexistent", "public") {
		t.Error("expected HasFieldTag for non-existent field to be false")
	}
}
