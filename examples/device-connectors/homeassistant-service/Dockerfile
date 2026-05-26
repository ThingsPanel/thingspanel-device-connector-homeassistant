FROM golang:1.22-alpine AS build
WORKDIR /src
COPY connector-sdk/go /src/connector-sdk/go
COPY examples/device-connectors/homeassistant-service /src/examples/device-connectors/homeassistant-service
WORKDIR /src/examples/device-connectors/homeassistant-service
RUN go build -o /out/homeassistant-service .

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=build /out/homeassistant-service /usr/local/bin/homeassistant-service
EXPOSE 9001
ENTRYPOINT ["/usr/local/bin/homeassistant-service"]
