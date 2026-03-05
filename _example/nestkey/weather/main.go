// weather service: extends Location @key(fields: "coordinate { lat lng }") with weather field
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

extend type Location @key(fields: "coordinate { lat lng }") {
  coordinate: Coordinate! @external
  weather: String!
}

type Coordinate {
  lat: Float!
  lng: Float!
}`

// weatherDB maps (lat, lng) → weather description.
// Keys are [2]float64{lat, lng}.
var weatherDB = map[[2]float64]string{
	{35.6762, 139.6503}: "Sunny, 22°C",
	{35.6580, 139.7016}: "Cloudy, 20°C",
	{35.6896, 139.6917}: "Rainy, 18°C",
}

func findWeather(lat, lng float64) string {
	if w, ok := weatherDB[[2]float64{lat, lng}]; ok {
		return w
	}
	return "Unknown"
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
		coordRaw, ok := repMap["coordinate"].(map[string]interface{})
		if !ok {
			entities = append(entities, nil)
			continue
		}
		lat, _ := coordRaw["lat"].(float64)
		lng, _ := coordRaw["lng"].(float64)
		entities = append(entities, map[string]interface{}{
			"__typename": "Location",
			"weather":    findWeather(lat, lng),
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
	log.Printf("weather service listening on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}
