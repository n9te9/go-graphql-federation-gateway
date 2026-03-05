// locations service: owns Location @key(fields: "coordinate { lat lng }")
package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
)

const sdl = `extend schema
  @link(url: "https://specs.apollo.dev/federation/v2.0", import: ["@key", "@shareable"])

type Location @key(fields: "coordinate { lat lng }") {
  coordinate: Coordinate!
  name: String!
}

type Coordinate @shareable {
  lat: Float!
  lng: Float!
}

type Query {
  location(id: ID!): Location
}`

type coordinate struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

type location struct {
	Name       string     `json:"name"`
	Coordinate coordinate `json:"coordinate"`
}

// locationDB stores all known locations indexed by id.
var locationDB = map[string]location{
	"loc1": {Name: "Tokyo Station", Coordinate: coordinate{Lat: 35.6762, Lng: 139.6503}},
	"loc2": {Name: "Shibuya", Coordinate: coordinate{Lat: 35.6580, Lng: 139.7016}},
	"loc3": {Name: "Shinjuku", Coordinate: coordinate{Lat: 35.6896, Lng: 139.6917}},
}

// findByCoordinate returns the location that has the given (lat, lng) coordinate.
func findByCoordinate(lat, lng float64) *location {
	for _, loc := range locationDB {
		if loc.Coordinate.Lat == lat && loc.Coordinate.Lng == lng {
			l := loc
			return &l
		}
	}
	return nil
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
	case strings.Contains(req.Query, "location"):
		handleLocationQuery(w, req)
	default:
		writeJSON(w, map[string]interface{}{"data": map[string]interface{}{"__typename": "Query"}})
	}
}

func handleLocationQuery(w http.ResponseWriter, req gqlRequest) {
	id, _ := req.Variables["id"].(string)
	loc, ok := locationDB[id]
	if !ok {
		writeJSON(w, map[string]interface{}{"data": map[string]interface{}{"location": nil}})
		return
	}
	writeJSON(w, map[string]interface{}{
		"data": map[string]interface{}{
			"location": map[string]interface{}{
				"__typename": "Location",
				"name":       loc.Name,
				"coordinate": map[string]interface{}{
					"lat": loc.Coordinate.Lat,
					"lng": loc.Coordinate.Lng,
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
		coordRaw, ok := repMap["coordinate"].(map[string]interface{})
		if !ok {
			entities = append(entities, nil)
			continue
		}
		lat, _ := coordRaw["lat"].(float64)
		lng, _ := coordRaw["lng"].(float64)
		loc := findByCoordinate(lat, lng)
		if loc == nil {
			entities = append(entities, nil)
			continue
		}
		entities = append(entities, map[string]interface{}{
			"__typename": "Location",
			"name":       loc.Name,
			"coordinate": map[string]interface{}{"lat": loc.Coordinate.Lat, "lng": loc.Coordinate.Lng},
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
	log.Printf("locations service listening on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}
