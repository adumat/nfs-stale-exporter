FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
ARG REVISION=none
RUN CGO_ENABLED=0 go build -trimpath \
      -ldflags="-s -w -X main.version=${VERSION} -X main.revision=${REVISION}" \
      -o /nfs-stale-exporter .

FROM scratch
COPY --from=build /nfs-stale-exporter /nfs-stale-exporter
EXPOSE 9855
ENTRYPOINT ["/nfs-stale-exporter"]
