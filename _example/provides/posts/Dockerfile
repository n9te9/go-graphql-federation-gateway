FROM golang:1.25-alpine
WORKDIR /app
COPY go.mod ./
RUN go mod download
COPY . .
RUN go build -o server .
CMD ["./server"]
