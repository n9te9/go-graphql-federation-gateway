package graph

import "github.com/go-graphql-federation-gateway/_example/ec/shipping/graph/model"

var products = map[string]*model.Product{
	"1": {
		Upc:    "1",
		Price:  &[]int32{1000}[0],
		Weight: &[]int32{30}[0],
	},
	"2": {
		Upc:    "2",
		Price:  &[]int32{2000}[0],
		Weight: &[]int32{40}[0],
	},
	"3": {
		Upc:    "3",
		Price:  &[]int32{3000}[0],
		Weight: nil,
	},
}
