FROM golang:1.23-alpine AS builder

RUN apk add --no-cache gcc musl-dev sqlite-dev git

WORKDIR /app

COPY go.mod ./
RUN go mod download || true

COPY . .
RUN go mod tidy && \
    CGO_ENABLED=1 GOOS=linux go build -ldflags="-w -s" -o connector ./cmd/server

FROM alpine:3.19

RUN apk add --no-cache ca-certificates sqlite-libs tzdata

WORKDIR /app

COPY --from=builder /app/connector .

RUN mkdir -p /app/sessions /app/media

EXPOSE 3000

CMD ["./connector"]
