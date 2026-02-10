FROM golang:1.24-alpine

WORKDIR /app

COPY . .

RUN go install github.com/99designs/gqlgen@latest

CMD ["go", "run", "graph/resolver.go"]