package server

import (
	"log"
	"net/http"

	"github.com/n9te9/federation-gateway/gateway"
)

func RunGateway() {
	gw := gateway.NewGateway()
	srv := &http.Server{
		Addr:    ":8081",
		Handler: gw,
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil {
			log.Fatal(err)
		}
	}()
}
