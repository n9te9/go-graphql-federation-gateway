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
