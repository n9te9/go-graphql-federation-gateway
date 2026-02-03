package graph

import (
	"context"

	"github.com/n9te9/go-graphql-federation-gateway/_example/ec/review/graph/model"
)

var products = map[string]*model.Product{
	"1": {
		Upc: "1",
		Reviews: []*model.Review{
			{
				Body: &[]string{"Great book! Must read."}[0],
				Author: &model.User{
					ID: "1",
				},
				Product: &model.Product{Upc: "1"},
			},
			{
				Body: &[]string{"Not my taste."}[0],
				Author: &model.User{
					ID: "2",
				},
				Product: &model.Product{Upc: "1"},
			},
		},
	},
	"2": {
		Upc: "2",
		Reviews: []*model.Review{
			{
				Body: &[]string{"Informative and well-written."}[0],
				Author: &model.User{
					ID: "3",
				},
				Product: &model.Product{Upc: "2"},
			},
		},
	},
}

func (r *entityResolver) FindProductByUpc(ctx context.Context, upc string) (*model.Product, error) {
	if product, ok := products[upc]; ok {
		return product, nil
	}

	return &model.Product{
		Upc:     upc,
		Reviews: []*model.Review{},
	}, nil
}

func (r *entityResolver) FindUserByID(ctx context.Context, id string) (*model.User, error) {
	var userReviews []*model.Review

	for _, p := range products {
		for _, review := range p.Reviews {
			if review.Author.ID == id {
				userReviews = append(userReviews, review)
			}
		}
	}

	return &model.User{
		ID:      id,
		Reviews: userReviews,
	}, nil
}

func (r *Resolver) Entity() EntityResolver { return &entityResolver{r} }

type entityResolver struct{ *Resolver }
