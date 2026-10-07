FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -o /server ./cmd/server

FROM alpine:3.21
RUN apk add --no-cache ca-certificates && adduser -D -u 10001 appuser && mkdir /data && chown appuser /data
WORKDIR /app
COPY --from=build /server /app/server
USER appuser
ENV APP_ADDR=0.0.0.0:8080 DB_PATH=/data/access.db
EXPOSE 8080
CMD ["/app/server"]
