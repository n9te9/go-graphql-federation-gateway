package graph

import "github.com/n9te9/go-graphql-federation-gateway/_example/ec/account/graph/model"

var users = map[string]*model.User{
	"1": {ID: "1", Username: "Alice"},
	"2": {ID: "2", Username: "Bob"},
	"3": {ID: "3", Username: "Charlie"},
}
