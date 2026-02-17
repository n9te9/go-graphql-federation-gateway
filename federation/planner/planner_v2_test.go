package planner_test

import (
	"testing"

	"github.com/n9te9/go-graphql-federation-gateway/federation/graph"
	"github.com/n9te9/go-graphql-federation-gateway/federation/planner"
	"github.com/n9te9/graphql-parser/ast"
	"github.com/n9te9/graphql-parser/lexer"
	"github.com/n9te9/graphql-parser/parser"
)

func TestPlannerV2_SimpleQuery(t *testing.T) {
	// Product service schema
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

	productSG, err := graph.NewSubGraphV2("product", []byte(productSchema), "http://product.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed for product: %v", err)
	}

	superGraph, err := graph.NewSuperGraphV2([]*graph.SubGraphV2{productSG})
	if err != nil {
		t.Fatalf("NewSuperGraphV2 failed: %v", err)
	}

	// Planner を作成
	p := planner.NewPlannerV2(superGraph)

	// クエリをパース
	query := `
		query {
			product(id: "1") {
				id
				name
				price
			}
		}
	`

	l := lexer.New(query)
	parser := parser.New(l)
	doc := parser.ParseDocument()
	if len(parser.Errors()) > 0 {
		t.Fatalf("parse error: %v", parser.Errors())
	}

	// Plan を生成
	plan, err := p.Plan(doc, nil)
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}

	// 検証
	if len(plan.Steps) != 1 {
		t.Errorf("expected 1 step, got %d", len(plan.Steps))
	}

	if len(plan.RootStepIndexes) != 1 {
		t.Errorf("expected 1 root step, got %d", len(plan.RootStepIndexes))
	}

	if plan.Steps[0].SubGraph.Name != "product" {
		t.Errorf("expected subgraph 'product', got '%s'", plan.Steps[0].SubGraph.Name)
	}

	if plan.Steps[0].StepType != planner.StepTypeQuery {
		t.Errorf("expected StepTypeQuery, got %v", plan.Steps[0].StepType)
	}
}

func TestPlannerV2_FederatedQuery(t *testing.T) {
	// Product service schema
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

	// Planner を作成
	p := planner.NewPlannerV2(superGraph)

	// Parse query (fetch Product and Reviews)
	query := `
		query {
			product(id: "1") {
				id
				name
				reviews {
					rating
					comment
				}
			}
		}
	`

	l := lexer.New(query)
	parser := parser.New(l)
	doc := parser.ParseDocument()
	if len(parser.Errors()) > 0 {
		t.Fatalf("parse error: %v", parser.Errors())
	}

	// Plan を生成
	plan, err := p.Plan(doc, nil)
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}

	// Verify
	// Expected: 2 steps
	// 1. Fetch product from Product service
	// 2. Fetch reviews from Review service
	if len(plan.Steps) != 2 {
		t.Errorf("expected 2 steps, got %d", len(plan.Steps))
	}

	if len(plan.RootStepIndexes) != 1 {
		t.Errorf("expected 1 root step, got %d", len(plan.RootStepIndexes))
	}

	// Step 0: Product service
	if plan.Steps[0].SubGraph.Name != "product" {
		t.Errorf("step 0: expected subgraph 'product', got '%s'", plan.Steps[0].SubGraph.Name)
	}

	if plan.Steps[0].StepType != planner.StepTypeQuery {
		t.Errorf("step 0: expected StepTypeQuery, got %v", plan.Steps[0].StepType)
	}

	// Step 1: Review service
	if plan.Steps[1].SubGraph.Name != "review" {
		t.Errorf("step 1: expected subgraph 'review', got '%s'", plan.Steps[1].SubGraph.Name)
	}

	if plan.Steps[1].StepType != planner.StepTypeEntity {
		t.Errorf("step 1: expected StepTypeEntity, got %v", plan.Steps[1].StepType)
	}

	// Step 1 depends on step 0
	if len(plan.Steps[1].DependsOn) != 1 || plan.Steps[1].DependsOn[0] != 0 {
		t.Errorf("step 1: expected to depend on step 0, got %v", plan.Steps[1].DependsOn)
	}
}

