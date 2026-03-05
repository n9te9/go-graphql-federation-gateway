package executor_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/n9te9/go-graphql-federation-gateway/federation/executor"
	"github.com/n9te9/go-graphql-federation-gateway/federation/graph"
	"github.com/n9te9/go-graphql-federation-gateway/federation/planner"
	"github.com/n9te9/graphql-parser/lexer"
	parserPkg "github.com/n9te9/graphql-parser/parser"
)

// TestExecutorV2_MultipleKey_AlternateKeyResolution verifies that when an entity has
// multiple @key directives and an extension subgraph uses a DIFFERENT key than the
// owner's first @key, the executor sends the correct representation to the extension.
//
// Scenario:
//   - users service: type User @key(fields: "id") @key(fields: "username")
//   - badges service: extend type User @key(fields: "username") { badges: [String!]! }
//
// Expected: executor sends { __typename: "User", username: "alice" } to badges service,
// NOT { __typename: "User", id: "u1" } (the owner's first @key).
func TestExecutorV2_MultipleKey_AlternateKeyResolution(t *testing.T) {
	usersSchema := `
		type User @key(fields: "id") @key(fields: "username") {
			id: ID!
			username: String!
			name: String!
		}

		type Query {
			user(id: ID!): User
		}
	`

	badgesSchema := `
		extend type User @key(fields: "username") {
			username: String! @external
			badges: [String!]!
		}
	`

	var receivedRepresentations []map[string]interface{}

	usersServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"data": map[string]interface{}{
				"user": map[string]interface{}{
					"__typename": "User",
					"id":         "u1",
					"username":   "alice",
					"name":       "Alice Smith",
				},
			},
		})
	}))
	defer usersServer.Close()

	badgesServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		json.NewDecoder(r.Body).Decode(&body)
		if vars, ok := body["variables"].(map[string]interface{}); ok {
			if reprs, ok := vars["representations"].([]interface{}); ok {
				for _, repr := range reprs {
					if m, ok := repr.(map[string]interface{}); ok {
						receivedRepresentations = append(receivedRepresentations, m)
					}
				}
			}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"data": map[string]interface{}{
				"_entities": []interface{}{
					map[string]interface{}{
						"__typename": "User",
						"badges":     []string{"early-adopter", "power-user"},
					},
				},
			},
		})
	}))
	defer badgesServer.Close()

	usersSG, err := graph.NewSubGraphV2("users", []byte(usersSchema), usersServer.URL+"/query")
	if err != nil {
		t.Fatalf("NewSubGraphV2 (users) failed: %v", err)
	}
	badgesSG, err := graph.NewSubGraphV2("badges", []byte(badgesSchema), badgesServer.URL+"/query")
	if err != nil {
		t.Fatalf("NewSubGraphV2 (badges) failed: %v", err)
	}

	sg, err := graph.NewSuperGraphV2([]*graph.SubGraphV2{usersSG, badgesSG})
	if err != nil {
		t.Fatalf("NewSuperGraphV2 failed: %v", err)
	}

	p := planner.NewPlannerV2(sg)

	query := `
		query {
			user(id: "u1") {
				name
				badges
			}
		}
	`

	l := lexer.New(query)
	par := parserPkg.New(l)
	doc := par.ParseDocument()
	if len(par.Errors()) > 0 {
		t.Fatalf("parse error: %v", par.Errors())
	}

	plan, err := p.Plan(doc, nil)
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}

	exec := executor.NewExecutorV2(&http.Client{}, sg)
	resp, err := exec.Execute(context.Background(), plan, nil)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	// Verify response contains badges
	data, ok := resp["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected data in response, got %v", resp)
	}
	user, ok := data["user"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected user in data, got %v", data)
	}
	if user["name"] != "Alice Smith" {
		t.Errorf("expected name='Alice Smith', got %v", user["name"])
	}
	if user["badges"] == nil {
		t.Errorf("expected badges to be non-nil, got nil")
	}

	// CRITICAL: Verify badges service received username-keyed representation, NOT id-keyed
	if len(receivedRepresentations) == 0 {
		t.Fatal("expected badges service to receive entity representations")
	}
	repr := receivedRepresentations[0]

	if repr["__typename"] != "User" {
		t.Errorf("expected __typename='User', got %v", repr["__typename"])
	}

	// The representation MUST use "username" (badges service's @key), not "id" (owner's first @key)
	if repr["username"] == nil {
		t.Errorf("expected 'username' in representation (badges service uses @key(fields: \"username\")), got %v", repr)
	}
	if repr["username"] != "alice" {
		t.Errorf("expected username='alice', got %v", repr["username"])
	}

	// "id" must NOT be the sole key in representation (badges service doesn't use id @key)
	if repr["id"] != nil && repr["username"] == nil {
		t.Errorf("representation used owner's 'id' key instead of badges service's 'username' key: %v", repr)
	}
}

