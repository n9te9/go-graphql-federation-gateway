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
