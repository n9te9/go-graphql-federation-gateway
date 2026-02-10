package graph

import "github.com/n9te9/go-graphql-federation-gateway/_example/ec/review/graph/model"

var users = map[string]*model.User{
	"1": {
		ID: "1",
	},
	"2": {
		ID: "2",
	},
	"3": {
		ID: "3",
	},
}

var reviews = map[string]*model.Review{
	"1": {
		ID:   "1",
		Body: &[]string{"Great product!"}[0],
		Product: &model.Product{
			Upc: "1",
		},
		Author: &model.User{
			ID: "1",
		},
	},
	"2": {
		ID:   "2",
		Body: &[]string{"hogehoge"}[0],
		Product: &model.Product{
			Upc: "2",
		},
		Author: &model.User{
			ID: "2",
		},
	},
	"3": {
		ID:   "3",
		Body: &[]string{"ummm"}[0],
		Product: &model.Product{
			Upc: "3",
		},
		Author: &model.User{
			ID: "3",
		},
	},
}

var products = map[string]*model.Product{
	"1": {
		Upc: "1",
		Reviews: []*model.Review{
			{
				Body: &[]string{"Great product!"}[0],
				Author: &model.User{
					ID: "1",
				},
				ID: "1",
			},
		},
	},
	"2": {
		Upc: "2",
		Reviews: []*model.Review{
			{
				Body: &[]string{"hogehoge"}[0],
				Author: &model.User{
					ID: "2",
				},
				ID: "2",
			},
		},
	},
	"3": {
		Upc: "3",
		Reviews: []*model.Review{
			{
				Body: &[]string{"ummm"}[0],
				Author: &model.User{
					ID: "3",
				},
				ID: "3",
			},
		},
	},
}