// TestExecutorV2_MultipleKey_OwnerFirstKeyStillWorks verifies that when an entity step
// targets the OWNER subgraph, the owner's first @key is correctly used (no regression).
func TestExecutorV2_MultipleKey_OwnerFirstKeyStillWorks(t *testing.T) {
	// role is ONLY in admin service (not in users service) so an entity step is required
	usersSchema := `
		type User @key(fields: "id") @key(fields: "username") {
			id: ID!
			username: String!
			name: String!
		}

		type Query {
			user(id: ID!): User
		}
	`

	// admin service extends User using the FIRST key (id) — same as owner's first key
	adminSchema := `
		extend type User @key(fields: "id") {
			id: ID! @external
			role: String!
		}
	`

	var receivedRepresentations []map[string]interface{}

	usersServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"data": map[string]interface{}{
				"user": map[string]interface{}{
					"__typename": "User",
					"id":         "u1",
					"username":   "alice",
					"name":       "Alice Smith",
				},
			},
		})
	}))
	defer usersServer.Close()

	adminServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		json.NewDecoder(r.Body).Decode(&body)
		if vars, ok := body["variables"].(map[string]interface{}); ok {
			if reprs, ok := vars["representations"].([]interface{}); ok {
				for _, repr := range reprs {
					if m, ok := repr.(map[string]interface{}); ok {
						receivedRepresentations = append(receivedRepresentations, m)
					}
				}
			}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"data": map[string]interface{}{
				"_entities": []interface{}{
					map[string]interface{}{"__typename": "User", "role": "admin"},
				},
			},
		})
	}))
	defer adminServer.Close()

	usersSG, err := graph.NewSubGraphV2("users", []byte(usersSchema), usersServer.URL+"/query")
	if err != nil {
		t.Fatalf("NewSubGraphV2 (users) failed: %v", err)
	}
	adminSG, err := graph.NewSubGraphV2("admin", []byte(adminSchema), adminServer.URL+"/query")
	if err != nil {
		t.Fatalf("NewSubGraphV2 (admin) failed: %v", err)
	}

	sg, err := graph.NewSuperGraphV2([]*graph.SubGraphV2{usersSG, adminSG})
	if err != nil {
		t.Fatalf("NewSuperGraphV2 failed: %v", err)
	}

	p := planner.NewPlannerV2(sg)

	query := `
		query {
			user(id: "u1") {
				name
				role
			}
		}
	`

	l := lexer.New(query)
	par := parserPkg.New(l)
	doc := par.ParseDocument()
	if len(par.Errors()) > 0 {
		t.Fatalf("parse error: %v", par.Errors())
	}

	plan, err := p.Plan(doc, nil)
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}

	exec := executor.NewExecutorV2(&http.Client{}, sg)
	resp, err := exec.Execute(context.Background(), plan, nil)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	data := resp["data"].(map[string]interface{})
	user := data["user"].(map[string]interface{})
	if user["role"] != "admin" {
		t.Errorf("expected role='admin', got %v", user["role"])
	}

	// admin service uses @key(fields: "id"), so representation should use "id"
	if len(receivedRepresentations) == 0 {
		t.Fatal("expected admin service to receive entity representations")
	}
	repr := receivedRepresentations[0]
	if repr["id"] == nil {
		t.Errorf("expected 'id' in representation for admin service (uses @key(fields: \"id\")), got %v", repr)
	}
	if repr["id"] != "u1" {
		t.Errorf("expected id='u1', got %v", repr["id"])
	}
}
