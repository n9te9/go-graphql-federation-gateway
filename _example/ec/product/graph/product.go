package graph

import "github.com/n9te9/go-graphql-federation-gateway/_example/ec/product/graph/model"

var products = map[string]*model.Product{
	"1": {
		Upc:    "1",
		Name:   "hogehoge",
		Price:  &[]int32{1000}[0],
		Weight: &[]int32{30}[0],
	},
	"2": {
		Upc:    "2",
		Name:   "fugafuga",
		Price:  &[]int32{2000}[0],
		Weight: &[]int32{40}[0],
	},
	"3": {
		Upc:    "3",
		Name:   "piyopiyo",
		Price:  &[]int32{3000}[0],
		Weight: nil,
	},
}
