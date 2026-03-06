// users service: owns User type.
// Returns name = "USERS_<id>" — intentionally DIFFERENT from posts service's @provides value.
// If @provides optimization is working, this service should NEVER be called when
// the query only requests fields covered by @provides. If it IS called, the response
// would contain "USERS_..." values which would cause the integration test to FAIL.
package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
)

const sdl = `extend schema
  @link(url: "https://specs.apollo.dev/federation/v2.0", import: ["@key"])

type User @key(fields: "id") {
  id: ID!
  name: String!
}

type Query {
  user(id: ID!): User
}`

var users = map[string]string{
	"u1": "USERS_alice",
	"u2": "USERS_bob",
	"u3": "USERS_carol",
}

type gqlRequest struct {
	Query     string                 `json:"query"`
	Variables map[string]interface{} `json:"variables"`
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}

func handleQuery(w http.ResponseWriter, r *http.Request) {
	var req gqlRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, map[string]interface{}{"errors": []map[string]interface{}{{"message": err.Error()}}})
		return
	}

	switch {
	case strings.Contains(req.Query, "_service"):
		writeJSON(w, map[string]interface{}{
			"data": map[string]interface{}{"_service": map[string]interface{}{"sdl": sdl}},
		})
	case strings.Contains(req.Query, "_entities"):
		handleEntities(w, req)
	case strings.Contains(req.Query, "user"):
		handleUserQuery(w, req)
	default:
		writeJSON(w, map[string]interface{}{"data": map[string]interface{}{"__typename": "Query"}})
	}
}

func handleUserQuery(w http.ResponseWriter, req gqlRequest) {
	id, _ := req.Variables["id"].(string)
	name, ok := users[id]
	if !ok {
		writeJSON(w, map[string]interface{}{"data": map[string]interface{}{"user": nil}})
		return
	}
	writeJSON(w, map[string]interface{}{
		"data": map[string]interface{}{
			"user": map[string]interface{}{"__typename": "User", "id": id, "name": name},
		},
	})
}

func handleEntities(w http.ResponseWriter, req gqlRequest) {
	repsRaw, _ := req.Variables["representations"].([]interface{})
	entities := make([]interface{}, 0, len(repsRaw))
	for _, repRaw := range repsRaw {
		repMap, ok := repRaw.(map[string]interface{})
		if !ok {
			entities = append(entities, nil)
			continue
		}
		id, _ := repMap["id"].(string)
		name, ok := users[id]
		if !ok {
			entities = append(entities, nil)
			continue
		}
		entities = append(entities, map[string]interface{}{
			"__typename": "User",
			"id":         id,
			"name":       name, // "USERS_alice" etc. — indicates optimization failed if seen in response
		})
	}
	writeJSON(w, map[string]interface{}{
		"data": map[string]interface{}{"_entities": entities},
	})
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	http.HandleFunc("/query", handleQuery)
	log.Printf("users service listening on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}
