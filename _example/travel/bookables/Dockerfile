FROM golang:1.23-alpine
WORKDIR /app
COPY . .
RUN go mod download
RUN go build -o bookables
CMD ["./bookables"]
