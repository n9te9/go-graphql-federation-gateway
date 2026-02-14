package planner_test

import (
	"testing"

	"github.com/n9te9/go-graphql-federation-gateway/federation/graph"
	"github.com/n9te9/go-graphql-federation-gateway/federation/planner"
	"github.com/n9te9/graphql-parser/lexer"
	"github.com/n9te9/graphql-parser/parser"
)

func TestPlannerV2_SimpleQuery(t *testing.T) {
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

	// クエリをパース（Product と Reviews を取得）
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

	// 検証
	// 期待: 2つのステップ
	// 1. Product サービスで product を取得
	// 2. Review サービスで reviews を取得
	if len(plan.Steps) != 2 {
		t.Errorf("expected 2 steps, got %d", len(plan.Steps))
	}

	if len(plan.RootStepIndexes) != 1 {
		t.Errorf("expected 1 root step, got %d", len(plan.RootStepIndexes))
	}

	// ステップ 0: Product サービス
	if plan.Steps[0].SubGraph.Name != "product" {
		t.Errorf("step 0: expected subgraph 'product', got '%s'", plan.Steps[0].SubGraph.Name)
	}

	if plan.Steps[0].StepType != planner.StepTypeQuery {
		t.Errorf("step 0: expected StepTypeQuery, got %v", plan.Steps[0].StepType)
	}

	// ステップ 1: Review サービス
	if plan.Steps[1].SubGraph.Name != "review" {
		t.Errorf("step 1: expected subgraph 'review', got '%s'", plan.Steps[1].SubGraph.Name)
	}

	if plan.Steps[1].StepType != planner.StepTypeEntity {
		t.Errorf("step 1: expected StepTypeEntity, got %v", plan.Steps[1].StepType)
	}

	// ステップ 1 は ステップ 0 に依存
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

	// Planner を作成
	p := planner.NewPlannerV2(superGraph)

	// クエリをパース（複数のルートフィールド）
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

	// 検証
	// 同じサブグラフなので1つのステップにまとまる
	if len(plan.Steps) != 1 {
		t.Errorf("expected 1 step, got %d", len(plan.Steps))
	}

	if len(plan.RootStepIndexes) != 1 {
		t.Errorf("expected 1 root step, got %d", len(plan.RootStepIndexes))
	}

	// SelectionSet に2つのフィールドが含まれている
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

	// クエリをパース（3階層のネスト）
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

	// 検証
	// 期待: 3つのステップ
	// 1. User サービスで user を取得
	// 2. Post サービスで posts を取得
	// 3. Comment サービスで comments を取得
	if len(plan.Steps) != 3 {
		t.Errorf("expected 3 steps, got %d", len(plan.Steps))
	}

	if len(plan.RootStepIndexes) != 1 {
		t.Errorf("expected 1 root step, got %d", len(plan.RootStepIndexes))
	}

	// ステップ 0: User サービス
	if plan.Steps[0].SubGraph.Name != "user" {
		t.Errorf("step 0: expected subgraph 'user', got '%s'", plan.Steps[0].SubGraph.Name)
	}

	// ステップ 1: Post サービス
	if plan.Steps[1].SubGraph.Name != "post" {
		t.Errorf("step 1: expected subgraph 'post', got '%s'", plan.Steps[1].SubGraph.Name)
	}

	if len(plan.Steps[1].DependsOn) != 1 || plan.Steps[1].DependsOn[0] != 0 {
		t.Errorf("step 1: expected to depend on step 0, got %v", plan.Steps[1].DependsOn)
	}

	// ステップ 2: Comment サービス
	if plan.Steps[2].SubGraph.Name != "comment" {
		t.Errorf("step 2: expected subgraph 'comment', got '%s'", plan.Steps[2].SubGraph.Name)
	}

	if len(plan.Steps[2].DependsOn) != 1 || plan.Steps[2].DependsOn[0] != 1 {
		t.Errorf("step 2: expected to depend on step 1, got %v", plan.Steps[2].DependsOn)
	}
}
