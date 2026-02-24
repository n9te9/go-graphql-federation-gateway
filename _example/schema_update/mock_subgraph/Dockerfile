FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY go.mod ./
RUN go mod download
COPY . .
RUN go build -o mock_subgraph .

FROM alpine:3.20
WORKDIR /app
COPY --from=builder /app/mock_subgraph .
ENTRYPOINT ["./mock_subgraph"]
