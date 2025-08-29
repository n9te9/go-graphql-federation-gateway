package main

import (
	"log"

	"github.com/n9te9/federation-gateway/server"
)

var userSDL = `type User @key(fields: "id") {
  id: ID!
  name: String!
  email: String!
}

type Query {
  me: User
  user(id: ID!): User
}`

var productSDL = `type Product @key(fields: "id") {
  id: ID!
  name: String!
  price: Int!
  createdBy: User @provides(fields: "id")
}

extend type User @key(fields: "id") {
  id: ID! @external
  products: [Product]
}

type Query {
  product(id: ID!): Product
}`

var subgraphs = []*server.Graph{
	{Name: "users", Host: "localhost:3001", SDL: userSDL},
	{Name: "products", Host: "localhost:3002", SDL: productSDL},
}

func main() {
	if err := server.Run(subgraphs); err != nil {
		log.Fatal(err)
	}
}
