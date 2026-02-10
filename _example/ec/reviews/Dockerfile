FROM golang:1.25-alpine

WORKDIR /app

COPY . .

RUN go mod tidy

CMD ["go", "run", ""]