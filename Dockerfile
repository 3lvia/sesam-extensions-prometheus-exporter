# Build
FROM library/golang:1.18.5-alpine3.15 as build
LABEL maintainer="feng.luan@elvia.no"

ENV GO111MODULE=on

WORKDIR /app
COPY go.mod ./
COPY go.sum ./
Run go mod download
Copy *.go ./

RUN go build -o ./out/prometheus-sesam-exporter

# Deploy
FROM gcr.io/distroless/base-debian10

WORKDIR /

COPY --from=build /app/out/prometheus-sesam-exporter /prometheus-sesam-exporter
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/prometheus-sesam-exporter"]