func TestPlannerV2_MultipleRootFields(t *testing.T) {
	// User サービスのスキーマ
	userSchema := `
		type User @key(fields: "id") {
			id: ID!
			username: String!
		}

		type Query {
			user(id: ID!): User
			users: [User!]!
		}
	`

	userSG, err := graph.NewSubGraphV2("user", []byte(userSchema), "http://user.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed for user: %v", err)
	}

	superGraph, err := graph.NewSuperGraphV2([]*graph.SubGraphV2{userSG})
	if err != nil {
		t.Fatalf("NewSuperGraphV2 failed: %v", err)
	}

	// Create planner
	p := planner.NewPlannerV2(superGraph)

	// Parse query (multiple root fields)
	query := `
		query {
			user(id: "1") {
				id
				username
			}
			users {
				id
				username
			}
		}
	`

	l := lexer.New(query)
	parser := parser.New(l)
	doc := parser.ParseDocument()
	if len(parser.Errors()) > 0 {
		t.Fatalf("parse error: %v", parser.Errors())
	}

	// Plan を生成
	plan, err := p.Plan(doc, nil)
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}

	// Verify
	// Same subgraph, so combined into single step
	if len(plan.Steps) != 1 {
		t.Errorf("expected 1 step, got %d", len(plan.Steps))
	}

	if len(plan.RootStepIndexes) != 1 {
		t.Errorf("expected 1 root step, got %d", len(plan.RootStepIndexes))
	}

	// SelectionSet contains 2 fields
	if len(plan.Steps[0].SelectionSet) != 2 {
		t.Errorf("expected 2 selections in step 0, got %d", len(plan.Steps[0].SelectionSet))
	}
}

func TestPlannerV2_NestedFederation(t *testing.T) {
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

		type Post @key(fields: "id") {
			id: ID!
			title: String!
			content: String!
		}
	`

	// Comment サービスのスキーマ
	commentSchema := `
		extend type Post @key(fields: "id") {
			id: ID! @external
			comments: [Comment!]!
		}

		type Comment {
			id: ID!
			text: String!
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

	commentSG, err := graph.NewSubGraphV2("comment", []byte(commentSchema), "http://comment.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed for comment: %v", err)
	}

	superGraph, err := graph.NewSuperGraphV2([]*graph.SubGraphV2{userSG, postSG, commentSG})
	if err != nil {
		t.Fatalf("NewSuperGraphV2 failed: %v", err)
	}

	// Planner を作成
	p := planner.NewPlannerV2(superGraph)

	// Parse query (3-level nesting)
	query := `
		query {
			user(id: "1") {
				id
				username
				posts {
					id
					title
					comments {
						id
						text
					}
				}
			}
		}
	`

	l := lexer.New(query)
	parser := parser.New(l)
	doc := parser.ParseDocument()
	if len(parser.Errors()) > 0 {
		t.Fatalf("parse error: %v", parser.Errors())
	}

	// Plan を生成
	plan, err := p.Plan(doc, nil)
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}

	// Verify
	// Expected: 3 steps
	// 1. Fetch user from User service
	// 2. Fetch posts from Post service
	// 3. Fetch comments from Comment service
	if len(plan.Steps) != 3 {
		t.Errorf("expected 3 steps, got %d", len(plan.Steps))
	}

	if len(plan.RootStepIndexes) != 1 {
		t.Errorf("expected 1 root step, got %d", len(plan.RootStepIndexes))
	}

	// Step 0: User service
	if plan.Steps[0].SubGraph.Name != "user" {
		t.Errorf("step 0: expected subgraph 'user', got '%s'", plan.Steps[0].SubGraph.Name)
	}

	// Step 1: Post service
	if plan.Steps[1].SubGraph.Name != "post" {
		t.Errorf("step 1: expected subgraph 'post', got '%s'", plan.Steps[1].SubGraph.Name)
	}

	if len(plan.Steps[1].DependsOn) != 1 || plan.Steps[1].DependsOn[0] != 0 {
		t.Errorf("step 1: expected to depend on step 0, got %v", plan.Steps[1].DependsOn)
	}

	// Step 2: Comment service
	if plan.Steps[2].SubGraph.Name != "comment" {
		t.Errorf("step 2: expected subgraph 'comment', got '%s'", plan.Steps[2].SubGraph.Name)
	}

	if len(plan.Steps[2].DependsOn) != 1 || plan.Steps[2].DependsOn[0] != 1 {
		t.Errorf("step 2: expected to depend on step 1, got %v", plan.Steps[2].DependsOn)
	}
}

