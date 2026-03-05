// badges service: extends User using @key(fields: "username") — the SECOND @key of the owner
// This demonstrates correct multiple @key support: even though users service has @key(fields:"id")
// as its first @key, badges must receive representations keyed by "username".
package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
)

const sdl = `extend schema
  @link(url: "https://specs.apollo.dev/federation/v2.0", import: ["@key", "@external"])

extend type User @key(fields: "username") {
  username: String! @external
  badges: [String!]!
}`

// badgesByUsername maps username → list of badges.
var badgesByUsername = map[string][]string{
	"alice": {"early-adopter", "power-user"},
	"bob":   {"contributor"},
	"carol": {"early-adopter", "contributor", "mentor"},
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
	default:
		writeJSON(w, map[string]interface{}{"data": map[string]interface{}{"__typename": "Query"}})
	}
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
		// Must receive "username" key (this service declares @key(fields: "username"))
		username, ok := repMap["username"].(string)
		if !ok {
			// If we receive "id" instead of "username", that's the bug we're fixing
			entities = append(entities, nil)
			continue
		}
		badges := badgesByUsername[username]
		if badges == nil {
			badges = []string{}
		}
		entities = append(entities, map[string]interface{}{
			"__typename": "User",
			"badges":     badges,
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
	log.Printf("badges service listening on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}
