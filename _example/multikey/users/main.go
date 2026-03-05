// users service: owns User with multiple @key directives (id AND username)
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

type User @key(fields: "id") @key(fields: "username") {
  id: ID!
  username: String!
  name: String!
}

type Query {
  user(id: ID!): User
}`

type user struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Name     string `json:"name"`
}

var usersByID = map[string]user{
	"u1": {ID: "u1", Username: "alice", Name: "Alice Smith"},
	"u2": {ID: "u2", Username: "bob", Name: "Bob Jones"},
	"u3": {ID: "u3", Username: "carol", Name: "Carol White"},
}

var usersByUsername = map[string]user{
	"alice": {ID: "u1", Username: "alice", Name: "Alice Smith"},
	"bob":   {ID: "u2", Username: "bob", Name: "Bob Jones"},
	"carol": {ID: "u3", Username: "carol", Name: "Carol White"},
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
	u, ok := usersByID[id]
	if !ok {
		writeJSON(w, map[string]interface{}{"data": map[string]interface{}{"user": nil}})
		return
	}
	writeJSON(w, map[string]interface{}{
		"data": map[string]interface{}{
			"user": map[string]interface{}{
				"__typename": "User",
				"id":         u.ID,
				"username":   u.Username,
				"name":       u.Name,
			},
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
		// Support resolution by "id" (first @key)
		if id, ok := repMap["id"].(string); ok {
			if u, found := usersByID[id]; found {
				entities = append(entities, map[string]interface{}{
					"__typename": "User", "id": u.ID, "username": u.Username, "name": u.Name,
				})
				continue
			}
		}
		// Support resolution by "username" (second @key)
		if username, ok := repMap["username"].(string); ok {
			if u, found := usersByUsername[username]; found {
				entities = append(entities, map[string]interface{}{
					"__typename": "User", "id": u.ID, "username": u.Username, "name": u.Name,
				})
				continue
			}
		}
		entities = append(entities, nil)
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
