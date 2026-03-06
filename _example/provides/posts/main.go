// posts service: owns Post with @provides(fields: "name") on the author field.
// Returns author.name = "PROVIDED_<username>" — a value DISTINCT from what the
// users service would return. This distinction proves the @provides optimization
// is working: if the test passes, the gateway used posts' provided data and did
// NOT call the users service.
package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
)

const sdl = `extend schema
  @link(url: "https://specs.apollo.dev/federation/v2.0", import: ["@key", "@external", "@provides"])

type Post @key(fields: "id") {
  id: ID!
  title: String!
  author: User! @provides(fields: "name")
}

type User @key(fields: "id") {
  id: ID! @external
  name: String! @external
}

type Query {
  post(id: ID!): Post
}`

type postData struct {
	ID       string
	Title    string
	AuthorID string
	AuthorName string // Provided directly (not fetched from users service)
}

var posts = map[string]postData{
	"p1": {ID: "p1", Title: "Federation Post 1", AuthorID: "u1", AuthorName: "PROVIDED_alice"},
	"p2": {ID: "p2", Title: "Federation Post 2", AuthorID: "u2", AuthorName: "PROVIDED_bob"},
	"p3": {ID: "p3", Title: "Federation Post 3", AuthorID: "u3", AuthorName: "PROVIDED_carol"},
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
	case strings.Contains(req.Query, "post"):
		handlePostQuery(w, req)
	default:
		writeJSON(w, map[string]interface{}{"data": map[string]interface{}{"__typename": "Query"}})
	}
}

func handlePostQuery(w http.ResponseWriter, req gqlRequest) {
	id, _ := req.Variables["id"].(string)
	p, ok := posts[id]
	if !ok {
		writeJSON(w, map[string]interface{}{"data": map[string]interface{}{"post": nil}})
		return
	}
	writeJSON(w, map[string]interface{}{
		"data": map[string]interface{}{
			"post": map[string]interface{}{
				"__typename": "Post",
				"id":         p.ID,
				"title":      p.Title,
				// The author is returned WITH the provided "name" field.
				// This is the key of the @provides optimization: posts service
				// returns author.name directly so no users service call is needed.
				"author": map[string]interface{}{
					"__typename": "User",
					"id":         p.AuthorID,
					"name":       p.AuthorName,
				},
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
		id, _ := repMap["id"].(string)
		var found *postData
		for _, p := range posts {
			if p.ID == id {
				pp := p
				found = &pp
				break
			}
		}
		if found == nil {
			entities = append(entities, nil)
			continue
		}
		entities = append(entities, map[string]interface{}{
			"__typename": "Post",
			"id":         found.ID,
			"title":      found.Title,
			"author": map[string]interface{}{
				"__typename": "User",
				"id":         found.AuthorID,
				"name":       found.AuthorName,
			},
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
	log.Printf("posts service listening on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}
