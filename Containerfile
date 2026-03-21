FROM docker.io/library/golang:1.26-bookworm AS build

WORKDIR /src
ENV GOWORK=off
ENV CGO_ENABLED=1

COPY . .
RUN mkdir -p /out \
    && export GOPATH=/tmp/go \
    && export GOMODCACHE=/tmp/go/pkg/mod \
    && export GOCACHE=/tmp/go/build-cache \
    && go build -trimpath -mod=mod -o /out/mirageserver . \
    && rm -rf /tmp/go /root/.cache/go-build

FROM docker.io/library/debian:bookworm-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /var/lib/mirageserver
RUN mkdir -p /var/lib/mirageserver/download

COPY --from=build /out/mirageserver /usr/local/bin/mirageserver

EXPOSE 8080 8081

CMD ["/usr/local/bin/mirageserver"]
