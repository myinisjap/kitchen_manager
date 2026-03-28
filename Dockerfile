# syntax=docker/dockerfile:1

# ---- builder ----
FROM golang:1.26-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o kitchen_manager .

# ---- runtime ----
FROM alpine:3.21
WORKDIR /app
COPY --from=builder /src/kitchen_manager .
COPY --from=builder /src/static ./static
EXPOSE 8080
CMD ["./kitchen_manager"]
