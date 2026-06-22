# openrate — single static binary with the web UI embedded (web/dist is
# committed and baked in via go:embed). No Node needed at build or run time.
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /openrate ./cmd/openrate

FROM alpine:3.20
RUN apk add --no-cache ca-certificates && adduser -D -u 10001 openrate
USER openrate
COPY --from=build /openrate /usr/local/bin/openrate
EXPOSE 8080
ENTRYPOINT ["openrate"]
CMD ["-addr", ":8080", "-base", "ZAR", "-refresh", "5m"]
