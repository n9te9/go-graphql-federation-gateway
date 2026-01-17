package main

import (
	"io"
	"log"
	"net/http"
	"strings"
)

func main() {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		bodyStr := string(bodyBytes)

		// Federation の _entities クエリか判定
		if strings.Contains(bodyStr, "_entities") {
			w.Header().Set("Content-Type", "application/json")
			// テストで期待されているレスポンス
			response := `{"data": {"_entities": [{"weight": 10.0, "height": 20.0}, null]}}`
			w.Write([]byte(response))
			log.Println("Responded to _entities query")
			return
		}

		log.Printf("Unknown query: %s", bodyStr)
		http.Error(w, "query not supported", http.StatusBadRequest)
	})

	log.Println("Starting Inventory service on port 8888")
	if err := http.ListenAndServe(":8888", nil); err != nil {
		log.Fatal(err)
	}
}
