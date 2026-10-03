# The compiler and race-test tools stay in this build stage.
FROM golang:1.27.1-alpine3.24@sha256:8a5910f31396cd4d89662f56c68b3ae31d374308270a1c3bd96672ee5ed43414 AS build

WORKDIR /src

RUN apk add --no-cache ca-certificates gcc musl-dev

COPY go.mod go.sum ./
RUN go mod download

COPY *.go ./

# Validate concurrency in Linux, then compile a smaller standalone binary.
RUN CGO_ENABLED=1 go test -race -timeout 60s ./...
RUN CGO_ENABLED=0 GOOS=linux go build -buildvcs=false -trimpath -ldflags="-s -w" -o /out/cloudProject01 .

# The final image contains the program and the certificates needed for HTTPS.
FROM scratch

COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/cloudProject01 /cloudProject01

WORKDIR /
ENTRYPOINT ["/cloudProject01"]
