FROM golang:1.26-alpine 

WORKDIR /app

COPY . .

RUN go install github.com/n9te9/go-graphql-federation-gateway/cmd/go-graphql-federation-gateway@latest

CMD ["go-graphql-federation-gateway", "serve"]