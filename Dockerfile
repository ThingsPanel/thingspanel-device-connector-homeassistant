FROM golang:1.24-alpine AS build
WORKDIR /src
COPY . /src
RUN go build -o /out/homeassistant-service .

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=build /out/homeassistant-service /usr/local/bin/homeassistant-service
EXPOSE 9001
ENTRYPOINT ["/usr/local/bin/homeassistant-service"]
