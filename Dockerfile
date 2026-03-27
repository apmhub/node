FROM golang:1.26-alpine3.23 as builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download
COPY . .

RUN go build -ldflags "-s -w" -o /app/app_bin ./main.go

FROM alpine:3.23
WORKDIR /app

COPY --from=builder /app/app_bin .
RUN chmod +x app_bin
CMD ["./app_bin"]