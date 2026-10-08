# -----------------------------
# Stage 1: Build
# -----------------------------
FROM golang:1.26-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build \
    -o /vehicle-service \
    ./vehicle-service

# -----------------------------
# Stage 2: Runtime
# -----------------------------
FROM alpine:3.22

WORKDIR /app

COPY --from=builder /vehicle-service .

EXPOSE 50051

CMD ["./vehicle-service"]