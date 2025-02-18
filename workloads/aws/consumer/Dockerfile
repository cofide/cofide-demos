FROM golang:1.22-alpine AS builder
WORKDIR /build
COPY . /build/
RUN cd /build && go build -o main
 
FROM alpine:3
COPY --from=builder /build/main /app/
WORKDIR /app
EXPOSE 9090
ENTRYPOINT ["/app/main"]