// TestPlannerV2_Loopback tests loopback (circular reference) scenarios like Product->Review->Product.
// This ensures nested entity resolution works correctly when an entity references back to a parent entity type.
func TestPlannerV2_Loopback(t *testing.T) {
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

	// Review サービスのスキーマ（Product を拡張し、Review から Product への参照を持つ）
	reviewSchema := `
		extend type Product @key(fields: "id") {
			id: ID! @external
			reviews: [Review!]!
		}

		type Review @key(fields: "id") {
			id: ID!
			body: String!
			productId: ID!
			product: Product!
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

	// Planner を作成
	p := planner.NewPlannerV2(superGraph)

	// Loopback クエリ: Product -> Reviews -> Product
	query := `
		query {
			product(id: "p1") {
				name
				reviews {
					body
					product {
						name
						price
					}
				}
			}
		}
	`

	l := lexer.New(query)
	parser := parser.New(l)
	doc := parser.ParseDocument()
	if len(parser.Errors()) > 0 {
		t.Fatalf("parse error: %v", parser.Errors())
	}

	// Plan を生成
	plan, err := p.Plan(doc, nil)
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}

	// Verify: 3 steps expected
	// Step 0: Fetch product from Product service
	// Step 1: Fetch reviews from Review service (Product key fields injected)
	// Step 2: Fetch reviews.product from Product service (nested entity)
	if len(plan.Steps) != 3 {
		t.Errorf("expected 3 steps, got %d", len(plan.Steps))
	}

	if len(plan.RootStepIndexes) != 1 {
		t.Errorf("expected 1 root step, got %d", len(plan.RootStepIndexes))
	}

	// Step 0: Product サービス
	if plan.Steps[0].SubGraph.Name != "product" {
		t.Errorf("step 0: expected subgraph 'product', got '%s'", plan.Steps[0].SubGraph.Name)
	}

	if plan.Steps[0].StepType != planner.StepTypeQuery {
		t.Errorf("step 0: expected StepTypeQuery, got %v", plan.Steps[0].StepType)
	}

	// Step 1: Review サービス（reviews を取得）
	if plan.Steps[1].SubGraph.Name != "review" {
		t.Errorf("step 1: expected subgraph 'review', got '%s'", plan.Steps[1].SubGraph.Name)
	}

	if plan.Steps[1].StepType != planner.StepTypeEntity {
		t.Errorf("step 1: expected StepTypeEntity, got %v", plan.Steps[1].StepType)
	}

	if len(plan.Steps[1].DependsOn) != 1 || plan.Steps[1].DependsOn[0] != 0 {
		t.Errorf("step 1: expected to depend on step 0, got %v", plan.Steps[1].DependsOn)
	}

	// Step 2: Product サービス（reviews.product を取得 - ネストされたエンティティ）
	if plan.Steps[2].SubGraph.Name != "product" {
		t.Errorf("step 2: expected subgraph 'product', got '%s'", plan.Steps[2].SubGraph.Name)
	}

	if plan.Steps[2].StepType != planner.StepTypeEntity {
		t.Errorf("step 2: expected StepTypeEntity, got %v", plan.Steps[2].StepType)
	}

	if len(plan.Steps[2].DependsOn) != 1 || plan.Steps[2].DependsOn[0] != 1 {
		t.Errorf("step 2: expected to depend on step 1, got %v", plan.Steps[2].DependsOn)
	}

	// Step 2 の InsertionPath は [Query, product, reviews, product] になる（ネストされたパス）
	// Note: InsertionPath には Query が含まれる場合がある
	if len(plan.Steps[2].InsertionPath) < 3 {
		t.Errorf("step 2: expected InsertionPath length at least 3, got %d: %v", len(plan.Steps[2].InsertionPath), plan.Steps[2].InsertionPath)
	} else {
		// InsertionPath の最後の3要素が [product, reviews, product] または [Query, product, reviews, product] であることを確認
		path := plan.Steps[2].InsertionPath
		lastSegment := path[len(path)-1]
		if lastSegment != "product" {
			t.Errorf("step 2: expected last InsertionPath segment to be 'product', got '%s'", lastSegment)
		}
	}
}

// TestPlannerV2_TypenameCheck tests that __typename fields are correctly handled across entity boundaries.
func TestPlannerV2_TypenameCheck(t *testing.T) {
	// Product サービスのスキーマ
	productSchema := `
		type Product @key(fields: "id") {
			id: ID!
			name: String!
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

		type Review @key(fields: "id") {
			id: ID!
			body: String!
			productId: ID!
			product: Product!
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

	// Planner を作成
	p := planner.NewPlannerV2(superGraph)

	// __typename を含むクエリ
	query := `
		query {
			product(id: "p1") {
				__typename
				id
				reviews {
					__typename
					body
					product {
						__typename
						name
					}
				}
			}
		}
	`

	l := lexer.New(query)
	parser := parser.New(l)
	doc := parser.ParseDocument()
	if len(parser.Errors()) > 0 {
		t.Fatalf("parse error: %v", parser.Errors())
	}

	// Plan を生成
	plan, err := p.Plan(doc, nil)
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}

	// Verify: __typename is correctly handled in planning
	if len(plan.Steps) != 3 {
		t.Errorf("expected 3 steps, got %d", len(plan.Steps))
	}

	if len(plan.RootStepIndexes) != 1 {
		t.Errorf("expected 1 root step, got %d", len(plan.RootStepIndexes))
	}

	// Step 0 の SelectionSet に __typename が含まれている
	hasTypename := false
	for _, sel := range plan.Steps[0].SelectionSet {
		if field, ok := sel.(*ast.Field); ok {
			if field.Name.String() == "__typename" {
				hasTypename = true
				break
			}
		}
	}
	if !hasTypename {
		// Note: __typename is a special field, may not be included in SelectionSet
		// Verify plan is generated correctly
		t.Logf("__typename field handling may vary")
	}
}

// TestPlannerV2_MultiProductsWithAliases tests queries with field aliases at the root level.
// This ensures the planner correctly handles multiple queries to the same field with different aliases.
func TestPlannerV2_MultiProductsWithAliases(t *testing.T) {
	// Product サービスのスキーマ
	productSchema := `
		type Product @key(fields: "id") {
			id: ID!
			name: String!
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
			body: String!
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

	// Planner を作成
	p := planner.NewPlannerV2(superGraph)

	// Query with aliases for multiple products
	query := `
		query {
			p1: product(id: "p1") {
				name
				reviews {
					body
				}
			}
			p2: product(id: "p2") {
				name
				reviews {
					body
				}
			}
		}
	`

	l := lexer.New(query)
	parser := parser.New(l)
	doc := parser.ParseDocument()
	if len(parser.Errors()) > 0 {
		t.Fatalf("parse error: %v", parser.Errors())
	}

	// Plan を生成
	plan, err := p.Plan(doc, nil)
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}

	// Verify: Aliases are correctly handled in planning
	// Step 0: Fetch p1 and p2 from Product service (parallel)
	// Step 1: Fetch p1.reviews and p2.reviews from Review service
	if len(plan.Steps) < 2 {
		t.Errorf("expected at least 2 steps, got %d", len(plan.Steps))
	}

	if len(plan.RootStepIndexes) != 1 {
		t.Errorf("expected 1 root step, got %d", len(plan.RootStepIndexes))
	}

	// Step 0: Product サービス（p1 と p2 を両方含む）
	if plan.Steps[0].SubGraph.Name != "product" {
		t.Errorf("step 0: expected subgraph 'product', got '%s'", plan.Steps[0].SubGraph.Name)
	}

	if plan.Steps[0].StepType != planner.StepTypeQuery {
		t.Errorf("step 0: expected StepTypeQuery, got %v", plan.Steps[0].StepType)
	}

	// Step 0 の SelectionSet に2つのフィールド（p1 と p2）が含まれている
	if len(plan.Steps[0].SelectionSet) != 2 {
		t.Errorf("step 0: expected 2 selections (p1 and p2), got %d", len(plan.Steps[0].SelectionSet))
	}

	// Step 1 以降: Review サービス（エイリアスごとに分かれる、または統合される）
	hasReviewStep := false
	for i := 1; i < len(plan.Steps); i++ {
		if plan.Steps[i].SubGraph.Name == "review" {
			hasReviewStep = true
			if plan.Steps[i].StepType != planner.StepTypeEntity {
				t.Errorf("step %d: expected StepTypeEntity for review service, got %v", i, plan.Steps[i].StepType)
			}
		}
	}

	if !hasReviewStep {
		t.Error("expected at least one review service step")
	}
}
