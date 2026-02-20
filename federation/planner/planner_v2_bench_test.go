package planner_test

// Benchmark tests comparing Plan() (v0.1.3 algorithm) vs PlanOptimized() (Dijkstra + @provides).
//
// Run with:
//
//	go test -bench=. -benchmem ./federation/planner/
//
// Scenarios
// ---------
//  1. SingleSubGraph        – fast-path: single owner for every root field.
//  2. EntityFetch           – two subgraphs joined by @key; no @provides.
//  3. ProvidesFullyCovered  – @provides covers all queried child fields;
//                             PlanOptimized skips an entity fetch that Plan emits.
//  4. ProvidesPartial       – @provides covers only a subset; both planners emit an entity step.
//  5. ThreeSubgraphs        – three-hop chain: products → reviews → users.

import (
	"testing"

	"github.com/n9te9/go-graphql-federation-gateway/federation/graph"
	"github.com/n9te9/go-graphql-federation-gateway/federation/planner"
	"github.com/n9te9/graphql-parser/lexer"
	"github.com/n9te9/graphql-parser/parser"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func mustSubGraph(b *testing.B, name, schema, host string) *graph.SubGraphV2 {
	b.Helper()
	sg, err := graph.NewSubGraphV2(name, []byte(schema), host)
	if err != nil {
		b.Fatalf("NewSubGraphV2(%s): %v", name, err)
	}
	return sg
}

func mustSuperGraph(b *testing.B, sgs ...*graph.SubGraphV2) *graph.SuperGraphV2 {
	b.Helper()
	sg, err := graph.NewSuperGraphV2(sgs)
	if err != nil {
		b.Fatalf("NewSuperGraphV2: %v", err)
	}
	return sg
}

// ---------------------------------------------------------------------------
// 1. Single subgraph – fast path
// ---------------------------------------------------------------------------

func BenchmarkPlan_SingleSubGraph(b *testing.B) {
	const schema = `
		type Product @key(fields: "id") {
			id: ID!
			name: String!
			price: Float!
		}
		type Query {
			product(id: ID!): Product
		}
	`
	const query = `query { product(id: "1") { id name price } }`

	sg := mustSuperGraph(b, mustSubGraph(b, "products", schema, "http://products.example.com"))
	pl := planner.NewPlannerV2(sg)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		l := lexer.New(query)
		pr := parser.New(l)
		doc := pr.ParseDocument()
		if _, err := pl.Plan(doc, nil); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPlanOptimized_SingleSubGraph(b *testing.B) {
	const schema = `
		type Product @key(fields: "id") {
			id: ID!
			name: String!
			price: Float!
		}
		type Query {
			product(id: ID!): Product
		}
	`
	const query = `query { product(id: "1") { id name price } }`

	sg := mustSuperGraph(b, mustSubGraph(b, "products", schema, "http://products.example.com"))
	pl := planner.NewPlannerV2(sg)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		l := lexer.New(query)
		pr := parser.New(l)
		doc := pr.ParseDocument()
		if _, err := pl.PlanOptimized(doc, nil); err != nil {
			b.Fatal(err)
		}
	}
}

// ---------------------------------------------------------------------------
// 2. Two subgraphs – entity fetch via @key (no @provides)
// ---------------------------------------------------------------------------

func BenchmarkPlan_EntityFetch(b *testing.B) {
	const productSchema = `
		type Product @key(fields: "id") {
			id: ID!
			name: String!
		}
		type Query {
			product(id: ID!): Product
		}
	`
	const reviewSchema = `
		extend type Product @key(fields: "id") {
			id: ID! @external
			reviews: [Review!]!
		}
		type Review {
			id: ID!
			body: String!
		}
		extend type Query {
			review(id: ID!): Review
		}
	`
	const query = `query { product(id: "1") { id name reviews { id body } } }`

	sg := mustSuperGraph(b,
		mustSubGraph(b, "products", productSchema, "http://products.example.com"),
		mustSubGraph(b, "reviews", reviewSchema, "http://reviews.example.com"),
	)
	pl := planner.NewPlannerV2(sg)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		l := lexer.New(query)
		pr := parser.New(l)
		doc := pr.ParseDocument()
		if _, err := pl.Plan(doc, nil); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPlanOptimized_EntityFetch(b *testing.B) {
	const productSchema = `
		type Product @key(fields: "id") {
			id: ID!
			name: String!
		}
		type Query {
			product(id: ID!): Product
		}
	`
	const reviewSchema = `
		extend type Product @key(fields: "id") {
			id: ID! @external
			reviews: [Review!]!
		}
		type Review {
			id: ID!
			body: String!
		}
		extend type Query {
			review(id: ID!): Review
		}
	`
	const query = `query { product(id: "1") { id name reviews { id body } } }`

	sg := mustSuperGraph(b,
		mustSubGraph(b, "products", productSchema, "http://products.example.com"),
		mustSubGraph(b, "reviews", reviewSchema, "http://reviews.example.com"),
	)
	pl := planner.NewPlannerV2(sg)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		l := lexer.New(query)
		pr := parser.New(l)
		doc := pr.ParseDocument()
		if _, err := pl.PlanOptimized(doc, nil); err != nil {
			b.Fatal(err)
		}
	}
}

// ---------------------------------------------------------------------------
// 3. @provides fully covered – PlanOptimized skips the entity fetch
//
// @provides(fields: "name") covers the only queried child field "name".
// Plan()          → emits an entity fetch step to the products subgraph
// PlanOptimized() → skips that entity fetch (uses the provided data)
// ---------------------------------------------------------------------------

func BenchmarkPlan_ProvidesFullyCovered(b *testing.B) {
	const reviewSchema = `
		type Review @key(fields: "id") {
			id: ID!
			body: String!
			product: Product! @provides(fields: "name")
		}
		extend type Product @key(fields: "upc") {
			upc: String! @external
			name: String @external
		}
		type Query {
			review(id: ID!): Review
		}
	`
	const productSchema = `
		type Product @key(fields: "upc") {
			upc: String!
			name: String!
			price: Int!
		}
		type Query {
			product(upc: String!): Product
		}
	`
	// Two root fields from different subgraphs → Dijkstra path in PlanOptimized.
	const query = `
		query {
			review(id: "1") { id body product { name } }
			product(upc: "abc") { upc name }
		}
	`

	sg := mustSuperGraph(b,
		mustSubGraph(b, "reviews", reviewSchema, "http://reviews.example.com"),
		mustSubGraph(b, "products", productSchema, "http://products.example.com"),
	)
	pl := planner.NewPlannerV2(sg)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		l := lexer.New(query)
		pr := parser.New(l)
		doc := pr.ParseDocument()
		if _, err := pl.Plan(doc, nil); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPlanOptimized_ProvidesFullyCovered(b *testing.B) {
	const reviewSchema = `
		type Review @key(fields: "id") {
			id: ID!
			body: String!
			product: Product! @provides(fields: "name")
		}
		extend type Product @key(fields: "upc") {
			upc: String! @external
			name: String @external
		}
		type Query {
			review(id: ID!): Review
		}
	`
	const productSchema = `
		type Product @key(fields: "upc") {
			upc: String!
			name: String!
			price: Int!
		}
		type Query {
			product(upc: String!): Product
		}
	`
	const query = `
		query {
			review(id: "1") { id body product { name } }
			product(upc: "abc") { upc name }
		}
	`

	sg := mustSuperGraph(b,
		mustSubGraph(b, "reviews", reviewSchema, "http://reviews.example.com"),
		mustSubGraph(b, "products", productSchema, "http://products.example.com"),
	)
	pl := planner.NewPlannerV2(sg)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		l := lexer.New(query)
		pr := parser.New(l)
		doc := pr.ParseDocument()
		if _, err := pl.PlanOptimized(doc, nil); err != nil {
			b.Fatal(err)
		}
	}
}

// ---------------------------------------------------------------------------
// 4. @provides partial – entity fetch still required for non-provided field
//
// @provides(fields: "name") does NOT cover "price".
// Both Plan() and PlanOptimized() should emit an entity fetch step for products.
// ---------------------------------------------------------------------------

func BenchmarkPlan_ProvidesPartial(b *testing.B) {
	const reviewSchema = `
		type Review @key(fields: "id") {
			id: ID!
			body: String!
			product: Product! @provides(fields: "name")
		}
		extend type Product @key(fields: "upc") {
			upc: String! @external
			name: String @external
		}
		type Query {
			review(id: ID!): Review
		}
	`
	const productSchema = `
		type Product @key(fields: "upc") {
			upc: String!
			name: String!
			price: Int!
		}
		type Query {
			product(upc: String!): Product
		}
	`
	const query = `
		query {
			review(id: "1") { id body product { name price } }
			product(upc: "abc") { upc name }
		}
	`

	sg := mustSuperGraph(b,
		mustSubGraph(b, "reviews", reviewSchema, "http://reviews.example.com"),
		mustSubGraph(b, "products", productSchema, "http://products.example.com"),
	)
	pl := planner.NewPlannerV2(sg)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		l := lexer.New(query)
		pr := parser.New(l)
		doc := pr.ParseDocument()
		if _, err := pl.Plan(doc, nil); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPlanOptimized_ProvidesPartial(b *testing.B) {
	const reviewSchema = `
		type Review @key(fields: "id") {
			id: ID!
			body: String!
			product: Product! @provides(fields: "name")
		}
		extend type Product @key(fields: "upc") {
			upc: String! @external
			name: String @external
		}
		type Query {
			review(id: ID!): Review
		}
	`
	const productSchema = `
		type Product @key(fields: "upc") {
			upc: String!
			name: String!
			price: Int!
		}
		type Query {
			product(upc: String!): Product
		}
	`
	const query = `
		query {
			review(id: "1") { id body product { name price } }
			product(upc: "abc") { upc name }
		}
	`

	sg := mustSuperGraph(b,
		mustSubGraph(b, "reviews", reviewSchema, "http://reviews.example.com"),
		mustSubGraph(b, "products", productSchema, "http://products.example.com"),
	)
	pl := planner.NewPlannerV2(sg)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		l := lexer.New(query)
		pr := parser.New(l)
		doc := pr.ParseDocument()
		if _, err := pl.PlanOptimized(doc, nil); err != nil {
			b.Fatal(err)
		}
	}
}

// ---------------------------------------------------------------------------
// 5. Three-subgraph chain: products → reviews → users
// ---------------------------------------------------------------------------

func BenchmarkPlan_ThreeSubgraphs(b *testing.B) {
	const productSchema = `
		type Product @key(fields: "id") {
			id: ID!
			name: String!
			price: Int!
		}
		type Query {
			product(id: ID!): Product
		}
	`
	const reviewSchema = `
		extend type Product @key(fields: "id") {
			id: ID! @external
			reviews: [Review!]!
		}
		type Review @key(fields: "id") {
			id: ID!
			body: String!
			author: User!
		}
		type User @key(fields: "id") {
			id: ID!
		}
		extend type Query {
			review(id: ID!): Review
		}
	`
	const userSchema = `
		type User @key(fields: "id") {
			id: ID!
			name: String!
			email: String!
		}
		type Query {
			user(id: ID!): User
		}
	`
	const query = `
		query {
			product(id: "1") {
				id name price
				reviews {
					id body
					author { id name email }
				}
			}
		}
	`

	sg := mustSuperGraph(b,
		mustSubGraph(b, "products", productSchema, "http://products.example.com"),
		mustSubGraph(b, "reviews", reviewSchema, "http://reviews.example.com"),
		mustSubGraph(b, "users", userSchema, "http://users.example.com"),
	)
	pl := planner.NewPlannerV2(sg)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		l := lexer.New(query)
		pr := parser.New(l)
		doc := pr.ParseDocument()
		if _, err := pl.Plan(doc, nil); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPlanOptimized_ThreeSubgraphs(b *testing.B) {
	const productSchema = `
		type Product @key(fields: "id") {
			id: ID!
			name: String!
			price: Int!
		}
		type Query {
			product(id: ID!): Product
		}
	`
	const reviewSchema = `
		extend type Product @key(fields: "id") {
			id: ID! @external
			reviews: [Review!]!
		}
		type Review @key(fields: "id") {
			id: ID!
			body: String!
			author: User!
		}
		type User @key(fields: "id") {
			id: ID!
		}
		extend type Query {
			review(id: ID!): Review
		}
	`
	const userSchema = `
		type User @key(fields: "id") {
			id: ID!
			name: String!
			email: String!
		}
		type Query {
			user(id: ID!): User
		}
	`
	const query = `
		query {
			product(id: "1") {
				id name price
				reviews {
					id body
					author { id name email }
				}
			}
		}
	`

	sg := mustSuperGraph(b,
		mustSubGraph(b, "products", productSchema, "http://products.example.com"),
		mustSubGraph(b, "reviews", reviewSchema, "http://reviews.example.com"),
		mustSubGraph(b, "users", userSchema, "http://users.example.com"),
	)
	pl := planner.NewPlannerV2(sg)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		l := lexer.New(query)
		pr := parser.New(l)
		doc := pr.ParseDocument()
		if _, err := pl.PlanOptimized(doc, nil); err != nil {
			b.Fatal(err)
		}
	}
}
