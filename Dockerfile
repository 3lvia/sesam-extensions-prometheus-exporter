FROM library/golang:1.18.5-alpine3.15 as build
LABEL maintainer="feng.luan@elvia.no"

ENV GO111MODULE=on

WORKDIR /app
COPY . .
RUN go mod download \
    && CGO_ENABLED=0 \
    GOOS=linux GOARCH=amd64 \
    go build \
    -o ./out/prometheus-sesam-exporter


FROM library/alpine:3.14 as runtime
LABEL maintainer="feng.luan@elvia.no"

RUN addgroup application-group --gid 1001 \
    && adduser application-user --uid 1001 \
    --ingroup application-group \
    --disabled-password

# RUN apk add \
#     'apache2-utils>=2.4.51-r0' \
#     'ca-certificates>=20191127-r5' \
#     --no-cache

WORKDIR /app
COPY --from=build /app/out .
RUN chown --recursive application-user .
USER application-user
EXPOSE 8080
ENTRYPOINT ["/app/prometheus-sesam-exporter"]
CMD ["-config.vault"]
